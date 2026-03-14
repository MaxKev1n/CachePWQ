package l1cache

import (
	"strconv"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/mem/cache"
	"gitlab.com/akita/mem/vm"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/ca"
	"gitlab.com/akita/util/pipelining"
	"gitlab.com/akita/util/tracing"
)

type pipelineItem struct {
	taskID string
	msg    akita.Msg
}

func (item *pipelineItem) TaskID() string {
	return item.taskID
}

type CaPWQPair struct {
	PID     ca.PID
	Address uint64
}

type CaPWQEntry struct {
	blocks []vm.CaPWQBlock
}

type IdealCaPWQCache struct {
	*akita.TickingComponent

	numReqPerCycle int

	mmuSidePort     akita.Port
	pipeline        pipelining.Pipeline
	postPipelineBuf util.Buffer

	storage map[CaPWQPair]*CaPWQEntry
}

func NewIdealCaPWQCache(
	name string,
	engine akita.Engine,
	freq akita.Freq,
	numReqPerCycle int,
	latency int,
) *IdealCaPWQCache {
	c := &IdealCaPWQCache{
		numReqPerCycle: numReqPerCycle,
	}

	c.TickingComponent = akita.NewTickingComponent(
		name, engine, freq, c)

	c.mmuSidePort = akita.NewLimitNumMsgPort(
		c,
		numReqPerCycle,
		name+".MMUSidePort",
	)

	c.postPipelineBuf = util.NewBuffer(numReqPerCycle)
	c.pipeline = pipelining.MakeBuilder().
		WithPipelineWidth(numReqPerCycle).
		WithNumStage(latency).
		WithCyclePerStage(1).
		WithPostPipelineBuffer(c.postPipelineBuf).
		Build(name + ".Pipeline")

	c.storage = make(map[CaPWQPair]*CaPWQEntry)

	return c
}

func (c *IdealCaPWQCache) trace(now akita.VTimeInSec, what string) {
	ctx := akita.HookCtx{
		Domain: c,
		Now:    now,
		Item:   what,
	}

	c.InvokeHook(ctx)
}

func (c *IdealCaPWQCache) Tick(now akita.VTimeInSec) bool {
	madeProgress := false

	for i := 0; i < c.numReqPerCycle; i++ {
		madeProgress = madeProgress || c.processingPipeline(now)
		madeProgress = madeProgress || c.parseFromMMU(now)
	}
	madeProgress = madeProgress || c.pipeline.Tick(now)

	return madeProgress
}

func (c *IdealCaPWQCache) parseFromMMU(now akita.VTimeInSec) bool {
	item := c.mmuSidePort.Peek()
	if item == nil {
		return false
	}

	switch msg := item.(type) {
	case *mem.ReadReq:
		return c.processingRequests(now, msg)
	case *mem.WriteReq:
		return c.processingRequests(now, msg)
	default:
		panic("Unsupported message type.")
	}

	return false
}

func (c *IdealCaPWQCache) processingRequests(
	now akita.VTimeInSec,
	req akita.Msg,
) bool {
	if !c.pipeline.CanAccept() {
		return false
	}

	item := &pipelineItem{
		taskID: akita.GetIDGenerator().Generate(),
		msg:    req,
	}

	c.pipeline.Accept(now, item)
	c.mmuSidePort.Retrieve(now)

	return true
}

func (c *IdealCaPWQCache) processingPipeline(now akita.VTimeInSec) bool {
	item := c.postPipelineBuf.Peek()
	if item == nil {
		return false
	}

	pItem := item.(*pipelineItem)
	switch msg := pItem.msg.(type) {
	case *mem.ReadReq:
		return c.handleReadReq(now, msg)
	case *mem.WriteReq:
		return c.handleWriteReq(now, msg)
	default:
		panic("Unsupported message type.")
	}

	return false
}

func (c *IdealCaPWQCache) handleReadReq(
	now akita.VTimeInSec,
	req *mem.ReadReq,
) bool {
	pair := CaPWQPair{
		PID:     req.PID,
		Address: req.Address,
	}

	entry, ok := c.storage[pair]
	if !ok {
		panic("Cache miss in ideal cache.")
	}

	if len(entry.blocks) == 0 {
		panic("No block in the CaPWQ entry.")
	}

	entry.blocks[0].PPN = req.Info.(uint64)

	rsp := mem.DataReadyRspBuilder{}.
		WithSendTime(now).
		WithSrc(c.mmuSidePort).
		WithDst(req.Src).
		WithRspTo(req.ID).
		WithInfo(entry.blocks[0]).
		Build()

	rsp.TrafficBytes += 12

	err := c.mmuSidePort.Send(rsp)
	if err != nil {
		return false
	}

	c.postPipelineBuf.Pop()

	// Remove the block from the cache.
	entry.blocks = entry.blocks[1:]

	if len(entry.blocks) == 0 {
		delete(c.storage, pair)
	}

	return true
}

func (c *IdealCaPWQCache) handleWriteReq(
	now akita.VTimeInSec,
	req *mem.WriteReq,
) bool {
	pair := CaPWQPair{
		PID:     req.PID,
		Address: req.Address,
	}

	entry, ok := c.storage[pair]
	if !ok {
		entry = &CaPWQEntry{}
		c.storage[pair] = entry
	}

	entry.blocks = append(
		entry.blocks,
		req.Info.(vm.CaPWQBlock),
	)

	done := mem.WriteDoneRspBuilder{}.
		WithSendTime(now).
		WithSrc(c.mmuSidePort).
		WithDst(req.Src).
		WithRspTo(req.ID).
		Build()

	err := c.mmuSidePort.Send(done)
	if err != nil {
		return false
	}

	c.postPipelineBuf.Pop()

	tracing.StartTask(
		"",
		"",
		now,
		c,
		"l1capwq_cache_len",
		strconv.Itoa(int(len(entry.blocks))),
		nil,
	)

	return true
}

func (c *IdealCaPWQCache) GetTopPort() akita.Port {
	panic("not implemented")
}

func (c *IdealCaPWQCache) GetBottomPort() akita.Port {
	panic("not implemented")
}

func (c *IdealCaPWQCache) GetControlPort() akita.Port {
	panic("not implemented")
}

func (c *IdealCaPWQCache) GetName() string {
	return c.Name()
}

func (c *IdealCaPWQCache) SetLowModuleFinder(lmf cache.LowModuleFinder) {
	panic("not implemented")
}

func (c *IdealCaPWQCache) GetWalkerPort() akita.Port {
	return c.mmuSidePort
}
