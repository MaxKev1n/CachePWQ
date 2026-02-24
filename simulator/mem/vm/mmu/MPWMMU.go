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
	"gitlab.com/akita/util/akitaext"
	"gitlab.com/akita/util/tracing"
)

type MPWWalkerStatus struct {
	state         transactionState
	requestVector map[int]struct{}
}

type MPWPageWalker struct {
	status *MPWWalkerStatus
	queue  []*Transaction
}

// MPWMMU is the default mmu implementation. It is also an akita Component.
type MPWMMU struct {
	akita.TickingComponent

	ToTop akita.Port

	ToPageWalkCache akita.Port
	PageWalkCache   akita.Port
	topSender       akitaext.BufferedSender

	TranslationPort   akita.Port
	translationSender akitaext.BufferedSender
	lowModuleFinder   cache.LowModuleFinder

	pageTable *device.PageTableImpl

	pageWalkers   []*MPWPageWalker
	nextPointer   int
	queueCapacity int
}

// Tick defines how the MMU update state each cycle
func (mmu *MPWMMU) Tick(now akita.VTimeInSec) bool {
	madeProgress := false

	madeProgress = mmu.topSender.Tick(now) || madeProgress
	madeProgress = mmu.translationSender.Tick(now) || madeProgress
	madeProgress = mmu.walkPageTable(now) || madeProgress
	madeProgress = mmu.parseFromPageWalkCache(now) || madeProgress
	madeProgress = mmu.parseFromMem(now) || madeProgress
	madeProgress = mmu.parseFromTop(now) || madeProgress

	return true
}

func (mmu *MPWMMU) trace(now akita.VTimeInSec, what string) {
	ctx := akita.HookCtx{
		Domain: mmu,
		Now:    now,
		Item:   what,
	}

	mmu.InvokeHook(ctx)
}

func (mmu *MPWMMU) walkPageTable(now akita.VTimeInSec) bool {
	madeProgress := false

	numInflightPTWRequests := 0
	pageWalkQueueLen := 0
	for _, walker := range mmu.pageWalkers {
		if len(walker.queue) == 0 {
			continue
		}

		status := walker.status

		switch status.state {
		case pageWalkCacheDone:
			mmu.sendToMem(now, walker)
		case memDone:
			mmu.sendToMem(now, walker)
		case transactionFinished:
			mmu.removeFromWalker(walker)
		}

		numInflightPTWRequests += len(walker.status.requestVector)
		pageWalkQueueLen += len(walker.queue)

		madeProgress = true
	}

	if mmu.isActive() {
		tracing.StartTask(
			"",
			"",
			now,
			mmu,
			"num_active_walkers",
			strconv.Itoa(numInflightPTWRequests),
			nil,
		)
		tracing.StartTask(
			"",
			"",
			now,
			mmu,
			"page_walk_queue_len",
			strconv.Itoa(pageWalkQueueLen),
			nil,
		)
	}

	return madeProgress
}

func (mmu *MPWMMU) parseFromPageWalkCache(now akita.VTimeInSec) bool {
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

	return false
}

