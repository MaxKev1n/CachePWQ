package mmu

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
	"gitlab.com/akita/util/akitaext"
	"gitlab.com/akita/util/tracing"
)

type CaPWQPageWalker struct {
	transaction *Transaction
}

// CaPWQMMU is the default mmu implementation. It is also an akita Component.
type CaPWQMMU struct {
	akita.TickingComponent

	ToTop akita.Port
	L2TLB akita.Port

	ToPageWalkCache akita.Port
	PageWalkCache   akita.Port
	topSender       akitaext.BufferedSender

	TranslationPort akita.Port
	lowModuleFinder cache.LowModuleFinder

	ToCache              akita.Port
	CacheLowModuleFinder cache.LowModuleFinder

	pageTable *device.PageTableImpl

	pageWalkers []CaPWQPageWalker

	pageWalkQueue        []*Transaction
	maxPageWalkQueueSize int

	log2CacheLineSize uint64

	numInflightPTWRequests uint64
}

// Tick defines how the MMU update state each cycle
func (mmu *CaPWQMMU) Tick(now akita.VTimeInSec) bool {
	madeProgress := false

	madeProgress = mmu.topSender.Tick(now) || madeProgress
	madeProgress = mmu.walkPageTable(now) || madeProgress
	madeProgress = mmu.parseFromPageWalkCache(now) || madeProgress
	madeProgress = mmu.parseFromMem(now) || madeProgress
	madeProgress = mmu.parseFromTop(now) || madeProgress
	madeProgress = mmu.parseFromL1(now) || madeProgress

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

func (mmu *CaPWQMMU) walkPageTable(now akita.VTimeInSec) bool {
	madeProgress := false

	for i := range mmu.pageWalkers {
		if mmu.pageWalkers[i].transaction == nil {
			continue
		}

		transaction := mmu.pageWalkers[i].transaction

		switch transaction.state {
		case pageWalkCacheDone:
			mmu.sendWriteReqToL1(now, transaction)
		case sentWRToL1:
			mmu.sendToMem(now, i)
		case l1Done:
			mmu.sendWriteReqToL1(now, transaction)
		case transactionFinished:
			mmu.pageWalkers[i].transaction = nil
		}

		madeProgress = true
	}

	return madeProgress
}

func (mmu *CaPWQMMU) parseFromPageWalkCache(now akita.VTimeInSec) bool {
	item := mmu.ToPageWalkCache.Peek()
	if item == nil {
		return false
	}
	switch msg := item.(type) {
	case *mem.DataReadyRsp:
		return mmu.handlePageWalkCacheResponse(msg, now)
	case *mem.WriteDoneRsp:
		mmu.ToPageWalkCache.Retrieve(now)
		return true
	default:
		panic("unknown message type")
	}

	return false
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

	return false
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

	return false
}

func (mmu *CaPWQMMU) sendToMem(now akita.VTimeInSec, i int) {
	trans := mmu.pageWalkers[i].transaction

	if trans.state != sentWRToL1 {
		panic("this state shouldn't be here!")
	}

	PPN := trans.PPN
	PPNWithOffset := mmu.pageTable.AddOffset(PPN, trans.vAddr)

	srcPort := mmu.TranslationPort
	readReqInfo := &mem.ReadReqInfo{ReturnAccessInfo: true}
	dstPort := mmu.lowModuleFinder.Find(PPNWithOffset)

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

	mmu.pageWalkers[i].transaction = nil

	tracing.AddTaskStep(tracing.MsgIDAtReceiver(readReq, mmu),
		now, mmu, "page_walk_req_local")
}

func (mmu *CaPWQMMU) sendWriteReqToL1(now akita.VTimeInSec, trans *Transaction) {
	transState := trans.state
	if transState != pageWalkCacheDone && transState != l1Done {
		panic("this state shouldn't be here!")
	}

	if transState == l1Done {
		trans.vAddr = mmu.pageTable.MoveFromVAddrToLevel(
			trans.Address,
			trans.level,
		)
	}

	PPN := trans.PPN
	PPNWithOffset := mmu.pageTable.AddOffset(PPN, trans.vAddr)

	dstPort := mmu.CacheLowModuleFinder.Find(PPNWithOffset)

	block := vm.CaPWQBlock{
		PID:           trans.pid,
		Address:       trans.Address,
		PPNWithOffset: PPNWithOffset,
		Level:         trans.level,
		MsgID:         trans.msgID,
	}

	writeReq := mem.WriteReqBuilder{}.
		WithSendTime(now).
		WithSrc(mmu.ToCache).
		WithDst(dstPort).
		WithPID(trans.pid).
		WithAddress(PPNWithOffset).
		WithInfo(block).
		Build()

	writeReq.TrafficBytes += 12

	err := mmu.ToCache.Send(writeReq)
	if err != nil {
		return
	}

	trans.state = sentWRToL1

	tracing.AddTaskStep(tracing.MsgIDAtReceiver(writeReq, mmu),
		now, mmu, "page_walk_store_l1")
}

func (mmu *CaPWQMMU) handlePageWalkCacheResponse(
	rsp *mem.DataReadyRsp,
	now akita.VTimeInSec,
) bool {
	// Process the transaction in page walk queue first.
	if len(mmu.pageWalkQueue) > 0 {
		return false
	}

	for i := range mmu.pageWalkers {
		if mmu.pageWalkers[i].transaction != nil {
			continue
		}

		// Initialize a transaction
		transaction := &Transaction{
			Address: rsp.Info.(uint64),
			pid:     rsp.PID,
			msgID:   akita.GetIDGenerator().Generate(),
		}

		if rsp.Data != nil {
			rspData := binary.LittleEndian.Uint64(rsp.Data)

			level := int(rspData & uint64(3))

			transaction.PPN = rspData & ^uint64(3)
			transaction.vAddr = mmu.pageTable.MoveFromVAddrToLevel(
				rsp.Info.(uint64),
				level+1,
			)
			transaction.level = level + 1
		} else {
			transaction.PPN = mmu.pageTable.GetRoot(rsp.PID)
			transaction.vAddr = mmu.pageTable.Rearrange(rsp.Info.(uint64))
			transaction.level = 0
		}

		transaction.state = pageWalkCacheDone

		tracing.AddTaskStep(tracing.MsgIDAtReceiver(rsp, mmu),
			now, mmu, "pwc-hit-level"+strconv.Itoa(transaction.level))

		mmu.pageWalkers[i].transaction = transaction

		mmu.ToPageWalkCache.Retrieve(now)

		mmu.numInflightPTWRequests++

		tracing.StartTask(
			transaction.msgID,
			"",
			now,
			mmu,
			"req_in",
			"",
			nil,
		)

		return true
	}

	return false
}

func (mmu *CaPWQMMU) handleMemResponse(rsp *mem.DataReadyRsp, now akita.VTimeInSec) bool {
	if len(mmu.pageWalkQueue) >= mmu.maxPageWalkQueueSize {
		return false
	}

	for i := range mmu.pageWalkers {
		if mmu.pageWalkers[i].transaction != nil {
			continue
		}

		rspInfo := rsp.Info.(*mem.DataReadyRspInfo)

		PPN := binary.LittleEndian.Uint64(rsp.Data)
		if PPN == 0 {
			panic("invalid page walk result, PPN is 0")
		}

		dstPort := mmu.CacheLowModuleFinder.Find(rspInfo.Address)

		readReq := mem.ReadReqBuilder{}.
			WithSendTime(now).
			WithSrc(mmu.ToCache).
			WithDst(dstPort).
			WithPID(rsp.PID).
			WithAddress(rspInfo.Address).
			WithInfo(PPN).
			Build()

		readReq.TrafficBytes += 8

		err := mmu.ToCache.Send(readReq)
		if err != nil {
			return false
		}

		tempTransaction := Transaction{
			state: sentRDToL1,
			pid:   rsp.PID,
			PPN:   PPN,
		}

		mmu.pageWalkQueue = append(mmu.pageWalkQueue, &tempTransaction)

		mmu.TranslationPort.Retrieve(now)

		tracing.AddTaskStep(tracing.MsgIDAtReceiver(readReq, mmu),
			now, mmu, "page_walk_load_l1")

		return true
	}

	return false
}

func (mmu *CaPWQMMU) handleL1ReadResponse(rsp *mem.DataReadyRsp, now akita.VTimeInSec) bool {
	for i := range mmu.pageWalkers {
		if mmu.pageWalkers[i].transaction != nil {
			continue
		}

		block := rsp.Info.(vm.CaPWQBlock)

		for j, trans := range mmu.pageWalkQueue {
			if trans.state != sentRDToL1 || trans.PPN != block.PPN {
				continue
			}

			mmu.pageWalkers[i].transaction = trans

			trans.pid = block.PID
			trans.state = l1Done
			trans.level = block.Level
			trans.Address = block.Address
			trans.msgID = block.MsgID

			if trans.level+1 == 4 {
				mmu.finalizeTransaction(now, trans)
			} else {
				mmu.fillPageWalkCache(now, trans)
			}
			trans.level++

			mmu.ToCache.Retrieve(now)

			mmu.pageWalkQueue = append(
				mmu.pageWalkQueue[:j],
				mmu.pageWalkQueue[j+1:]...,
			)

			return true
		}
		panic("no match transactions!")
	}

	return false
}

func (mmu *CaPWQMMU) fillPageWalkCache(
	now akita.VTimeInSec,
	trans *Transaction,
) bool {
	if trans == nil {
		panic("no inflight transaction")
	}

	level := uint64(trans.level)
	data := uint64ToBytes(trans.PPN | level)

	writeReq := mem.WriteReqBuilder{}.
		WithSendTime(now).
		WithSrc(mmu.ToPageWalkCache).
		WithDst(mmu.PageWalkCache).
		WithPID(trans.pid).
		WithAddress(mmu.pageTable.AlignToPage(trans.Address) | level).
		WithData(data).
		Build()

	err := mmu.ToPageWalkCache.Send(writeReq)
	if err != nil {
		return false
	}

	return true
}

func (mmu *CaPWQMMU) finalizeTransaction(
	now akita.VTimeInSec,
	trans *Transaction,
) bool {
	page, found := mmu.pageTable.Find(trans.pid, trans.Address)
	if !found {
		panic("page not found")
	}

	pAddr := trans.PPN
	if pAddr != page.PAddr {
		panic("addresses don't match!")
	}

	newPage := device.Page{PID: trans.pid, VAddr: trans.Address, PAddr: pAddr, Valid: true}

	trans.page = newPage
	trans.state = transactionFinished

	return mmu.doPageWalkHit(now, trans)
}

func (mmu *CaPWQMMU) doPageWalkHit(
	now akita.VTimeInSec,
	trans *Transaction,
) bool {
	if !mmu.topSender.CanSend(1) {
		return false
	}

	rsp := device.TranslationRspBuilder{}.
		WithSendTime(now).
		WithSrc(mmu.ToTop).
		WithDst(mmu.L2TLB).
		WithPage(trans.page).
		Build()

	mmu.topSender.Send(rsp)

	tracing.EndTask(trans.msgID, now, mmu)

	mmu.numInflightPTWRequests--

	return true
}

func (mmu *CaPWQMMU) parseFromTop(now akita.VTimeInSec) bool {
	item := mmu.ToTop.Peek()
	if item == nil {
		return false
	}

	req, ok := item.(*device.TranslationReq)
	if !ok {
		panic(fmt.Sprintf("item isn't a translation request: %s", reflect.TypeOf(item)))
	}

	// Debugging check: address of readReq is the same as the one in the translation request.
	if mmu.pageTable.AlignToPage(req.VAddr) != req.VAddr {
		panic(fmt.Sprintf("unaligned address in translation request: %x", req.VAddr))
	}

	// Send to Page walk cache first.
	readReq := mem.ReadReqBuilder{}.
		WithSendTime(now).
		WithSrc(mmu.ToPageWalkCache).
		WithDst(mmu.PageWalkCache).
		WithPID(req.PID).
		WithAddress(mmu.pageTable.AlignToPage(req.VAddr)).
		WithByteSize(8).
		Build()

	err := mmu.ToPageWalkCache.Send(readReq)
	if err != nil {
		return false
	}

	mmu.ToTop.Retrieve(now)

	return true
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

func (mmu *CaPWQMMU) TranslationPortPort() akita.Port {
	return mmu.TranslationPort
}

func (mmu *CaPWQMMU) ToCachePort() akita.Port {
	return mmu.ToCache
}
