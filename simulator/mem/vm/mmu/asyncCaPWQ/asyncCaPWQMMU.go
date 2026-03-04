package asyncCaPWQ

import (
	"encoding/binary"
	"fmt"
	"reflect"
	"strconv"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/mem/cache"
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/mem/vm"
	"gitlab.com/akita/mem/vm/lds"
	"gitlab.com/akita/mem/vm/mmu"
	"gitlab.com/akita/util/akitaext"
	"gitlab.com/akita/util/ca"
	"gitlab.com/akita/util/tracing"
)

type transactionState int

const (
	pageWalkCacheDone transactionState = iota
	sentWRToL1
	memDone
	sentRDToL1
	l1Done
	sentWRToLDS
	sentRDToLDS
	transactionFinished
)

type transactionImpl struct {
	akita.MsgMeta

	level   int
	msgID   string
	state   transactionState
	Address uint64
	PPN     uint64
	vAddr   uint64
	pid     ca.PID
}

type AsyncCaPWQPageWalker struct {
	*akita.TickingComponent

	mmu         *AsyncCaPWQMMU
	transaction *transactionImpl
	info        uint64
}

func newAsyncCaPWQPageWalker(mmu *AsyncCaPWQMMU, id int) *AsyncCaPWQPageWalker {
	walker := &AsyncCaPWQPageWalker{
		mmu: mmu,
	}

	walker.TickingComponent = akita.NewTickingComponent(
		fmt.Sprintf("AsyncCaPWQPageWalker_%02d", id),
		mmu.Engine,
		mmu.Freq,
		walker,
	)

	return walker
}

func (walker *AsyncCaPWQPageWalker) Tick(now akita.VTimeInSec) bool {
	if walker.transaction == nil {
		return false
	}

	return walker.walkPageTable(now)
}

func (walker *AsyncCaPWQPageWalker) CanAccept() bool {
	return walker.transaction == nil
}

func (walker *AsyncCaPWQPageWalker) AcceptReqFromTop(
	now akita.VTimeInSec,
	req *device.TranslationReq,
) {
	if !walker.CanAccept() {
		panic("walker can't accept")
	}

	// Initialize a transaction
	rearrangedVAddr := walker.mmu.pageTable.Rearrange(req.VAddr)
	root := walker.mmu.pageTable.GetRoot(req.PID)

	transaction := &transactionImpl{
		state:   pageWalkCacheDone,
		Address: req.VAddr,
		pid:     req.PID,
		msgID:   akita.GetIDGenerator().Generate(),
		PPN:     root,
		vAddr:   rearrangedVAddr,
		level:   0,
	}

	if req.Data != nil {
		rspData := binary.LittleEndian.Uint64(req.Data)
		transaction.PPN = rspData & ^uint64(3)
		level := int(rspData & uint64(3))
		transaction.vAddr = walker.mmu.pageTable.MoveToLevel(
			transaction.vAddr,
			level+1,
		)
		transaction.level = level + 1
	}

	walker.transaction = transaction

	tracing.AddTaskStep("",
		now, walker.mmu, "pwc-hit-level"+strconv.Itoa(transaction.level))

	tracing.StartTask(
		transaction.msgID,
		"",
		now,
		walker.mmu,
		"req_in",
		"",
		nil,
	)

	walker.TickLater(now)
}

func (walker *AsyncCaPWQPageWalker) AcceptLDSRsp(
	now akita.VTimeInSec,
	entry lds.IdealLDSEntry,
) {
	if !walker.CanAccept() {
		panic("walker can't accept")
	}

	walker.info = entry.Address
	walker.transaction = &transactionImpl{
		state: memDone,
		pid:   entry.PID,
		PPN:   entry.PPN,
	}

	walker.mmu.numResponseInLDS--

	walker.TickLater(now)
}

func (walker *AsyncCaPWQPageWalker) AcceptMemoryRsp(
	now akita.VTimeInSec,
	rsp *mem.DataReadyRsp,
) {
	if !walker.CanAccept() {
		panic("walker can't accept")
	}

	PPN := binary.LittleEndian.Uint64(rsp.Data)
	if PPN == 0 {
		panic("invalid page walk result, PPN is 0")
	}

	walker.info = rsp.Info.(*mem.DataReadyRspInfo).Address
	walker.transaction = &transactionImpl{
		state: memDone,
		pid:   rsp.PID,
		PPN:   PPN,
	}

	if len(walker.mmu.pageWalkQueue) >= walker.mmu.maxPageWalkQueueSize {
		walker.mmu.drainingPWQ = true
	}

	if walker.mmu.drainingPWQ {
		walker.transaction.state = sentWRToLDS
	}

	tracing.EndTask(rsp.RespondTo, now, walker.mmu)

	walker.TickLater(now)
}