func (mmu *MPWMMU) parseFromMem(now akita.VTimeInSec) bool {
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

func (mmu *MPWMMU) sendToMem(now akita.VTimeInSec, walker *MPWPageWalker) {
	transState := walker.status.state
	if transState != pageWalkCacheDone && transState != memDone {
		panic("this state shouldn't be here!")
	}

	if len(walker.status.requestVector) != 0 {
		panic("there are still requests in flight!")
	}

	var batchedTransactions []*Transaction

	batchedLevel := 0
	requestVector := make(map[int]struct{})
	// scan the queue
	for i := 0; i < len(walker.queue); i++ {
		if walker.queue[i] == nil {
			continue
		}

		if _, exist := requestVector[i]; exist {
			continue
		}

		trans := walker.queue[i]

		if len(batchedTransactions) == 0 {
			batchedLevel = trans.level
		}

		if trans.level != batchedLevel {
			continue
		}

		batchedTransactions = append(batchedTransactions, trans)

		requestVector[i] = struct{}{}
	}

	if mmu.translationSender.CanSend(len(batchedTransactions)) {
		for _, trans := range batchedTransactions {
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

			readReq.PTW = true

			mmu.translationSender.Send(readReq)

			trans.vAddr = mmu.pageTable.NextLevel(trans.vAddr)
			trans.msgID = readReq.ID
			trans.state = sentToMem

			l2SliceID, fail := getL2SliceNum(dstPort.Name())
			if fail != nil {
				log.Panicf("cannot get l2 slice num from port name %s", dstPort.Name())
			}

			if l2SliceID < 32 {
				tracing.AddTaskStep(tracing.MsgIDAtReceiver(trans.req, mmu),
					now, mmu, "page_walk_req_left")
			} else {
				tracing.AddTaskStep(tracing.MsgIDAtReceiver(trans.req, mmu),
					now, mmu, "page_walk_req_right")
			}

			tracing.AddTaskStep(tracing.MsgIDAtReceiver(trans.req, mmu),
				now, mmu, "page_walk_req_local")

			tracing.StartTask(
				readReq.ID,
				"",
				now,
				mmu,
				"walker_mem_latency",
				reflect.TypeOf(readReq).String(),
				readReq,
			)
		}

		walker.status.state = sentToMem
		walker.status.requestVector = requestVector
	}
}

func (mmu *MPWMMU) removeFromWalker(walker *MPWPageWalker) {
	tmp := walker.queue[:0]
	for _, trans := range walker.queue {
		if trans != nil && trans.state != transactionFinished {
			tmp = append(tmp, trans)
		}
	}

	walker.queue = tmp

	if len(walker.queue) == 0 {
		walker.status.state = newTransaction
	} else {
		walker.status.state = pageWalkCacheDone
	}
}

func (mmu *MPWMMU) handleMemResponse(rsp *mem.DataReadyRsp, now akita.VTimeInSec) {
	for _, walker := range mmu.pageWalkers {
		if len(walker.queue) == 0 {
			continue
		}

		for j := range walker.queue {
			if walker.queue[j] == nil {
				continue
			}

			if _, exist := walker.status.requestVector[j]; !exist {
				continue
			}

			trans := walker.queue[j]

			if trans.msgID != rsp.RespondTo {
				continue
			}

			trans.PPN = binary.LittleEndian.Uint64(rsp.Data)
			trans.state = memDone
			if trans.level+1 == 4 {
				mmu.finalizeTransaction(now, trans)
			} else {
				mmu.fillPageWalkCache(now, trans)
			}
			trans.level++

			tracing.EndTask(rsp.RespondTo, now, mmu)

			delete(walker.status.requestVector, j)

			if len(walker.status.requestVector) == 0 {
				if trans.state == transactionFinished {
					walker.status.state = transactionFinished
				} else {
					walker.status.state = memDone
				}
			}

			return
		}
	}
	panic("cannot handle mem response")
}

func (mmu *MPWMMU) fillPageWalkCache(
	now akita.VTimeInSec,
	trans *Transaction,
) bool {
	level := uint64(trans.level)
	data := uint64ToBytes(trans.PPN | level)
	writeReq := mem.WriteReqBuilder{}.
		WithSendTime(now).
		WithSrc(mmu.ToPageWalkCache).
		WithDst(mmu.PageWalkCache).
		WithPID(trans.req.PID).
		WithAddress(mmu.pageTable.AlignToPage(trans.req.VAddr) | level).
		WithData(data).
		Build()

	err := mmu.ToPageWalkCache.Send(writeReq)
	if err != nil {
		return false
	}

	trans.msgID = writeReq.ID

	return true
}

func (mmu *MPWMMU) finalizeTransaction(
	now akita.VTimeInSec,
	trans *Transaction,
) bool {
	req := trans.req

	page, found := mmu.pageTable.Find(req.PID, req.VAddr)
	if !found {
		panic("page not found")
	}

	pAddr := trans.PPN
	if pAddr != page.PAddr {
		panic("addresses don't match!")
	}

	newPage := device.Page{PID: req.PID, VAddr: req.VAddr, PAddr: pAddr, Valid: true}
	trans.page = newPage
	trans.state = transactionFinished

	return mmu.doPageWalkHit(now, trans)
}

func (mmu *MPWMMU) doPageWalkHit(
	now akita.VTimeInSec,
	trans *Transaction,
) bool {
	if !mmu.topSender.CanSend(1) {
		return false
	}

	rsp := device.TranslationRspBuilder{}.
		WithSendTime(now).
		WithSrc(mmu.ToTop).
		WithDst(trans.req.Src).
		WithRspTo(trans.req.ID).
		WithPage(trans.page).
		Build()

	mmu.topSender.Send(rsp)

	tracing.TraceReqComplete(trans.req, now, mmu)

	return true
}

func (mmu *MPWMMU) parseFromTop(now akita.VTimeInSec) bool {
	item := mmu.ToTop.Peek()
	if item == nil {
		return false
	}

	req, ok := item.(*device.TranslationReq)
	if !ok {
		log.Panicf("MMU cannot handle request of type %s", reflect.TypeOf(req))
	}

	for i := 0; i < len(mmu.pageWalkers); i++ {
		index := (mmu.nextPointer + i) % len(mmu.pageWalkers)
		if len(mmu.pageWalkers[index].queue) < mmu.queueCapacity {
			rearrangedVAddr := mmu.pageTable.Rearrange(req.VAddr)
			root := mmu.pageTable.GetRoot(req.PID)
			translationInPipeline := Transaction{
				req:   req,
				level: 0,
				msgID: "invalid",
				state: pageWalkCacheDone,
				vAddr: rearrangedVAddr,
				PPN:   root,
			}

			if req.Data != nil {
				rspData := binary.LittleEndian.Uint64(req.Data)
				translationInPipeline.PPN = rspData & ^uint64(3)
				level := int(rspData & uint64(3))
				translationInPipeline.vAddr = mmu.pageTable.MoveToLevel(
					translationInPipeline.vAddr,
					level+1,
				)
				translationInPipeline.level = level + 1
			}

			tracing.AddTaskStep(tracing.MsgIDAtReceiver(translationInPipeline.req, mmu),
				now, mmu, "pwc-hit-level"+strconv.Itoa(translationInPipeline.level))

			mmu.pageWalkers[index].queue = append(
				mmu.pageWalkers[index].queue,
				&translationInPipeline,
			)
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

			if mmu.pageWalkers[index].status.state == newTransaction {
				mmu.pageWalkers[index].status.state = pageWalkCacheDone
			}
			return true
		}
	}

	return false
}

// SetLowModuleFinder sets the table recording where to find an address.
func (mmu *MPWMMU) SetLowModuleFinder(lmf cache.LowModuleFinder) {
	mmu.lowModuleFinder = lmf
}

func (mmu *MPWMMU) GetNumActiveWalkers() int {
	num := 0
	for i := range mmu.pageWalkers {
		num += len(mmu.pageWalkers[i].status.requestVector)
	}
	return num
}

func (mmu *MPWMMU) ToTopPort() akita.Port {
	return mmu.ToTop
}

func (mmu *MPWMMU) TranslationPortPort() akita.Port {
	return mmu.TranslationPort
}

func (mmu *MPWMMU) ToCachePort() akita.Port {
	panic("Baseline MMU does not support ToCachePort")
}

func (mmu *MPWMMU) CanAccept() bool {
	for i := range mmu.pageWalkers {
		if len(mmu.pageWalkers[i].queue) < mmu.queueCapacity {
			return true
		}
	}

	return false
}

func (mmu *MPWMMU) ToPageWalkCachePort() akita.Port {
	return mmu.ToPageWalkCache
}

func (mmu *MPWMMU) isActive() bool {
	for i := range mmu.pageWalkers {
		if len(mmu.pageWalkers[i].queue) > 0 {
			return true
		}
	}

	return false
}
