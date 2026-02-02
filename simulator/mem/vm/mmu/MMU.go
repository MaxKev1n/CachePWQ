package mmu

import (
	"encoding/binary"
	"fmt"
	"log"
	"reflect"
	"strconv"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/mem/cache"
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/util/akitaext"
	"gitlab.com/akita/util/tracing"
)

type PageWalkerImpl struct {
	queue         []*device.TranslationReq
	inflightTrans *Transaction
}

// MMUImpl is the default mmu implementation. It is also an akita Component.
type MMUImpl struct {
	akita.TickingComponent

	ToTop akita.Port

	pageWalkCachePort akita.Port
	PageWalkCache     akita.Port
	topSender         akitaext.BufferedSender

	TranslationPort akita.Port
	lowModuleFinder cache.LowModuleFinder

	pageTable *device.PageTableImpl

	pageWalkers   []PageWalkerImpl
	nextPointer   int
	queueCapacity int
}

// Tick defines how the MMU update state each cycle
func (mmu *MMUImpl) Tick(now akita.VTimeInSec) bool {
	madeProgress := false

	madeProgress = mmu.topSender.Tick(now) || madeProgress
	madeProgress = mmu.walkPageTable(now) || madeProgress
	madeProgress = mmu.parseFromPageWalkCache(now) || madeProgress
	madeProgress = mmu.parseFromMem(now) || madeProgress
	madeProgress = mmu.parseFromTop(now) || madeProgress

	return true
}

func (mmu *MMUImpl) trace(now akita.VTimeInSec, what string) {
	ctx := akita.HookCtx{
		Domain: mmu,
		Now:    now,
		Item:   what,
	}

	mmu.InvokeHook(ctx)
}

func (mmu *MMUImpl) walkPageTable(now akita.VTimeInSec) bool {
	madeProgress := false

	for i := range mmu.pageWalkers {
		inflightTrans := mmu.pageWalkers[i].inflightTrans
		if inflightTrans == nil {
			continue
		}

		switch inflightTrans.state {
		case newTransaction:
			mmu.sendToPageWalkCache(now, inflightTrans)
		case pageWalkCacheDone:
			mmu.sendToMem(now, inflightTrans)
		case memDone:
			mmu.sendToMem(now, inflightTrans)
		case transactionFinished:
			mmu.pageWalkers[i].inflightTrans = nil
		}

		madeProgress = true
	}

	return madeProgress
}

func (mmu *MMUImpl) parseFromPageWalkCache(now akita.VTimeInSec) bool {
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

func (mmu *MMUImpl) parseFromMem(now akita.VTimeInSec) bool {
	madeProgress := false
	item := mmu.TranslationPort.Peek()
	if item != nil {
		switch msg := item.(type) {
		case *mem.DataReadyRsp:
			mmu.handleMemResponse(msg, now)
		default:
			panic("unknown message type")
		}
		madeProgress = true
	}
	mmu.TranslationPort.Retrieve(now)
	return madeProgress
}

func (mmu *MMUImpl) sendToPageWalkCache(now akita.VTimeInSec, trans *Transaction) {
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

func (mmu *MMUImpl) sendToMem(now akita.VTimeInSec, trans *Transaction) {
	transState := trans.state
	if transState != pageWalkCacheDone && transState != memDone {
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

	trans.vAddr = mmu.pageTable.NextLevel(trans.vAddr)
	trans.msgID = readReq.ID
	trans.state = sentToMem

	tracing.AddTaskStep(tracing.MsgIDAtReceiver(trans.req, mmu),
		now, mmu, "page_walk_req_local")
}

func (mmu *MMUImpl) handlePageWalkCacheResponse(rsp *mem.DataReadyRsp, now akita.VTimeInSec) {
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

func (mmu *MMUImpl) handleMemResponse(rsp *mem.DataReadyRsp, now akita.VTimeInSec) {
	for i := range mmu.pageWalkers {
		if mmu.pageWalkers[i].inflightTrans == nil {
			continue
		}

		trans := mmu.pageWalkers[i].inflightTrans
		if trans.msgID == rsp.RespondTo {
			rspInfo := rsp.Info.(*mem.DataReadyRspInfo)
			accessResult := rspInfo.AccessResult
			src := rspInfo.Src
			taskStep := fmt.Sprintf("chiplet-%s-level-%d-%s", getChipletNum(src), trans.level, getAccessResultString(accessResult))

			tracing.AddTaskStep(
				trans.msgID+"MMU-mem-latency",
				now, mmu,
				taskStep,
			)
			tracing.EndTask(
				trans.msgID+"MMU-mem-latency",
				now,
				mmu,
			)

			trans.PPN = binary.LittleEndian.Uint64(rsp.Data)
			trans.state = memDone
			if trans.level+1 == 4 {
				mmu.finalizeTransaction(now, i)
			} else {
				mmu.fillPageWalkCache(now, i)
			}
			trans.level++
		}
	}
}

func (mmu *MMUImpl) fillPageWalkCache(
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

func (mmu *MMUImpl) finalizeTransaction(
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

func (mmu *MMUImpl) doPageWalkHit(
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

func (mmu *MMUImpl) parseFromTop(now akita.VTimeInSec) bool {
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
			}

			walker.inflightTrans = &translationInPipeline

			walker.queue = walker.queue[1:]

			madeProgress = true
		}
	}

	return madeProgress
}

// SetLowModuleFinder sets the table recording where to find an address.
func (mmu *MMUImpl) SetLowModuleFinder(lmf cache.LowModuleFinder) {
	mmu.lowModuleFinder = lmf
}

func (mmu *MMUImpl) GetNumActiveWalkers() int {
	num := 0
	for i := range mmu.pageWalkers {
		if mmu.pageWalkers[i].inflightTrans != nil {
			num++
		}
	}
	return num
}

func (mmu *MMUImpl) ToTopPort() akita.Port {
	return mmu.ToTop
}

func (mmu *MMUImpl) TranslationPortPort() akita.Port {
	return mmu.TranslationPort
}

func (mmu *MMUImpl) ToCachePort() akita.Port {
	panic("Baseline MMU does not support ToCachePort")
}
