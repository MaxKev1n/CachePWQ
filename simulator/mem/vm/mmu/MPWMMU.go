package mmu

import (
	"encoding/binary"
	"fmt"
	"log"
	"reflect"
	"strconv"

	"strings"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/mem/cache"
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/util/akitaext"
	"gitlab.com/akita/util/tracing"
)

type MPWPageWalker struct {
	mmu *MPWMMU

	status        int
	requestVector []bool

	queue           []*transaction
	outstandingReqs map[string]*transaction
}

// MPWMMU is the default mmu implementation. It is also an akita Component.
type MPWMMU struct {
	akita.TickingComponent

	ToTop            akita.Port
	ControlPort      akita.Port
	CommandProcessor akita.Port

	pageWalkCachePort akita.Port
	PageWalkCache     akita.Port
	topSender         akitaext.BufferedSender

	TranslationPort akita.Port
	lowModuleFinder cache.LowModuleFinder
	numChiplets     uint64

	pageTable *device.PageTableImpl
	//	latency             int

	pageWalkers   []*MPWPageWalker
	nextPointer   int
	queueCapacity int

	remoteMemAccessesInCurEpoch  uint64
	avgWalksEnqueuedInCurEpoch   float64
	remoteMemAccessesInPrevEpoch uint64
	avgWalksEnqueuedInPrevEpoch  float64
	memAccessesInCurEpoch        uint64
	memAccessesInPrevEpoch       uint64

	numWalksDone        uint64
	numWalksInCurEpoch  uint64
	numWalksInPrevEpoch uint64
	lastChecked         uint64
	sendStateInfo       bool
	// numWalksArrived          uint64
	interleaving uint64

	inflightPWCRequests map[string]*transaction
	inflightMemRequests []*mem.ReadReq

	mappingMemAccess map[string]*transaction
}

// Tick defines how the MMU update state each cycle
func (mmu *MPWMMU) Tick(now akita.VTimeInSec) bool {
	madeProgress := false

	madeProgress = mmu.performCtrlReq(now) || madeProgress

	if mmu.GetNumActiveWalkers() > 0 {
		tracing.StartTask("", "", now, mmu, "imbalance", "", nil)
	}

	madeProgress = mmu.topSender.Tick(now) || madeProgress
	madeProgress = mmu.walkPageTable(now) || madeProgress
	madeProgress = mmu.sendToMem(now) || madeProgress
	madeProgress = mmu.sendToPageWalkCache(now) || madeProgress
	madeProgress = mmu.parseFromPageWalkCache(now) || madeProgress
	madeProgress = mmu.parseFromMem(now) || madeProgress
	madeProgress = mmu.parseFromTop(now) || madeProgress
	return madeProgress
}

func (mmu *MPWMMU) performCtrlReq(now akita.VTimeInSec) bool {
	item := mmu.ControlPort.Peek()
	if item == nil {
		return false
	}
	madeProgress := false
	switch req := item.(type) {
	case *akita.TLBIndexingSwitchMsg:
		madeProgress = mmu.switchIndexing(now, req)
	case *akita.SendStatsMsg:
		madeProgress = mmu.sendCollectedStatsToCP(now)
	default:
		log.Panicf("cannot process request %s", reflect.TypeOf(req))
	}
	if madeProgress {
		mmu.ControlPort.Retrieve(now)
		return true
	}
	panic("something is wrong!")
}

func (mmu *MPWMMU) sendCollectedStatsToCP(now akita.VTimeInSec) bool {
	req := akita.CollectedStatsMsgBuilder{}.
		WithSendTime(now).
		WithSrc(mmu.ControlPort).
		WithDst(mmu.CommandProcessor).
		Build()
	err := mmu.ControlPort.Send(req)
	if err != nil {
		panic("could not send stats to CP!")
	}
	mmu.ControlPort.Retrieve(now)
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
	numActiveTransactions := len(mmu.mappingMemAccess)

	for _, walker := range mmu.pageWalkers {
		numWalksDone := walker.walkPageTable(now)

		if numWalksDone > 0 {
			mmu.numWalksDone += uint64(numWalksDone)
			if mmu.numWalksDone == 1200 && !mmu.sendStateInfo && mmu.interleaving == 12 {
				mmu.numWalksDone = 0
				mmu.lastChecked = 0
				mmu.sendStateInfo = true
			}
			mmu.avgWalksEnqueuedInCurEpoch =
				(mmu.avgWalksEnqueuedInCurEpoch*float64(mmu.numWalksInCurEpoch) +
					float64(len(mmu.ToTop.(akita.MsgBufferContainer).GetBuffer()))) /
					float64(mmu.numWalksInCurEpoch+1)

			mmu.numWalksInCurEpoch += uint64(numWalksDone)
		}
	}

	tracing.StartTask(
		"",
		"",
		now,
		mmu,
		"num_active_walkers",
		fmt.Sprintf("%d", numActiveTransactions),
		nil,
	)

	numTranslations := 0

	for _, walker := range mmu.pageWalkers {
		numTranslations += len(walker.queue)
	}

	return numTranslations > 0
}

