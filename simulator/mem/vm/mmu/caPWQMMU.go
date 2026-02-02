package mmu

import (
	"encoding/binary"
	"log"
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
	queue         []*device.TranslationReq
	inflightTrans *Transaction

	waitingTrans *Transaction
}

// CaPWQMMU is the default mmu implementation. It is also an akita Component.
type CaPWQMMU struct {
	akita.TickingComponent

	ToTop akita.Port

	pageWalkCachePort akita.Port
	PageWalkCache     akita.Port
	topSender         akitaext.BufferedSender

	TranslationPort akita.Port
	lowModuleFinder cache.LowModuleFinder

	ToCache              akita.Port
	CacheLowModuleFinder cache.LowModuleFinder

	pageTable *device.PageTableImpl

	pageWalkers   []CaPWQPageWalker
	nextPointer   int
	queueCapacity int

	log2CacheLineSize uint64

	inflightMemRequets map[string]string // For Debugging
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
		if mmu.pageWalkers[i].inflightTrans == nil {
			if mmu.pageWalkers[i].waitingTrans == nil {
				continue
			}

			mmu.pageWalkers[i].inflightTrans = mmu.pageWalkers[i].waitingTrans
			mmu.pageWalkers[i].waitingTrans = nil
		}
		inflightTrans := mmu.pageWalkers[i].inflightTrans

		switch inflightTrans.state {
		case newTransaction:
			mmu.sendToPageWalkCache(now, inflightTrans)
		case pageWalkCacheDone:
			mmu.sendWriteReqToL1(now, inflightTrans)
		case sentWRToL1:
			mmu.sendToMem(now, i)
		case memDone:
			mmu.sendReadReqToL1(now, inflightTrans)
		case l1Done:
			mmu.sendWriteReqToL1(now, inflightTrans)
		case transactionFinished:
			mmu.pageWalkers[i].inflightTrans = nil
		}

		madeProgress = true
	}

	return madeProgress
}

