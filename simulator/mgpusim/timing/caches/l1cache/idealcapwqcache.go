package l1cache

import (
	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/mem/cache"
	"gitlab.com/akita/mem/vm"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/pipelining"
)

type pipelineItem struct {
	taskID string
	msg    akita.Msg
}

func (item *pipelineItem) TaskID() string {
	return item.taskID
}

type IdealCaPWQCache struct {
	*akita.TickingComponent

	numReqPerCycle int

	mmuSidePort     akita.Port
	pipeline        pipelining.Pipeline
	postPipelineBuf util.Buffer

	storage map[string]vm.CaPWQBlock
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

	c.storage = make(map[string]vm.CaPWQBlock)

	return c
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
	block, ok := c.storage[req.Info.(string)]
	if !ok {
		panic("Cache miss in ideal cache.")
	}

	rsp := mem.DataReadyRspBuilder{}.
		WithSendTime(now).
		WithSrc(c.mmuSidePort).
		WithDst(req.Src).
		WithRspTo(req.ID).
		WithInfo(block).
		Build()

	rsp.TrafficBytes += 12

	err := c.mmuSidePort.Send(rsp)
	if err != nil {
		return false
	}

	c.postPipelineBuf.Pop()

	return true
}

func (c *IdealCaPWQCache) handleWriteReq(
	now akita.VTimeInSec,
	req *mem.WriteReq,
) bool {
	info := req.Info.(vm.CaPWQBlock)

	if _, ok := c.storage[info.Req.ID]; ok {
		panic("Overwriting existing CaPWQ block in ideal cache.")
	}

	c.storage[info.Req.ID] = info

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

func (c *IdealCaPWQCache) GetMMUSidePort() akita.Port {
	return c.mmuSidePort
}

func (c *IdealCaPWQCache) GetName() string {
	return c.Name()
}

func (c *IdealCaPWQCache) SetLowModuleFinder(lmf cache.LowModuleFinder) {
	panic("not implemented")
}
