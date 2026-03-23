package caPWQL5

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
	sentToMem
	l1Done
	memDone
	transactionFinished
)

type transactionImpl struct {
	akita.MsgMeta

	req     *device.TranslationReq
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

	mmu                  *CaPWQMMU
	transaction          *transactionImpl
	secondaryTransaction *transactionImpl
	info                 uint64
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
	if walker.transaction == nil && walker.secondaryTransaction == nil {
		return false
	}

	return walker.walkPageTable(now)
}

func (walker *CaPWQPageWalker) CanAccept() bool {
	return walker.transaction == nil || walker.secondaryTransaction == nil
}

func (walker *CaPWQPageWalker) CanAcceptPrimary() bool {
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

	if walker.transaction == nil {
		walker.transaction = transaction
		walker.transaction.req = req
	} else {
		walker.secondaryTransaction = transaction
	}

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

func (walker *CaPWQPageWalker) AcceptL1CacheRsp(
	now akita.VTimeInSec,
	rsp *mem.DataReadyRsp,
) {
	if !walker.CanAccept() {
		panic("walker can't accept")
	}

	block := rsp.Info.(vm.CaPWQBlock)

	newTrans := &transactionImpl{
		state:   l1Done,
		Address: block.Address,
		pid:     block.PID,
		msgID:   block.MsgID,
		PPN:     binary.LittleEndian.Uint64(rsp.Data),
		level:   block.Level,
	}

	if newTrans.level+1 == 4 {
		newTrans.state = transactionFinished
	} else {
		walker.fillPageWalkCache(now, newTrans)
	}
	newTrans.level++

	if walker.transaction == nil {
		walker.transaction = newTrans
	} else {
		walker.secondaryTransaction = newTrans
	}

	walker.TickLater(now)
}

func (walker *CaPWQPageWalker) AcceptMemRsp(
	now akita.VTimeInSec,
	rsp *mem.DataReadyRsp,
) {
	if walker.transaction == nil {
		panic("walker has no primary transaction")
	}

	trans := walker.transaction

	trans.PPN = binary.LittleEndian.Uint64(rsp.Data)
	trans.state = memDone

	tracing.AddTaskStepWithDetail(
		"",
		now,
		walker.mmu,
		"ptw-mem-req",
		trans,
	)

	if trans.level+1 == 4 {
		trans.state = transactionFinished
	} else {
		walker.fillPageWalkCache(now, trans)
	}
	trans.level++

	tracing.EndTask(rsp.RespondTo, now, walker.mmu)

	walker.TickLater(now)
}

func (walker *CaPWQPageWalker) walkPageTable(now akita.VTimeInSec) bool {
	if walker.transaction == nil || walker.transaction.state == sentToMem {
		return walker.walkSecondaryPageTable(now)
	}

	switch walker.transaction.state {
	case pageWalkCacheDone, memDone, l1Done:
		walker.sendToMem(now)
	case transactionFinished:
		walker.finalizeTransaction(now, walker.transaction)
	default:
		panic("invalid transaction state")
	}

	return true
}

func (walker *CaPWQPageWalker) walkSecondaryPageTable(now akita.VTimeInSec) bool {
	if walker.secondaryTransaction == nil {
		return false
	}

	switch walker.secondaryTransaction.state {
	case pageWalkCacheDone, l1Done:
		walker.sendWriteReqToL1(now)
	case transactionFinished:
		walker.finalizeTransaction(now, walker.secondaryTransaction)
	default:
		panic("invalid transaction state")
	}

	return true
}

func (walker *CaPWQPageWalker) sendToMem(now akita.VTimeInSec) {
	trans := walker.transaction

	transState := trans.state
	if transState != pageWalkCacheDone &&
		transState != memDone && transState != l1Done {
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

	trans.vAddr = walker.mmu.pageTable.NextLevel(trans.vAddr)
	trans.msgID = readReq.ID
	trans.state = sentToMem

	partitionID := mmu.ExtractMPID(dstPort.Name())

	if partitionID < 4 {
		tracing.AddTaskStep("",
			now, walker.mmu, "page_walk_req_left")
	} else {
		tracing.AddTaskStep("",
			now, walker.mmu, "page_walk_req_right")
	}

	tracing.AddTaskStep("",
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

func (walker *CaPWQPageWalker) sendWriteReqToL1(now akita.VTimeInSec) {
	trans := walker.secondaryTransaction

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
		tracing.StartTask(walker.mmu.Name()+"stall", "", now, walker.mmu, "mmu_stall", "", nil)
		return
	}

	walker.secondaryTransaction = nil

	tracing.AddTaskStep(tracing.MsgIDAtReceiver(writeReq, walker.mmu),
		now, walker.mmu, "page_walk_store_l1")
	tracing.EndTask(walker.mmu.Name()+"stall", now, walker.mmu)
}

func (walker *CaPWQPageWalker) fillPageWalkCache(
	now akita.VTimeInSec,
	trans *transactionImpl,
) {
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
	trans *transactionImpl,
) {
	if !walker.mmu.topSender.CanSend(1) {
		return
	}

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

	if walker.transaction == trans {
		walker.transaction = nil
	} else {
		walker.secondaryTransaction = nil
	}

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

	log2CacheLineSize uint64

	numInflightPTWRequests uint64

	pageWalkReqQueue []*device.TranslationReq
	pageWalkRspQueue []*mem.DataReadyRsp

	walkReqQueueCapacity int
	walkRspQueueCapacity int
}

// Tick defines how the MMU update state each cycle
func (mmu *CaPWQMMU) Tick(now akita.VTimeInSec) bool {
	mmu.topSender.Tick(now)
	mmu.translationSender.Tick(now)
	mmu.processPageWalkRspQueue(now)
	mmu.parseFromMem(now)
	mmu.parseFromL1(now)
	mmu.parseFromPageWalkCache(now)
	mmu.parseFromTop(now)
	mmu.processPageWalkReqQueue(now)

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
			strconv.Itoa(len(mmu.pageWalkReqQueue)),
			nil,
		)
		tracing.StartTask(
			"",
			"",
			now,
			mmu,
			"page_walk_rsp_queue_len",
			strconv.Itoa(len(mmu.pageWalkRspQueue)),
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
		mmu.handleMemResponse(msg, now)
	default:
		panic("unknown message type")
	}

	return false
}

func (mmu *CaPWQMMU) handleMemResponse(rsp *mem.DataReadyRsp, now akita.VTimeInSec) {
	for _, walker := range mmu.pageWalkers {
		if walker.transaction == nil {
			continue
		}

		if walker.transaction.msgID == rsp.RespondTo {
			walker.AcceptMemRsp(now, rsp)

			mmu.TranslationPort.Retrieve(now)

			return
		}
	}
	panic("response doesn't match any inflight transaction")
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

func (mmu *CaPWQMMU) handleL1ReadResponse(rsp *mem.DataReadyRsp, now akita.VTimeInSec) bool {
	if len(mmu.pageWalkRspQueue) >= mmu.walkRspQueueCapacity {
		return false
	}

	mmu.pageWalkRspQueue = append(mmu.pageWalkRspQueue, rsp)

	mmu.ToCache.Retrieve(now)

	return true
}

func (mmu *CaPWQMMU) parseFromTop(now akita.VTimeInSec) bool {
	item := mmu.ToTop.Peek()
	if item == nil {
		return false
	}

	if len(mmu.pageWalkReqQueue) >= mmu.walkReqQueueCapacity {
		return false
	}

	req, ok := item.(*device.TranslationReq)
	if !ok {
		panic(fmt.Sprintf("item isn't a translation request: %s", reflect.TypeOf(item)))
	}

	mmu.pageWalkReqQueue = append(mmu.pageWalkReqQueue, req)

	mmu.ToTop.Retrieve(now)

	return false
}

func (mmu *CaPWQMMU) processPageWalkReqQueue(now akita.VTimeInSec) bool {
	if len(mmu.pageWalkReqQueue) == 0 {
		return false
	}

	head := mmu.pageWalkReqQueue[0]

	for _, walker := range mmu.pageWalkers {
		if !walker.CanAcceptPrimary() {
			continue
		}

		walker.AcceptReqFromTop(
			now,
			head,
		)

		mmu.numInflightPTWRequests++

		mmu.pageWalkReqQueue = mmu.pageWalkReqQueue[1:]

		return true
	}

	for _, walker := range mmu.pageWalkers {
		if !walker.CanAccept() {
			continue
		}

		walker.AcceptReqFromTop(
			now,
			head,
		)

		mmu.numInflightPTWRequests++

		mmu.pageWalkReqQueue = mmu.pageWalkReqQueue[1:]

		return true
	}

	return false
}

func (mmu *CaPWQMMU) processPageWalkRspQueue(now akita.VTimeInSec) bool {
	if len(mmu.pageWalkRspQueue) == 0 {
		return false
	}

	head := mmu.pageWalkRspQueue[0]

	for _, walker := range mmu.pageWalkers {
		if !walker.CanAccept() {
			continue
		}

		walker.AcceptL1CacheRsp(
			now,
			head,
		)

		mmu.pageWalkRspQueue = mmu.pageWalkRspQueue[1:]

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
		if mmu.pageWalkers[i].secondaryTransaction != nil {
			num++
		}
	}
	return num
}

func (mmu *CaPWQMMU) isActive() bool {
	for i := range mmu.pageWalkers {
		if mmu.pageWalkers[i].secondaryTransaction != nil {
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