func (walker *AsyncCaPWQPageWalker) AcceptL1CacheRsp(
	now akita.VTimeInSec,
	rsp *mem.DataReadyRsp,
) {
	if !walker.CanAccept() {
		panic("walker can't accept")
	}

	block := rsp.Info.(vm.CaPWQBlock)

	for j, trans := range walker.mmu.pageWalkQueue {
		if trans.state != sentRDToL1 || trans.PPN != block.PPN {
			continue
		}

		walker.transaction = trans

		trans.pid = block.PID
		trans.state = l1Done
		trans.level = block.Level
		trans.Address = block.Address
		trans.msgID = block.MsgID

		if trans.level+1 == 4 {
			trans.state = transactionFinished
		} else {
			walker.fillPageWalkCache(now)
		}
		trans.level++

		walker.mmu.pageWalkQueue = append(
			walker.mmu.pageWalkQueue[:j],
			walker.mmu.pageWalkQueue[j+1:]...,
		)

		if len(walker.mmu.pageWalkQueue) == 0 {
			walker.mmu.drainingPWQ = false
		}

		walker.TickLater(now)

		return
	}
	panic(fmt.Sprintf("%s: no match transactions!", walker.Name()))
}

func (walker *AsyncCaPWQPageWalker) walkPageTable(now akita.VTimeInSec) bool {
	if walker.transaction == nil {
		panic("empty walker can't walkPageTable")
	}

	switch walker.transaction.state {
	case pageWalkCacheDone, l1Done:
		walker.sendWriteReqToL1(now)
	case sentWRToL1:
		walker.sendToMem(now)
	case sentWRToLDS:
		walker.sendWriteReqToLDS(now)
	case sentRDToLDS:
		walker.sendReadReqToLDS(now)
	case memDone:
		walker.sendReadReqToL1(now)
	case transactionFinished:
		walker.finalizeTransaction(now)
	default:
		panic("invalid transaction state")
	}

	return walker.transaction != nil
}

func (walker *AsyncCaPWQPageWalker) sendReadReqToL1(now akita.VTimeInSec) {
	trans := walker.transaction

	if trans.state != memDone {
		panic("this state shouldn't be here!")
	}

	dstPort := walker.mmu.CacheLowModuleFinder.Find(walker.info)

	readReq := mem.ReadReqBuilder{}.
		WithSendTime(now).
		WithSrc(walker.mmu.ToCache).
		WithDst(dstPort).
		WithPID(trans.pid).
		WithAddress(walker.info).
		WithInfo(trans.PPN).
		Build()

	readReq.TrafficBytes += 8

	err := walker.mmu.ToCache.Send(readReq)
	if err != nil {
		return
	}

	trans.state = sentRDToL1 // Just for debugging.

	walker.mmu.pageWalkQueue = append(
		walker.mmu.pageWalkQueue,
		trans,
	)
	walker.transaction = nil

	tracing.AddTaskStep(tracing.MsgIDAtReceiver(readReq, walker.mmu),
		now, walker.mmu, "page_walk_load_l1")
}

func (walker *AsyncCaPWQPageWalker) sendWriteReqToL1(now akita.VTimeInSec) {
	trans := walker.transaction

	if trans.state != pageWalkCacheDone && trans.state != l1Done {
		panic("this state shouldn't be here!")
	}

	if trans.state == l1Done {
		trans.vAddr = walker.mmu.pageTable.MoveFromVAddrToLevel(
			trans.Address,
			trans.level,
		)
	}

	PPN := trans.PPN
	PPNWithOffset := walker.mmu.pageTable.AddOffset(PPN, trans.vAddr)

	dstPort := walker.mmu.CacheLowModuleFinder.Find(PPNWithOffset)

	block := vm.CaPWQBlock{
		PID:           trans.pid,
		Address:       trans.Address,
		PPNWithOffset: PPNWithOffset,
		Level:         trans.level,
		MsgID:         trans.msgID,
	}

	writeReq := mem.WriteReqBuilder{}.
		WithSendTime(now).
		WithSrc(walker.mmu.ToCache).
		WithDst(dstPort).
		WithPID(trans.pid).
		WithAddress(PPNWithOffset).
		WithInfo(block).
		Build()

	writeReq.TrafficBytes += 12

	err := walker.mmu.ToCache.Send(writeReq)
	if err != nil {
		return
	}

	trans.state = sentWRToL1

	tracing.AddTaskStep(tracing.MsgIDAtReceiver(writeReq, walker.mmu),
		now, walker.mmu, "page_walk_store_l1")
}

