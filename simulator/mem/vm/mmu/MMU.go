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

type PageWalker struct {
	mmu *MMUImpl

	inflightTrans *Transaction
}

// MMUImpl is the default mmu implementation. It is also an akita Component.
type MMUImpl struct {
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
	maxRequestsInFlight int

	queue         []*device.TranslationReq
	pageWalkers   []*PageWalker
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

	inflightMemRequests []*mem.ReadReq

	maxMemRequestsInFlight int
	memReqToTrans          map[string]*Transaction
}

// Tick defines how the MMU update state each cycle
func (mmu *MMUImpl) Tick(now akita.VTimeInSec) bool {
	madeProgress := false

	madeProgress = mmu.performCtrlReq(now) || madeProgress

	if mmu.GetNumActiveWalkers() > 0 {
		tracing.StartTask("", "", now, mmu, "imbalance", "", nil)
	}

	madeProgress = mmu.topSender.Tick(now) || madeProgress
	madeProgress = mmu.walkPageTable(now) || madeProgress
	madeProgress = mmu.sendToMem(now) || madeProgress
	madeProgress = mmu.parseFromPageWalkCache(now) || madeProgress
	madeProgress = mmu.parseFromMem(now) || madeProgress
	madeProgress = mmu.parseFromTop(now) || madeProgress
	madeProgress = mmu.issueToWalkers(now) || madeProgress
	return true
	// return madeProgress
}

