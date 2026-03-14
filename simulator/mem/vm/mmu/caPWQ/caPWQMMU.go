package caPWQ

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
	data    []byte
}

type CaPWQPageWalker struct {
	*akita.TickingComponent

	mmu         *CaPWQMMU
	transaction *transactionImpl
	info        uint64
}

func newCaPWQPageWalker(mmu *CaPWQMMU, id int) *CaPWQPageWalker {
	walker := &CaPWQPageWalker{
		mmu: mmu,
	}

	walker.TickingComponent = akita.NewTickingComponent(
		fmt.Sprintf("%s.CaPWQPageWalker_%02d", mmu.Name(), id),
		mmu.Engine,
		mmu.Freq,
		walker,
	)

	return walker
}

func (walker *CaPWQPageWalker) Tick(now akita.VTimeInSec) bool {
	if walker.transaction == nil {
		return false
	}

	return walker.walkPageTable(now)
}

func (walker *CaPWQPageWalker) CanAccept() bool {
	return walker.transaction == nil
}

func (walker *CaPWQPageWalker) AcceptReqFromTop(
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

func (walker *CaPWQPageWalker) AcceptMemoryRsp(
	now akita.VTimeInSec,
	rsp *mem.DataReadyRsp,
) {
	if !walker.CanAccept() {
		panic("walker can't accept")
	}

	walker.info = rsp.Info.(*mem.DataReadyRspInfo).Address
	walker.transaction = &transactionImpl{
		state: memDone,
		pid:   rsp.PID,
		data:  rsp.Data,
		PPN:   binary.LittleEndian.Uint64(rsp.Data),
	}

	tracing.EndTask(rsp.RespondTo, now, walker.mmu)

	walker.TickLater(now)
}

func (walker *CaPWQPageWalker) AcceptL1CacheRsp(
	now akita.VTimeInSec,
	rsp *mem.DataReadyRsp,
) {
	if !walker.CanAccept() {
		panic("walker can't accept")
	}

	block := rsp.Info.(vm.CaPWQBlock)

	PPN := binary.LittleEndian.Uint64(rsp.Data)

	for j, trans := range walker.mmu.pageWalkQueue {
		if trans.state != sentRDToL1 || trans.PPN != PPN {
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

		walker.TickLater(now)

		return
	}
	panic(fmt.Sprintf("%s: no match transactions!", walker.Name()))
}

func (walker *CaPWQPageWalker) walkPageTable(now akita.VTimeInSec) bool {
	if walker.transaction == nil {
		panic("empty walker can't walkPageTable")
	}

	switch walker.transaction.state {
	case pageWalkCacheDone, l1Done:
		walker.sendWriteReqToL1(now)
	case sentWRToL1:
		walker.sendToMem(now)
	case memDone:
		walker.sendReadReqToL1(now)
	case transactionFinished:
		walker.finalizeTransaction(now)
	default:
		panic("invalid transaction state")
	}

	return walker.transaction != nil
}

func (walker *CaPWQPageWalker) sendReadReqToL1(now akita.VTimeInSec) {
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
		WithInfo(trans.data).
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

func (walker *CaPWQPageWalker) sendWriteReqToL1(now akita.VTimeInSec) {
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

func (walker *CaPWQPageWalker) sendToMem(now akita.VTimeInSec) {
	trans := walker.transaction

	if !walker.mmu.translationSender.CanSend(1) {
		return
	}

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

	walker.mmu.translationSender.Send(readReq)

	walker.transaction = nil

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

func (walker *CaPWQPageWalker) fillPageWalkCache(
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

func (walker *CaPWQPageWalker) finalizeTransaction(
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

// CaPWQMMU is the default mmu implementation. It is also an akita Component.
type CaPWQMMU struct {
	akita.TickingComponent

	ToTop akita.Port
	L3TLB akita.Port

	ToPageWalkCache akita.Port
	PageWalkCache   akita.Port
	topSender       akitaext.BufferedSender

	TranslationPort   akita.Port
	translationSender akitaext.BufferedSender
	lowModuleFinder   cache.LowModuleFinder

	ToCache              akita.Port
	CacheLowModuleFinder cache.LowModuleFinder

	pageTable *device.PageTableImpl

	pageWalkers []*CaPWQPageWalker

	pageWalkQueue        []*transactionImpl
	maxPageWalkQueueSize int

	log2CacheLineSize uint64

	numInflightPTWRequests uint64
}

// Tick defines how the MMU update state each cycle
func (mmu *CaPWQMMU) Tick(now akita.VTimeInSec) bool {
	mmu.topSender.Tick(now)
	mmu.translationSender.Tick(now)
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

func (mmu *CaPWQMMU) trace(now akita.VTimeInSec, what string) {
	ctx := akita.HookCtx{
		Domain: mmu,
		Now:    now,
		Item:   what,
	}

	mmu.InvokeHook(ctx)
}

func (mmu *CaPWQMMU) parseFromPageWalkCache(now akita.VTimeInSec) bool {
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

func (mmu *CaPWQMMU) parseFromMem(now akita.VTimeInSec) bool {
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

func (mmu *CaPWQMMU) parseFromL1(now akita.VTimeInSec) bool {
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

func (mmu *CaPWQMMU) handleMemResponse(rsp *mem.DataReadyRsp, now akita.VTimeInSec) bool {
	if len(mmu.pageWalkQueue) >= mmu.maxPageWalkQueueSize {
		return false
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

func (mmu *CaPWQMMU) handleL1ReadResponse(rsp *mem.DataReadyRsp, now akita.VTimeInSec) bool {
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

func (mmu *CaPWQMMU) parseFromTop(now akita.VTimeInSec) bool {
	if mmu.numInflightPTWRequests >= uint64(mmu.maxPageWalkQueueSize) {
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
func (mmu *CaPWQMMU) SetLowModuleFinder(lmf cache.LowModuleFinder) {
	mmu.lowModuleFinder = lmf
}

func (mmu *CaPWQMMU) GetNumActiveWalkers() int {
	num := 0
	for i := range mmu.pageWalkers {
		if mmu.pageWalkers[i].transaction != nil {
			num++
		}
	}
	return num
}

func (mmu *CaPWQMMU) isActive() bool {
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

func (mmu *CaPWQMMU) ToTopPort() akita.Port {
	return mmu.ToTop
}

func (mmu *CaPWQMMU) ToTranslationPort() akita.Port {
	return mmu.TranslationPort
}

func (mmu *CaPWQMMU) ToCachePort() akita.Port {
	return mmu.ToCache
}

func (mmu *CaPWQMMU) CanAccept() bool {
	for i := range mmu.pageWalkers {
		if mmu.pageWalkers[i].CanAccept() {
			return true
		}
	}
	return false
}

func (mmu *CaPWQMMU) ToPageWalkCachePort() akita.Port {
	return mmu.ToPageWalkCache
}
