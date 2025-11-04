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

type CaPWQPageWalker struct {
	mmu *CaPWQMMU

	capacity int

	queue []*Transaction
}

// CaPWQMMU is the default mmu implementation. It is also an akita Component.
type CaPWQMMU struct {
	akita.TickingComponent

	ToTop            akita.Port
	ToCache          akita.Port
	ControlPort      akita.Port
	CommandProcessor akita.Port

	pageWalkCachePort akita.Port
	PageWalkCache     akita.Port
	topSender         akitaext.BufferedSender

	TranslationPort      akita.Port
	lowModuleFinder      cache.LowModuleFinder
	CacheLowModuleFinder cache.LowModuleFinder
	numChiplets          uint64

	pageTable           *device.PageTableImpl
	maxRequestsInFlight int

	pageWalkers   []*CaPWQPageWalker
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
	interleaving        uint64

	inflightPWCRequests   map[string]*Transaction
	inflightMemRequests   map[string]*Transaction
	inflightCacheRequests map[string]*Transaction

	pendingIssueToMem   []*Transaction
	pendingIssueToCache []*Transaction

	pendingRspFromMem map[string]*mem.DataReadyRsp
}

// Tick defines how the MMU update state each cycle
func (mmu *CaPWQMMU) Tick(now akita.VTimeInSec) bool {
	madeProgress := false

	madeProgress = mmu.performCtrlReq(now) || madeProgress

	if mmu.GetNumActiveWalkers() > 0 {
		tracing.StartTask("", "", now, mmu, "imbalance", "", nil)
	}

	madeProgress = mmu.topSender.Tick(now) || madeProgress
	madeProgress = mmu.walkPageTable(now) || madeProgress
	madeProgress = mmu.sendToMem(now) || madeProgress
	madeProgress = mmu.sendMetaDataToCache(now) || madeProgress
	madeProgress = mmu.parseFromPageWalkCache(now) || madeProgress
	madeProgress = mmu.parseFromMem(now) || madeProgress
	madeProgress = mmu.parseFromTop(now) || madeProgress
	madeProgress = mmu.parseFromCache(now) || madeProgress
	return true
	// return madeProgress
}