func (mmu *MMUImpl) performCtrlReq(now akita.VTimeInSec) bool {
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

func (mmu *MMUImpl) sendCollectedStatsToCP(now akita.VTimeInSec) bool {
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

func (mmu *MMUImpl) trace(now akita.VTimeInSec, what string) {
	ctx := akita.HookCtx{
		Domain: mmu,
		Now:    now,
		Item:   what,
	}

	mmu.InvokeHook(ctx)
}

func (mmu *MMUImpl) walkPageTable(now akita.VTimeInSec) bool {
	numActiveTransactions := 0

	for _, walker := range mmu.pageWalkers {
		walker.walkPageTable()

		if walker.inflightTrans != nil {
			numActiveTransactions++
		}

		walkDone := false
		if walker.inflightTrans != nil &&
			walker.inflightTrans.state == transactionFinished {
			walkDone = true

			walker.inflightTrans = nil
		}

		if walkDone {
			mmu.numWalksDone++
			if mmu.numWalksDone == 1200 && !mmu.sendStateInfo && mmu.interleaving == 12 {
				mmu.numWalksDone = 0
				mmu.lastChecked = 0
				mmu.sendStateInfo = true
			}
			mmu.avgWalksEnqueuedInCurEpoch =
				(mmu.avgWalksEnqueuedInCurEpoch*float64(mmu.numWalksInCurEpoch) +
					float64(len(mmu.ToTop.(akita.MsgBufferContainer).GetBuffer()))) /
					float64(mmu.numWalksInCurEpoch+1)

			mmu.numWalksInCurEpoch++
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
		if walker.inflightTrans != nil {
			numTranslations++
		}
	}

	return numTranslations > 0 || len(mmu.queue) > 0
}

func (walker *PageWalker) walkPageTable() {
	trans := walker.inflightTrans

	if trans == nil {
		return
	}

	if trans.state == pageWalkCacheDone || trans.state == memDone {
		req := walker.generateMemReq(trans)

		if len(walker.mmu.inflightMemRequests)+1 > walker.mmu.maxMemRequestsInFlight {
			return
		}

		if _, ok := walker.mmu.memReqToTrans[req.ID]; ok {
			panic("duplicate mem access ID detected!")
		}

		walker.inflightTrans.state = sentToMem
		walker.mmu.memReqToTrans[req.ID] = trans
		walker.mmu.inflightMemRequests = append(
			walker.mmu.inflightMemRequests,
			req,
		)
	}
}

func (walker *PageWalker) generateMemReq(
	trans *Transaction,
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

	return readReq
}

func (mmu *MMUImpl) sendMsgToCP(now akita.VTimeInSec) bool {
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

func (mmu *MMUImpl) switchIndexing(now akita.VTimeInSec,
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

func (mmu *MMUImpl) sendToMem(now akita.VTimeInSec) bool {
	madeProgress := false

	for len(mmu.inflightMemRequests) > 0 {
		req := mmu.inflightMemRequests[0]
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

		trans, ok := mmu.memReqToTrans[req.ID]
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
		mmu.inflightMemRequests = mmu.inflightMemRequests[1:]

		madeProgress = true
	}

	return madeProgress
}

func (mmu *MMUImpl) handlePageWalkCacheResponse(
	rsp *mem.DataReadyRsp,
	now akita.VTimeInSec,
) {
	for _, walker := range mmu.pageWalkers {
		trans := walker.inflightTrans

		if trans == nil {
			continue
		}

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

			return
		}
	}
	panic("could not find matching page walk cache access ID!")
}

func (mmu *MMUImpl) handleMemResponse(rsp *mem.DataReadyRsp, now akita.VTimeInSec) {
	for _, walker := range mmu.pageWalkers {
		if walker.inflightTrans == nil {
			continue
		}

		if rsp.RespondTo != walker.inflightTrans.msgID {
			continue
		}

		trans := mmu.memReqToTrans[rsp.RespondTo]

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
		if trans.level+1 == 4 {
			walker.finalizeTransaction(now, trans)
		} else {
			mmu.fillPageWalkCache(now, trans)
		}

		trans.level++

		delete(mmu.memReqToTrans, rsp.RespondTo)

		return
	}
	log.Panicf("could not find matching mem access ID %s!", rsp.RespondTo)
}

func (mmu *MMUImpl) fillPageWalkCache(
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

func (walker *PageWalker) finalizeTransaction(
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

func (mmu *MMUImpl) doPageWalkHit(
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

func (mmu *MMUImpl) parseFromTop(now akita.VTimeInSec) bool {
	req := mmu.ToTop.Peek()
	if req == nil {
		return false
	}

	if len(mmu.queue) >= mmu.queueCapacity {
		return false
	}

	switch req := req.(type) {
	case *device.TranslationReq:
		mmu.queue = append(
			mmu.queue,
			req,
		)

		tracing.TraceReqReceive(req, now, mmu)

		mmu.ToTop.Retrieve(now)
	default:
		log.Panicf("MMU canot handle request of type %s", reflect.TypeOf(req))
	}

	return false
}

func (mmu *MMUImpl) issueToWalkers(now akita.VTimeInSec) bool {
	if len(mmu.queue) == 0 {
		return false
	}

	req := mmu.queue[0]

	for _, walker := range mmu.pageWalkers {
		if walker.inflightTrans == nil {
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

			walker.inflightTrans = &translationInPipeline

			mmu.queue = mmu.queue[1:]

			return true
		}
	}

	return false
}

// SetLowModuleFinder sets the table recording where to find an address.
func (mmu *MMUImpl) SetLowModuleFinder(lmf cache.LowModuleFinder) {
	mmu.lowModuleFinder = lmf
}

// ToTop returns the port connecting to the top component.
func (mmu *MMUImpl) ToTopPort() akita.Port {
	return mmu.ToTop
}

// TranslationPort returns the port connecting to the lower memory system.
func (mmu *MMUImpl) TranslationPortPort() akita.Port {
	return mmu.TranslationPort
}

// CommandProcessorPort returns the port connecting to the command processor.
func (mmu *MMUImpl) CommandProcessorPort() akita.Port {
	return mmu.CommandProcessor
}

// ControlPortPort returns the port connecting to the control processor.
func (mmu *MMUImpl) ControlPortPort() akita.Port {
	return mmu.ControlPort
}

// SetCommandProcessorPort sets the command processor port.
func (mmu *MMUImpl) SetCommandProcessorPort(port akita.Port) {
	mmu.CommandProcessor = port
}

func (mmu *MMUImpl) GetNumActiveWalkers() int {
	numActiveWalker := 0

	for _, walker := range mmu.pageWalkers {
		if walker.inflightTrans != nil {
			numActiveWalker++
		}
	}

	return numActiveWalker
}