func (walker *AsyncCaPWQPageWalker) sendWriteReqToLDS(now akita.VTimeInSec) {
	trans := walker.transaction

	if trans.state != sentWRToLDS {
		panic("this state shouldn't be here!")
	}

	entry := lds.IdealLDSEntry{
		PID:     trans.pid,
		Address: walker.info,
		PPN:     trans.PPN,
	}

	writeReq := mem.WriteReqBuilder{}.
		WithSendTime(now).
		WithSrc(walker.mmu.ToLDS).
		WithDst(walker.mmu.LDS).
		WithInfo(entry).
		Build()

	writeReq.TrafficBytes += 16

	err := walker.mmu.ToLDS.Send(writeReq)
	if err != nil {
		return
	}

	walker.transaction = nil

	walker.mmu.numResponseInLDS++

	tracing.AddTaskStep(tracing.MsgIDAtReceiver(writeReq, walker.mmu),
		now, walker.mmu, "page_walk_store_lds")
}

func (walker *AsyncCaPWQPageWalker) sendReadReqToLDS(now akita.VTimeInSec) {
	trans := walker.transaction

	if trans.state != sentRDToLDS {
		panic("this state shouldn't be here!")
	}

	readReq := mem.ReadReqBuilder{}.
		WithSendTime(now).
		WithSrc(walker.mmu.ToLDS).
		WithDst(walker.mmu.LDS).
		Build()

	err := walker.mmu.ToLDS.Send(readReq)
	if err != nil {
		return
	}

	walker.transaction = nil

	tracing.AddTaskStep(tracing.MsgIDAtReceiver(readReq, walker.mmu),
		now, walker.mmu, "page_walk_load_lds")
}

func (walker *AsyncCaPWQPageWalker) sendToMem(now akita.VTimeInSec) {
	trans := walker.transaction

	if trans.state != sentWRToL1 {
		panic("this state shouldn't be here!")
	}

	PPN := trans.PPN
	PPNWithOffset := walker.mmu.pageTable.AddOffset(PPN, trans.vAddr)

	srcPort := walker.mmu.TranslationPort
	readReqInfo := &mem.ReadReqInfo{ReturnAccessInfo: true}
	dstPort := walker.mmu.lowModuleFinder.Find(PPNWithOffset)

	readReq := mem.ReadReqBuilder{}.
		WithSendTime(now).
		WithSrc(srcPort).
		WithDst(dstPort).
		WithPID(trans.pid).
		WithAddress(PPNWithOffset).
		WithByteSize(8).
		WithInfo(readReqInfo).
		Build()

	readReq.PTW = true

	err := srcPort.Send(readReq)
	if err != nil {
		return
	}

	walker.transaction = nil

	// load requests from LDS.
	if walker.mmu.numResponseInLDS > 0 {
		walker.transaction = &transactionImpl{
			state: sentRDToLDS,
		}
	}

	partitionID := mmu.ExtractMPID(dstPort.Name())

	if partitionID < 4 {
		tracing.AddTaskStep(readReq.ID,
			now, walker.mmu, "page_walk_req_left")
	} else {
		tracing.AddTaskStep(readReq.ID,
			now, walker.mmu, "page_walk_req_right")
	}

	tracing.AddTaskStep(readReq.ID,
		now, walker.mmu, "page_walk_req_local")

	tracing.StartTask(
		readReq.ID,
		"",
		now,
		walker.mmu,
		"walker_mem_latency",
		reflect.TypeOf(readReq).String(),
		readReq,
	)
}

func (walker *AsyncCaPWQPageWalker) fillPageWalkCache(
	now akita.VTimeInSec,
) {
	trans := walker.transaction

	level := uint64(trans.level)
	data := mmu.Uint64ToBytes(trans.PPN | level)

	writeReq := mem.WriteReqBuilder{}.
		WithSendTime(now).
		WithSrc(walker.mmu.ToPageWalkCache).
		WithDst(walker.mmu.PageWalkCache).
		WithPID(trans.pid).
		WithAddress(walker.mmu.pageTable.AlignToPage(trans.Address) | level).
		WithData(data).
		Build()

	// No need to check whether the send is successful.
	walker.mmu.ToPageWalkCache.Send(writeReq)
}