func (walker *MPWPageWalker) walkPageTable(now akita.VTimeInSec) int {
	if walker.status != 0 {
		return 0
	}

	if len(walker.queue) == 0 {
		return 0
	}

	trans := walker.queue[0]

	if trans.state == pageWalkCacheDone || trans.state == memDone {
		walker.sendToMem()
	}

	tmp := walker.queue[:0]

	numWalksDone := 0

	for _, item := range walker.queue {
		if item.state != transactionFinished {
			tmp = append(tmp, item)
		} else {
			numWalksDone++
		}
	}
	walker.queue = tmp

	return numWalksDone
}

func (walker *MPWPageWalker) sendToMem() {
	if walker.status != 0 {
		panic("page walker is busy!")
	}

	if walker.queue[0].state != pageWalkCacheDone && walker.queue[0].state != memDone {
		panic("first transaction is not ready to send to memory!")
	}

	pendingMemAccesses := make([]*mem.ReadReq, 0)
	pendingMemAccesses = append(
		pendingMemAccesses,
		walker.generateMemReq(walker.queue[0]),
	)

	pendingTranslationIndices := make([]int, 0)
	pendingTranslationIndices = append(
		pendingTranslationIndices,
		0,
	)

	for i := 1; i < len(walker.queue); i++ {
		trans := walker.queue[i]
		transState := trans.state
		if transState != pageWalkCacheDone && transState != memDone {
			continue
		}

		if trans.level != walker.queue[0].level {
			continue
		}

		pendingMemAccesses = append(
			pendingMemAccesses,
			walker.generateMemReq(trans),
		)

		pendingTranslationIndices = append(
			pendingTranslationIndices,
			i,
		)
	}

	for i, req := range pendingMemAccesses {
		if _, ok := walker.mmu.mappingMemAccess[req.ID]; ok {
			panic("duplicate mem access ID detected!")
		}

		walker.mmu.mappingMemAccess[req.ID] = walker.queue[pendingTranslationIndices[i]]
		walker.mmu.inflightMemRequests = append(
			walker.mmu.inflightMemRequests,
			req,
		)
		walker.outstandingReqs[req.ID] = walker.queue[pendingTranslationIndices[i]]
	}

	walker.status = 1

	for _, idx := range pendingTranslationIndices {
		walker.queue[idx].state = sentToMem
		walker.requestVector[idx] = true
	}
}

func (walker *MPWPageWalker) generateMemReq(
	trans *transaction,
) *mem.ReadReq {
	PPN := trans.PPN
	PPNWithOffset := walker.mmu.pageTable.AddOffset(PPN, trans.vAddr)

	trans.vAddr = walker.mmu.pageTable.NextLevel(trans.vAddr)

	srcPort := walker.mmu.TranslationPort
	readReqInfo := &mem.ReadReqInfo{ReturnAccessInfo: true}
	dstPort := walker.mmu.lowModuleFinder.Find(PPNWithOffset)

	readReq := mem.ReadReqBuilder{}.
		WithSrc(srcPort).
		WithDst(dstPort).
		WithPID(trans.req.PID).
		WithAddress(PPNWithOffset).
		WithByteSize(8).
		WithInfo(readReqInfo).
		Build()

	readReq.Meta().PTW = true

	return readReq
}

func (mmu *MPWMMU) sendToMem(now akita.VTimeInSec) bool {
	madeProgress := false

	for i := 0; i < len(mmu.inflightMemRequests); {
		req := mmu.inflightMemRequests[i]
		req.SendTime = now

		err := mmu.TranslationPort.Send(req)
		if err != nil {
			break
		}

		tracing.StartTask(
			req.Meta().ID+"MMU-mem-latency",
			tracing.MsgIDAtReceiver(req, mmu),
			now,
			mmu,
			"MMU_mem_latency",
			reflect.TypeOf(req).String(),
			req,
		)

		dstsplits := strings.Split(req.Dst.Name(), ".")
		dstType := dstsplits[2]

		trans, ok := mmu.mappingMemAccess[req.ID]
		if !ok {
			log.Panic("could not find matching mem access ID!")
		}

		if dstType == "ChipRDMA" {
			mmu.remoteMemAccessesInCurEpoch++
			tracing.AddTaskStep(tracing.MsgIDAtReceiver(trans.req, mmu),
				now, mmu, "page_walk_req_remote")
			if trans.level == 3 {
				tracing.AddTaskStep(tracing.MsgIDAtReceiver(trans.req, mmu),
					now, mmu, "pw-level-3-remote-reqs")
			}
		} else {
			tracing.AddTaskStep(tracing.MsgIDAtReceiver(trans.req, mmu),
				now, mmu, "page_walk_req_local")
		}
		mmu.memAccessesInCurEpoch++

		trans.msgID = req.ID

		// remove from pendingIssueToMem
		mmu.inflightMemRequests = append(
			mmu.inflightMemRequests[:i],
			mmu.inflightMemRequests[i+1:]...,
		)

		madeProgress = true
	}

	return madeProgress
}