func (mmu *CaPWQMMU) performCtrlReq(now akita.VTimeInSec) bool {
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

func (mmu *CaPWQMMU) sendCollectedStatsToCP(now akita.VTimeInSec) bool {
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

func (mmu *CaPWQMMU) trace(now akita.VTimeInSec, what string) {
	ctx := akita.HookCtx{
		Domain: mmu,
		Now:    now,
		Item:   what,
	}

	mmu.InvokeHook(ctx)
}

func (mmu *CaPWQMMU) walkPageTable(now akita.VTimeInSec) bool {
	for _, walker := range mmu.pageWalkers {
		numWalksDone := walker.walkPageTable()

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

	numActiveTransactions := len(mmu.inflightMemRequests) +
		len(mmu.pendingRspFromMem) +
		len(mmu.pendingIssueToMem)

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

func (walker *CaPWQPageWalker) walkPageTable() int {
	if len(walker.queue) == 0 {
		return 0
	}

	trans := walker.queue[0]

	if trans.state == pageWalkCacheDone || trans.state == memDone {
		walker.sendToMem()
	}

	if trans.state == transactionFinished {
		walker.queue = walker.queue[1:]

		return 1
	} else {
		return 0
	}
}

func (walker *CaPWQPageWalker) sendToMem() {
	walker.queue[0].state = sentToMem
	walker.mmu.pendingIssueToMem = append(
		walker.mmu.pendingIssueToMem,
		walker.queue[0],
	)
}

func (mmu *CaPWQMMU) generateMemReq(
	now akita.VTimeInSec,
	trans *Transaction,
) *mem.ReadReq {
	PPN := trans.PPN
	PPNWithOffset := mmu.pageTable.AddOffset(PPN, trans.vAddr)

	trans.vAddr = mmu.pageTable.NextLevel(trans.vAddr)

	srcPort := mmu.TranslationPort
	readReqInfo := &mem.ReadReqInfo{ReturnAccessInfo: true}
	dstPort := mmu.lowModuleFinder.Find(PPNWithOffset)

	readReq := mem.ReadReqBuilder{}.
		WithSrc(srcPort).
		WithDst(dstPort).
		WithPID(trans.req.PID).
		WithAddress(PPNWithOffset).
		WithByteSize(8).
		WithInfo(readReqInfo).
		WithSendTime(now).
		Build()

	return readReq
}

func (mmu *CaPWQMMU) sendMsgToCP(now akita.VTimeInSec) bool {
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

func (mmu *CaPWQMMU) switchIndexing(now akita.VTimeInSec,
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

func (mmu *CaPWQMMU) parseFromPageWalkCache(now akita.VTimeInSec) bool {
	item := mmu.pageWalkCachePort.Peek()

	if item == nil {
		return false
	}

	switch msg := item.(type) {
	case *mem.DataReadyRsp:
		return mmu.handlePageWalkCacheResponse(msg, now)
	case *mem.WriteDoneRsp:
		// Do nothing for write done response
		mmu.pageWalkCachePort.Retrieve(now)

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
		return mmu.fetchMetaDataFromCache(now, msg)
	default:
		panic("unknown message type")
	}

	return false
}

func (mmu *CaPWQMMU) parseFromCache(now akita.VTimeInSec) bool {
	item := mmu.ToCache.Peek()
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

func (mmu *CaPWQMMU) fetchMetaDataFromCache(
	now akita.VTimeInSec,
	rsp *mem.DataReadyRsp,
) bool {
	//for i, trans := range mmu.pendingIssueToCache {
	//	if trans.msgID == rsp.RespondTo {
	//		mmu.pendingIssueToCache = append(
	//			mmu.pendingIssueToCache[:i],
	//			mmu.pendingIssueToCache[i+1:]...,
	//		)
	//
	//		rspInfo := rsp.Info.(*mem.DataReadyRspInfo)
	//		accessResult := rspInfo.AccessResult
	//		src := rspInfo.Src
	//
	//		taskStep := fmt.Sprintf(
	//			"chiplet-%s-level-%d-%s",
	//			getChipletNum(src),
	//			trans.level,
	//			getAccessResultString(accessResult),
	//		)
	//
	//		tracing.AddTaskStep(
	//			trans.msgID+"MMU-mem-latency",
	//			now, mmu,
	//			taskStep,
	//		)
	//		tracing.EndTask(
	//			trans.msgID+"MMU-mem-latency",
	//			now,
	//			mmu,
	//		)
	//
	//		trans.PPN = binary.LittleEndian.Uint64(rsp.Data)
	//		trans.state = memDone
	//		if trans.level+1 == 4 {
	//			for _, walker := range mmu.pageWalkers {
	//				if len(walker.queue) == 0 {
	//					continue
	//				}
	//				if walker.queue[0] == trans {
	//					walker.finalizeTransaction(now, trans)
	//				}
	//			}
	//			panic("Transaction not found!")
	//		} else {
	//			mmu.fillPageWalkCache(now, trans)
	//		}
	//
	//		trans.level++
	//
	//		delete(mmu.inflightTransactions, rsp.RespondTo)
	//
	//		mmu.TranslationPort.Retrieve(now)
	//
	//		return true
	//	}
	//}

	trans, ok := mmu.inflightMemRequests[rsp.RespondTo]
	if !ok {
		log.Panic("could not find matching mem access ID!")
	}

	cachePort := mmu.CacheLowModuleFinder.Find(trans.req.VAddr)

	req := mem.ReadReqBuilder{}.
		WithSrc(mmu.ToCache).
		WithDst(cachePort).
		WithSendTime(now).
		WithPID(trans.req.PID).
		WithInfo(trans).
		Build()

	err := mmu.ToCache.Send(req)
	if err != nil {
		return false
	}

	delete(mmu.inflightMemRequests, rsp.RespondTo)

	mmu.inflightCacheRequests[req.ID] = trans
	mmu.pendingRspFromMem[trans.msgID] = rsp

	mmu.TranslationPort.Retrieve(now)

	tracing.StartTracingNetwork(
		req,
		now,
		mmu,
		"trace-mmu-cache-req",
	)

	return true
}

func (mmu *CaPWQMMU) sendToPageWalkCache(
	req *device.TranslationReq,
	now akita.VTimeInSec,
) bool {
	readReq := mem.ReadReqBuilder{}.
		WithSendTime(now).
		WithSrc(mmu.pageWalkCachePort).
		WithDst(mmu.PageWalkCache).
		WithPID(req.PID).
		WithAddress(mmu.pageTable.AlignToPage(req.VAddr)).
		WithByteSize(8).
		Build()

	err := mmu.pageWalkCachePort.Send(readReq)

	if err != nil {
		return false
	}

	rearrangedVAddr := mmu.pageTable.Rearrange(req.VAddr)
	root := mmu.pageTable.GetRoot(req.PID)
	translationInPipeline := Transaction{
		req:   req,
		level: 0,
		msgID: readReq.ID,
		state: sentToPageWalkCache,
		vAddr: rearrangedVAddr,
		PPN:   root,
	}

	translationInPipeline.Meta().ID = akita.GetIDGenerator().Generate()

	if _, ok := mmu.inflightPWCRequests[readReq.ID]; ok {
		log.Panic("duplicate PWC request ID detected!")
	}

	mmu.inflightPWCRequests[readReq.ID] = &translationInPipeline

	tracing.TraceReqReceive(req, now, mmu)

	mmu.ToTop.Retrieve(now)

	return true
}

func (mmu *CaPWQMMU) sendToMem(now akita.VTimeInSec) bool {
	madeProgress := false

	for len(mmu.pendingIssueToMem) > 0 {
		trans := mmu.pendingIssueToMem[0]

		req := mmu.generateMemReq(
			now,
			trans,
		)

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

		// remove from pendingIssueToMem
		mmu.pendingIssueToMem = mmu.pendingIssueToMem[1:]
		mmu.pendingIssueToCache = append(
			mmu.pendingIssueToCache,
			trans,
		)

		mmu.inflightMemRequests[req.ID] = trans

		madeProgress = true
	}

	return madeProgress
}

func (mmu *CaPWQMMU) sendMetaDataToCache(now akita.VTimeInSec) bool {
	madeProgress := false

	for len(mmu.pendingIssueToCache) > 0 {
		trans := mmu.pendingIssueToCache[0]

		cachePort := mmu.CacheLowModuleFinder.Find(trans.req.VAddr)

		req := mem.WriteReqBuilder{}.
			WithSendTime(now).
			WithSrc(mmu.ToCache).
			WithDst(cachePort).
			WithSendTime(now).
			WithPID(trans.req.PID).
			WithInfo(trans).
			Build()

		req.TrafficBytes = 40

		err := mmu.ToCache.Send(req)
		if err != nil {
			break
		}

		tracing.StartTracingNetwork(
			req,
			now,
			mmu,
			"trace-mmu-cache-req",
		)

		mmu.pendingIssueToCache = mmu.pendingIssueToCache[1:]

		for i := 0; i < len(mmu.pageWalkers); i++ {
			if len(mmu.pageWalkers[i].queue) == 0 {
				continue
			}

			if mmu.pageWalkers[i].queue[0] == trans {
				mmu.pageWalkers[i].queue = mmu.pageWalkers[i].queue[1:]

				break
			}
		}

		madeProgress = true
	}

	return madeProgress
}

func (mmu *CaPWQMMU) handlePageWalkCacheResponse(
	rsp *mem.DataReadyRsp,
	now akita.VTimeInSec,
) bool {
	for i := 0; i < len(mmu.pageWalkers); i++ {
		walkerIndex := (mmu.nextPointer + i) % len(mmu.pageWalkers)
		walker := mmu.pageWalkers[walkerIndex]
		if walker.canAccept() {
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

			walker.queue = append(walker.queue, trans)

			mmu.nextPointer++

			mmu.pageWalkCachePort.Retrieve(now)

			return true
		}
	}

	return false
}

func (mmu *CaPWQMMU) handleMemResponse(
	rsp *mem.DataReadyRsp,
	now akita.VTimeInSec,
) bool {
	trans := rsp.Info.(*Transaction)

	for i := 0; i < len(mmu.pageWalkers); i++ {
		walkerIndex := (mmu.nextPointer + i) % len(mmu.pageWalkers)
		walker := mmu.pageWalkers[walkerIndex]

		mmu.nextPointer++

		walker.queue = append([]*Transaction{trans}, walker.queue...)

		trans := mmu.inflightCacheRequests[rsp.RespondTo]
		PTERsp := mmu.pendingRspFromMem[trans.msgID]

		rspInfo := PTERsp.Info.(*mem.DataReadyRspInfo)
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

		delete(mmu.pendingRspFromMem, trans.msgID)
		delete(mmu.inflightCacheRequests, rsp.RespondTo)

		trans.PPN = binary.LittleEndian.Uint64(PTERsp.Data)
		trans.state = memDone
		if trans.level+1 == 4 {
			walker.finalizeTransaction(now, trans)
		} else {
			mmu.fillPageWalkCache(now, trans)
		}

		trans.level++

		mmu.ToCache.Retrieve(now)

		tracing.StopTracingNetwork(
			rsp, now, mmu, "trace-mmu-cache-req")

		return true
	}

	return false
}

func (mmu *CaPWQMMU) fillPageWalkCache(
	now akita.VTimeInSec,
	trans *Transaction,
) {
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

	mmu.pageWalkCachePort.Send(writeReq)
}

func (walker *CaPWQPageWalker) finalizeTransaction(
	now akita.VTimeInSec,
	trans *Transaction,
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
		WithDst(trans.req.Src).
		WithRspTo(trans.req.ID).
		WithPage(trans.page).
		Build()

	mmu.topSender.Send(rsp)

	tracing.TraceReqComplete(trans.req, now, mmu)

	return true
}

func (mmu *CaPWQMMU) parseFromTop(now akita.VTimeInSec) bool {
	req := mmu.ToTop.Peek()
	if req == nil {
		return false
	}

	switch req := req.(type) {
	case *device.TranslationReq:
		return mmu.sendToPageWalkCache(req, now)
	default:
		log.Panicf("MMU canot handle request of type %s", reflect.TypeOf(req))
	}

	return false
}

func (walker *CaPWQPageWalker) canAccept() bool {
	return len(walker.queue) < walker.capacity
}

// SetLowModuleFinder sets the table recording where to find an address.
func (mmu *CaPWQMMU) SetLowModuleFinder(lmf cache.LowModuleFinder) {
	mmu.lowModuleFinder = lmf
}

// ToTop returns the port connecting to the top component.
func (mmu *CaPWQMMU) ToTopPort() akita.Port {
	return mmu.ToTop
}

// TranslationPort returns the port connecting to the lower memory system.
func (mmu *CaPWQMMU) TranslationPortPort() akita.Port {
	return mmu.TranslationPort
}

// CommandProcessorPort returns the port connecting to the command processor.
func (mmu *CaPWQMMU) CommandProcessorPort() akita.Port {
	return mmu.CommandProcessor
}

// ControlPortPort returns the port connecting to the control processor.
func (mmu *CaPWQMMU) ControlPortPort() akita.Port {
	return mmu.ControlPort
}

// SetCommandProcessorPort sets the command processor port.
func (mmu *CaPWQMMU) SetCommandProcessorPort(port akita.Port) {
	mmu.CommandProcessor = port
}

func (mmu *CaPWQMMU) GetNumActiveWalkers() int {
	return len(mmu.pendingIssueToMem)
}