func (walker *AsyncCaPWQPageWalker) finalizeTransaction(
	now akita.VTimeInSec,
) {
	if !walker.mmu.topSender.CanSend(1) {
		return
	}

	trans := walker.transaction

	page, found := walker.mmu.pageTable.Find(trans.pid, trans.Address)
	if !found {
		panic("page not found")
	}

	pAddr := trans.PPN
	if pAddr != page.PAddr {
		panic("addresses don't match!")
	}

	newPage := device.Page{
		PID:   trans.pid,
		VAddr: trans.Address,
		PAddr: pAddr,
		Valid: true,
	}

	rsp := device.TranslationRspBuilder{}.
		WithSendTime(now).
		WithSrc(walker.mmu.ToTop).
		WithDst(walker.mmu.L3TLB).
		WithPage(newPage).
		Build()

	walker.mmu.topSender.Send(rsp)

	walker.mmu.numInflightPTWRequests--

	walker.transaction = nil

	tracing.EndTask(trans.msgID, now, walker.mmu)
}

// AsyncCaPWQMMU is the default mmu implementation. It is also an akita Component.
type AsyncCaPWQMMU struct {
	akita.TickingComponent

	ToTop akita.Port
	L3TLB akita.Port

	ToPageWalkCache akita.Port
	PageWalkCache   akita.Port
	topSender       akitaext.BufferedSender

	TranslationPort akita.Port
	lowModuleFinder cache.LowModuleFinder

	ToCache              akita.Port
	CacheLowModuleFinder cache.LowModuleFinder

	ToLDS akita.Port
	LDS   akita.Port

	pageTable *device.PageTableImpl

	pageWalkers []*AsyncCaPWQPageWalker

	pageWalkQueue        []*transactionImpl
	maxPageWalkQueueSize int
	maxInflightRequests  int

	log2CacheLineSize uint64

	numInflightPTWRequests uint64
	numResponseInLDS       uint64

	drainingPWQ bool
}

func (mmu *AsyncCaPWQMMU) GetMaxExtensionReqs() int {
	return mmu.maxInflightRequests - mmu.maxPageWalkQueueSize
}

// Tick defines how the MMU update state each cycle
func (mmu *AsyncCaPWQMMU) Tick(now akita.VTimeInSec) bool {
	mmu.topSender.Tick(now)
	mmu.parseFromLDS(now)
	mmu.parseFromL1(now)
	mmu.parseFromMem(now)
	mmu.parseFromPageWalkCache(now)
	mmu.parseFromTop(now)

	if mmu.isActive() {
		tracing.StartTask(
			"",
			"",
			now,
			mmu,
			"num_active_walkers",
			strconv.Itoa(int(mmu.numInflightPTWRequests)),
			nil,
		)
		tracing.StartTask(
			"",
			"",
			now,
			mmu,
			"page_walk_queue_len",
			strconv.Itoa(int(len(mmu.pageWalkQueue))),
			nil,
		)
	}

	return true
}

func (mmu *AsyncCaPWQMMU) trace(now akita.VTimeInSec, what string) {
	ctx := akita.HookCtx{
		Domain: mmu,
		Now:    now,
		Item:   what,
	}

	mmu.InvokeHook(ctx)
}

func (mmu *AsyncCaPWQMMU) parseFromPageWalkCache(now akita.VTimeInSec) bool {
	item := mmu.ToPageWalkCache.Peek()
	if item == nil {
		return false
	}

	switch item.(type) {
	case *mem.WriteDoneRsp:
		mmu.ToPageWalkCache.Retrieve(now)
		return true
	default:
		panic("unknown message type")
	}
}

func (mmu *AsyncCaPWQMMU) parseFromMem(now akita.VTimeInSec) bool {
	item := mmu.TranslationPort.Peek()
	if item == nil {
		return false
	}

	switch msg := item.(type) {
	case *mem.DataReadyRsp:
		return mmu.handleMemResponse(msg, now)
	default:
		panic("unknown message type")
	}
}

func (mmu *AsyncCaPWQMMU) parseFromLDS(now akita.VTimeInSec) bool {
	item := mmu.ToLDS.Peek()
	if item == nil {
		return false
	}

	switch msg := item.(type) {
	case *mem.DataReadyRsp:
		return mmu.handleLDSReadResponse(msg, now)
	case *mem.WriteDoneRsp:
		mmu.ToLDS.Retrieve(now)

		return true
	default:
		panic("unknown message type")
	}
}

func (mmu *AsyncCaPWQMMU) parseFromL1(now akita.VTimeInSec) bool {
	item := mmu.ToCache.Peek()
	if item == nil {
		return false
	}

	switch msg := item.(type) {
	case *mem.DataReadyRsp:
		return mmu.handleL1ReadResponse(msg, now)
	case *mem.WriteDoneRsp:
		mmu.ToCache.Retrieve(now)

		return true
	default:
		panic("unknown message type")
	}
}