func (mmu *MPWMMU) sendMsgToCP(now akita.VTimeInSec) bool {
	if !mmu.sendStateInfo {
		panic("how are we sending a message to CP when we shouldn't be?!")
	}
	req := akita.SendStatsMsgBuilder{}.
		WithSendTime(now).
		WithSrc(mmu.ControlPort).
		WithDst(mmu.CommandProcessor).
		Build()

	err := mmu.ControlPort.Send(req)
	if err != nil {
		fmt.Println(err)
		// panic("oh no")
	}
	return true
}

func (mmu *MPWMMU) switchIndexing(now akita.VTimeInSec,
	req *akita.TLBIndexingSwitchMsg) bool {

	mmu.numWalksDone = 0
	mmu.lastChecked = 0

	mmu.avgWalksEnqueuedInCurEpoch = 0
	mmu.avgWalksEnqueuedInPrevEpoch = 0

	mmu.remoteMemAccessesInCurEpoch = 0
	mmu.remoteMemAccessesInPrevEpoch = 0

	mmu.memAccessesInCurEpoch = 0
	mmu.memAccessesInPrevEpoch = 0

	mmu.numWalksInCurEpoch = 0
	mmu.numWalksInPrevEpoch = 0

	if req.TLBInterleaving == 12 {
		mmu.sendStateInfo = false
	} else {
		mmu.sendStateInfo = true
	}
	mmu.interleaving = req.TLBInterleaving
	// fmt.Println(mmu.Name(), "flushing stats on switch", now, req.TLBInterleaving)
	return true
}

func (mmu *MPWMMU) parseFromPageWalkCache(now akita.VTimeInSec) bool {
	item := mmu.pageWalkCachePort.Peek()
	if item == nil {
		return false
	}

	switch msg := item.(type) {
	case *mem.DataReadyRsp:
		return mmu.handlePageWalkCacheResponse(msg, now)
	case *mem.WriteDoneRsp:
		mmu.pageWalkCachePort.Retrieve(now)

		return true
	default:
		panic("unknown message type")
	}

	return false
}