func (mmu *CaPWQMMU) parseFromPageWalkCache(now akita.VTimeInSec) bool {
	madeProgress := false
	item := mmu.pageWalkCachePort.Peek()
	if item != nil {
		switch msg := item.(type) {
		case *mem.DataReadyRsp:
			mmu.handlePageWalkCacheResponse(msg, now)
		case *mem.WriteDoneRsp:
		default:
			panic("unknown message type")
		}
		madeProgress = true
	}
	mmu.pageWalkCachePort.Retrieve(now)
	return madeProgress
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

func (mmu *CaPWQMMU) sendToPageWalkCache(now akita.VTimeInSec, trans *Transaction) {
	transState := trans.state
	if transState != newTransaction {
		panic("this state shouldn't be here!")
	}

	readReq := mem.ReadReqBuilder{}.
		WithSendTime(now).
		WithSrc(mmu.pageWalkCachePort).
		WithDst(mmu.PageWalkCache).
		WithPID(trans.req.PID).
		WithAddress(mmu.pageTable.AlignToPage(trans.req.VAddr)).
		WithByteSize(8).
		Build()

	trans.msgID = readReq.ID
	mmu.pageWalkCachePort.Send(readReq)
	trans.state = sentToPageWalkCache
}

func (mmu *CaPWQMMU) sendToMem(now akita.VTimeInSec, i int) {
	trans := mmu.pageWalkers[i].inflightTrans

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
		WithPID(trans.req.PID).
		WithAddress(PPNWithOffset).
		WithByteSize(8).
		WithInfo(readReqInfo).
		Build()

	err := srcPort.Send(readReq)
	if err != nil {
		return
	}

	if _, exists := mmu.inflightMemRequets[readReq.ID]; exists {
		log.Panicf("Duplicate inflight translation memory request ID: %s", readReq.ID)
	} else {
		mmu.inflightMemRequets[readReq.ID] = trans.req.ID
	}

	mmu.pageWalkers[i].inflightTrans = nil

	tracing.AddTaskStep(tracing.MsgIDAtReceiver(trans.req, mmu),
		now, mmu, "page_walk_req_local")
}

func (mmu *CaPWQMMU) sendWriteReqToL1(now akita.VTimeInSec, trans *Transaction) {
	transState := trans.state
	if transState != pageWalkCacheDone && transState != l1Done {
		panic("this state shouldn't be here!")
	}

	PPN := trans.PPN
	PPNWithOffset := mmu.pageTable.AddOffset(PPN, trans.vAddr)

	dstPort := mmu.CacheLowModuleFinder.Find(PPNWithOffset)

	block := vm.CaPWQBlock{
		Req:           trans.req,
		VAddr:         mmu.pageTable.NextLevel(trans.vAddr),
		PID:           trans.req.PID,
		VPN:           mmu.pageTable.AlignToPage(trans.req.VAddr),
		PPNWithOffset: PPNWithOffset,
		Level:         trans.level,
	}

	writeReq := mem.WriteReqBuilder{}.
		WithSendTime(now).
		WithSrc(mmu.ToCache).
		WithDst(dstPort).
		WithPID(trans.req.PID).
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

func (mmu *CaPWQMMU) sendReadReqToL1(now akita.VTimeInSec, trans *Transaction) {
	transState := trans.state
	if transState != memDone {
		panic("this state shouldn't be here!")
	}

	dstPort := mmu.CacheLowModuleFinder.Find(trans.LastPPNWithOffset)

	translationReqID, exists := mmu.inflightMemRequets[trans.msgID]
	if !exists {
		log.Panicf("Cannot find matching translation request ID for memory response ID: %s", trans.msgID)
	}

	readReq := mem.ReadReqBuilder{}.
		WithSendTime(now).
		WithSrc(mmu.ToCache).
		WithDst(dstPort).
		WithPID(trans.pid).
		WithAddress(trans.LastPPNWithOffset).
		WithInfo(translationReqID).
		Build()

	readReq.TrafficBytes += 12

	err := mmu.ToCache.Send(readReq)
	if err != nil {
		return
	}

	delete(mmu.inflightMemRequets, trans.msgID)

	trans.msgID = readReq.ID
	trans.state = sentRDToL1

	tracing.AddTaskStep(tracing.MsgIDAtReceiver(readReq, mmu),
		now, mmu, "page_walk_load_l1")
}

func (mmu *CaPWQMMU) handlePageWalkCacheResponse(rsp *mem.DataReadyRsp, now akita.VTimeInSec) {
	for i := range mmu.pageWalkers {
		if mmu.pageWalkers[i].inflightTrans == nil {
			continue
		}

		trans := mmu.pageWalkers[i].inflightTrans
		if trans.msgID == rsp.RespondTo {
			if rsp.Data != nil {
				rspData := binary.LittleEndian.Uint64(rsp.Data)
				trans.PPN = rspData & ^uint64(3)
				level := int(rspData & uint64(3))
				trans.vAddr = mmu.pageTable.MoveToLevel(trans.vAddr, level+1)
				trans.level = level + 1
			}

			trans.state = pageWalkCacheDone

			tracing.AddTaskStep(tracing.MsgIDAtReceiver(trans.req, mmu),
				now, mmu, "pwc-hit-level"+strconv.Itoa(trans.level))
		}
	}
}

func (mmu *CaPWQMMU) handleMemResponse(rsp *mem.DataReadyRsp, now akita.VTimeInSec) bool {
	for i := range mmu.pageWalkers {
		if mmu.pageWalkers[i].inflightTrans != nil && mmu.pageWalkers[i].waitingTrans != nil {
			continue
		}
		rspInfo := rsp.Info.(*mem.DataReadyRspInfo)

		tempTransaction := Transaction{
			pid:               rsp.PID,
			state:             memDone,
			PPN:               binary.LittleEndian.Uint64(rsp.Data),
			LastPPNWithOffset: rspInfo.Address,
			msgID:             rsp.RespondTo,
		}

		if mmu.pageWalkers[i].inflightTrans == nil {
			mmu.pageWalkers[i].inflightTrans = &tempTransaction
		} else {
			mmu.pageWalkers[i].waitingTrans = &tempTransaction
		}

		mmu.TranslationPort.Retrieve(now)

		return true
	}

	return false
}

func (mmu *CaPWQMMU) handleL1ReadResponse(rsp *mem.DataReadyRsp, now akita.VTimeInSec) bool {
	for i := range mmu.pageWalkers {
		if mmu.pageWalkers[i].inflightTrans == nil {
			continue
		}

		trans := mmu.pageWalkers[i].inflightTrans
		if trans.msgID == rsp.RespondTo {
			block := rsp.Info.(vm.CaPWQBlock)

			trans.pid = block.PID
			trans.req = block.Req
			trans.level = block.Level
			trans.state = l1Done
			trans.vAddr = block.VAddr

			if trans.level+1 == 4 {
				mmu.finalizeTransaction(now, i)
			} else {
				mmu.fillPageWalkCache(now, i)
			}
			trans.level++

			mmu.ToCache.Retrieve(now)

			return true
		}
	}
	panic("no match transactions!")
}

func (mmu *CaPWQMMU) fillPageWalkCache(
	now akita.VTimeInSec,
	i int,
) bool {
	trans := mmu.pageWalkers[i].inflightTrans

	if trans == nil {
		panic("no inflight transaction")
	}

	level := uint64(trans.level)
	data := uint64ToBytes(trans.PPN | level)

	writeReq := mem.WriteReqBuilder{}.
		WithSendTime(now).
		WithSrc(mmu.pageWalkCachePort).
		WithDst(mmu.PageWalkCache).
		WithPID(trans.req.PID).
		WithAddress(mmu.pageTable.AlignToPage(trans.req.VAddr) | level).
		WithData(data).
		Build()

	err := mmu.pageWalkCachePort.Send(writeReq)
	if err != nil {
		return false
	}

	trans.msgID = writeReq.ID

	return true
}

func (mmu *CaPWQMMU) finalizeTransaction(
	now akita.VTimeInSec,
	walkingIndex int,
) bool {
	req := mmu.pageWalkers[walkingIndex].inflightTrans.req

	page, found := mmu.pageTable.Find(req.PID, req.VAddr)
	if !found {
		panic("page not found")
	}

	pAddr := mmu.pageWalkers[walkingIndex].inflightTrans.PPN
	if pAddr != page.PAddr {
		panic("addresses don't match!")
	}

	newPage := device.Page{PID: req.PID, VAddr: req.VAddr, PAddr: pAddr, Valid: true}

	mmu.pageWalkers[walkingIndex].inflightTrans.page = newPage
	mmu.pageWalkers[walkingIndex].inflightTrans.state = transactionFinished

	return mmu.doPageWalkHit(now, walkingIndex)
}

func (mmu *CaPWQMMU) doPageWalkHit(
	now akita.VTimeInSec,
	walkingIndex int,
) bool {
	if !mmu.topSender.CanSend(1) {
		return false
	}

	walking := mmu.pageWalkers[walkingIndex].inflightTrans

	rsp := device.TranslationRspBuilder{}.
		WithSendTime(now).
		WithSrc(mmu.ToTop).
		WithDst(walking.req.Src).
		WithRspTo(walking.req.ID).
		WithPage(walking.page).
		Build()

	mmu.topSender.Send(rsp)

	tracing.TraceReqComplete(walking.req, now, mmu)

	return true
}

func (mmu *CaPWQMMU) parseFromTop(now akita.VTimeInSec) bool {
	madeProgress := false

	item := mmu.ToTop.Peek()

	if item != nil {
		req, ok := item.(*device.TranslationReq)
		if !ok {
			log.Panicf("MMU canot handle request of type %s", reflect.TypeOf(req))
		}

		for i := 0; i < len(mmu.pageWalkers); i++ {
			index := (mmu.nextPointer + i) % len(mmu.pageWalkers)
			if len(mmu.pageWalkers[index].queue) < mmu.queueCapacity {
				mmu.pageWalkers[index].queue = append(mmu.pageWalkers[index].queue, req)
				mmu.nextPointer = (index + 1) % len(mmu.pageWalkers)

				mmu.ToTop.Retrieve(now)

				tracing.StartTask(
					tracing.MsgIDAtReceiver(req, mmu),
					req.Meta().ID,
					now,
					mmu,
					"req",
					reflect.TypeOf(req).String(),
					req,
				)

				tracing.TraceReqReceive(req, now, mmu)

				madeProgress = true

				break
			}
		}
	}

	for i := range mmu.pageWalkers {
		walker := &mmu.pageWalkers[i]

		if walker.inflightTrans == nil && len(walker.queue) > 0 {
			req := walker.queue[0]

			rearrangedVAddr := mmu.pageTable.Rearrange(req.VAddr)
			root := mmu.pageTable.GetRoot(req.PID)
			translationInPipeline := Transaction{
				req:   req,
				level: 0,
				msgID: "invalid",
				state: newTransaction,
				vAddr: rearrangedVAddr,
				PPN:   root,
				pid:   req.PID,
			}

			walker.inflightTrans = &translationInPipeline

			walker.queue = walker.queue[1:]

			madeProgress = true
		}
	}

	return madeProgress
}

// SetLowModuleFinder sets the table recording where to find an address.
func (mmu *CaPWQMMU) SetLowModuleFinder(lmf cache.LowModuleFinder) {
	mmu.lowModuleFinder = lmf
}

func (mmu *CaPWQMMU) GetNumActiveWalkers() int {
	num := 0
	for i := range mmu.pageWalkers {
		if mmu.pageWalkers[i].inflightTrans != nil {
			num++
		}
	}
	return num
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