func (mmu *AsyncCaPWQMMU) handleMemResponse(rsp *mem.DataReadyRsp, now akita.VTimeInSec) bool {
	if mmu.numInflightPTWRequests > uint64(mmu.maxInflightRequests) {
		panic(fmt.Sprintf("too many inflight PTW requests: %d", mmu.numInflightPTWRequests))
	}

	for i := range mmu.pageWalkers {
		if !mmu.pageWalkers[i].CanAccept() {
			continue
		}

		mmu.pageWalkers[i].AcceptMemoryRsp(
			now,
			rsp,
		)

		mmu.TranslationPort.Retrieve(now)

		return true
	}

	return false
}

func (mmu *AsyncCaPWQMMU) handleLDSReadResponse(rsp *mem.DataReadyRsp, now akita.VTimeInSec) bool {
	if mmu.numInflightPTWRequests > uint64(mmu.maxInflightRequests) {
		panic(fmt.Sprintf("too many inflight PTW requests: %d", mmu.numInflightPTWRequests))
	}

	if len(rsp.Info.([]lds.IdealLDSEntry)) == 0 {
		mmu.ToLDS.Retrieve(now)
		return true
	}

	for i := range mmu.pageWalkers {
		if !mmu.pageWalkers[i].CanAccept() {
			continue
		}

		entries, ok := rsp.Info.([]lds.IdealLDSEntry)
		if !ok {
			panic(fmt.Sprintf("invalid response info type: %s", reflect.TypeOf(rsp.Info)))
		}

		mmu.pageWalkers[i].AcceptLDSRsp(
			now,
			entries[0],
		)

		mmu.ToLDS.Retrieve(now)

		return true
	}

	return false
}

func (mmu *AsyncCaPWQMMU) handleL1ReadResponse(rsp *mem.DataReadyRsp, now akita.VTimeInSec) bool {
	for i := range mmu.pageWalkers {
		if !mmu.pageWalkers[i].CanAccept() {
			continue
		}

		mmu.pageWalkers[i].AcceptL1CacheRsp(
			now,
			rsp,
		)

		mmu.ToCache.Retrieve(now)

		return true
	}

	return false
}

func (mmu *AsyncCaPWQMMU) parseFromTop(now akita.VTimeInSec) bool {
	if mmu.numInflightPTWRequests >= uint64(mmu.maxInflightRequests) {
		return false
	}

	item := mmu.ToTop.Peek()
	if item == nil {
		return false
	}

	req, ok := item.(*device.TranslationReq)
	if !ok {
		panic(fmt.Sprintf("item isn't a translation request: %s", reflect.TypeOf(item)))
	}

	for i := range mmu.pageWalkers {
		if !mmu.pageWalkers[i].CanAccept() {
			continue
		}

		mmu.pageWalkers[i].AcceptReqFromTop(
			now,
			req,
		)

		mmu.ToTop.Retrieve(now)

		mmu.numInflightPTWRequests++

		return true
	}

	return false
}

// SetLowModuleFinder sets the table recording where to find an address.
func (mmu *AsyncCaPWQMMU) SetLowModuleFinder(lmf cache.LowModuleFinder) {
	mmu.lowModuleFinder = lmf
}

func (mmu *AsyncCaPWQMMU) GetNumActiveWalkers() int {
	num := 0
	for i := range mmu.pageWalkers {
		if mmu.pageWalkers[i].transaction != nil {
			num++
		}
	}
	return num
}

func (mmu *AsyncCaPWQMMU) isActive() bool {
	if len(mmu.pageWalkQueue) > 0 {
		return true
	}

	for i := range mmu.pageWalkers {
		if mmu.pageWalkers[i].transaction != nil {
			return true
		}
	}

	return false
}

func (mmu *AsyncCaPWQMMU) ToTopPort() akita.Port {
	return mmu.ToTop
}

func (mmu *AsyncCaPWQMMU) TranslationPortPort() akita.Port {
	return mmu.TranslationPort
}

func (mmu *AsyncCaPWQMMU) ToCachePort() akita.Port {
	return mmu.ToCache
}

func (mmu *AsyncCaPWQMMU) CanAccept() bool {
	for i := range mmu.pageWalkers {
		if mmu.pageWalkers[i].CanAccept() {
			return true
		}
	}
	return false
}

func (mmu *AsyncCaPWQMMU) ToPageWalkCachePort() akita.Port {
	return mmu.ToPageWalkCache
}