func (mmu *MPWMMU) parseFromMem(now akita.VTimeInSec) bool {
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

func (mmu *MPWMMU) handleMemResponse(rsp *mem.DataReadyRsp, now akita.VTimeInSec) bool {
	for _, walker := range mmu.pageWalkers {
		trans, ok := walker.outstandingReqs[rsp.RespondTo]
		if !ok {
			continue
		}

		rspInfo := rsp.Info.(*mem.DataReadyRspInfo)
		accessResult := rspInfo.AccessResult
		src := rspInfo.Src

		taskStep := fmt.Sprintf(
			"chiplet-%s-level-%d-%s",
			getChipletNum(src),
			trans.level,
			getAccessResultString(accessResult),
		)

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

		var madeProgress bool

		if trans.level+1 == 4 {
			madeProgress = walker.finalizeTransaction(now, trans)
		} else {
			madeProgress = mmu.fillPageWalkCache(now, trans)
		}

		if !madeProgress {
			return false
		}

		trans.level++

		delete(walker.outstandingReqs, rsp.RespondTo)
		delete(mmu.mappingMemAccess, rsp.RespondTo)

		if len(walker.outstandingReqs) == 0 {
			// update walker status
			walker.status = 0
		}

		mmu.TranslationPort.Retrieve(now)

		return true
	}
	log.Panicf("could not find matching mem access ID %s!", rsp.RespondTo)
	return false
}

func (mmu *MPWMMU) fillPageWalkCache(
	now akita.VTimeInSec,
	trans *transaction,
) bool {
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

	trans.msgID = writeReq.ID
	err := mmu.pageWalkCachePort.Send(writeReq)

	if err != nil {
		return false
	}

	return true
}

func (walker *MPWPageWalker) finalizeTransaction(
	now akita.VTimeInSec,
	trans *transaction,
) bool {
	req := trans.req
	page, found := walker.mmu.pageTable.Find(req.PID, req.VAddr)

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

	return walker.mmu.doPageWalkHit(now, trans)
}

func (mmu *MPWMMU) doPageWalkHit(
	now akita.VTimeInSec,
	trans *transaction,
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
	req := mmu.ToTop.Peek()
	if req == nil {
		return false
	}

	switch req := req.(type) {
	case *device.TranslationReq:
		return mmu.insertPageWalkQueue(req, now)
	default:
		log.Panicf("MMU canot handle request of type %s", reflect.TypeOf(req))
	}

	return false
}

func (mmu *MPWMMU) insertPageWalkQueue(
	req *device.TranslationReq,
	now akita.VTimeInSec,
) bool {
	for i := 0; i < len(mmu.pageWalkers); i++ {
		walkerIndex := (mmu.nextPointer + i) % len(mmu.pageWalkers)
		walker := mmu.pageWalkers[walkerIndex]
		if walker.canAcceptNewReq(mmu.queueCapacity) {
			rearrangedVAddr := mmu.pageTable.Rearrange(req.VAddr)
			root := mmu.pageTable.GetRoot(req.PID)
			translationInPipeline := transaction{
				req:   req,
				level: 0,
				msgID: "invalid",
				state: newTransaction,
				vAddr: rearrangedVAddr,
				PPN:   root,
			}

			walker.queue = append(walker.queue, &translationInPipeline)

			mmu.nextPointer++

			tracing.TraceReqReceive(req, now, mmu)

			mmu.ToTop.Retrieve(now)

			return true
		}
	}

	return false
}

func (mmu *MPWMMU) sendToPageWalkCache(
	now akita.VTimeInSec,
) bool {
	for i := 0; i < len(mmu.pageWalkers); i++ {
		walkerIndex := (mmu.nextPointer + i) % len(mmu.pageWalkers)
		walker := mmu.pageWalkers[walkerIndex]

		for _, trans := range walker.queue {
			if trans.state == newTransaction {
				readReq := mem.ReadReqBuilder{}.
					WithSendTime(now).
					WithSrc(mmu.pageWalkCachePort).
					WithDst(mmu.PageWalkCache).
					WithPID(trans.req.PID).
					WithAddress(mmu.pageTable.AlignToPage(trans.req.VAddr)).
					WithByteSize(8).
					Build()

				err := mmu.pageWalkCachePort.Send(readReq)

				if err != nil {
					return false
				}

				if _, ok := mmu.inflightPWCRequests[readReq.ID]; ok {
					log.Panic("duplicate PWC request ID detected!")
				}
				mmu.inflightPWCRequests[readReq.ID] = trans

				trans.msgID = readReq.ID
				trans.state = sentToPageWalkCache

				mmu.nextPointer++

				return true
			}
		}
	}

	return false
}

func (mmu *MPWMMU) handlePageWalkCacheResponse(
	rsp *mem.DataReadyRsp,
	now akita.VTimeInSec,
) bool {
	trans, ok := mmu.inflightPWCRequests[rsp.RespondTo]
	if !ok {
		log.Panic("could not find matching PWC request ID!")
	}
	delete(mmu.inflightPWCRequests, rsp.RespondTo)

	if trans.msgID == rsp.RespondTo {
		if rsp.Data != nil {
			rspData := binary.LittleEndian.Uint64(rsp.Data)
			trans.PPN = rspData & ^uint64(3)
			level := int(rspData & uint64(3))
			trans.vAddr = mmu.pageTable.MoveToLevel(trans.vAddr, level+1)
			trans.level = level + 1
		}

		trans.state = pageWalkCacheDone
	} else {
		log.Panic("message ID mismatch!")
	}

	tracing.AddTaskStep(tracing.MsgIDAtReceiver(trans.req, mmu),
		now, mmu, "pwc-hit-level"+strconv.Itoa(trans.level))

	mmu.pageWalkCachePort.Retrieve(now)

	return true
}

func (walker *MPWPageWalker) canAcceptNewReq(
	queueCapacity int,
) bool {
	return len(walker.queue) < queueCapacity
}

// SetLowModuleFinder sets the table recording where to find an address.
func (mmu *MPWMMU) SetLowModuleFinder(lmf cache.LowModuleFinder) {
	mmu.lowModuleFinder = lmf
}

// ToTop returns the port connecting to the top component.
func (mmu *MPWMMU) ToTopPort() akita.Port {
	return mmu.ToTop
}

// TranslationPort returns the port connecting to the lower memory system.
func (mmu *MPWMMU) TranslationPortPort() akita.Port {
	return mmu.TranslationPort
}

// CommandProcessorPort returns the port connecting to the command processor.
func (mmu *MPWMMU) CommandProcessorPort() akita.Port {
	return mmu.CommandProcessor
}

// ControlPortPort returns the port connecting to the control processor.
func (mmu *MPWMMU) ControlPortPort() akita.Port {
	return mmu.ControlPort
}

// SetCommandProcessorPort sets the command processor port.
func (mmu *MPWMMU) SetCommandProcessorPort(port akita.Port) {
	mmu.CommandProcessor = port
}

func (mmu *MPWMMU) GetNumActiveWalkers() int {
	return len(mmu.inflightMemRequests)
}
