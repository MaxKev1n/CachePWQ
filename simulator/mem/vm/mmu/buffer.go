package mmu

import (
	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/pipelining"
)

type PipelineItem struct {
	taskID string
	msg    akita.Msg
}

func (t PipelineItem) TaskID() string {
	return t.taskID
}

type TickingBuffer struct {
	*akita.TickingComponent

	ToTop akita.Port

	storage map[string]*Transaction

	pendingIssues []*Transaction

	inPipeline     pipelining.Pipeline
	inLookupBuffer util.Buffer

	outPipeline     pipelining.Pipeline
	outLookupBuffer util.Buffer

	mmu MMU
}

func (buffer *TickingBuffer) Tick(now akita.VTimeInSec) bool {
	madeProgress := false

	for i := 0; i < 4; i++ {
		madeProgress = madeProgress || buffer.parseFromTop(now)
	}

	madeProgress = madeProgress || buffer.inPipeline.Tick(now)
	madeProgress = madeProgress || buffer.outPipeline.Tick(now)

	for i := 0; i < 4; i++ {
		madeProgress = madeProgress || buffer.checkInBuffer()
		madeProgress = madeProgress || buffer.checkOutBuffer()
	}

	for i := 0; i < 4; i++ {
		madeProgress = madeProgress || buffer.sendToTop(now)
	}

	return madeProgress
}

func (buffer *TickingBuffer) parseFromTop(now akita.VTimeInSec) bool {
	item := buffer.ToTop.Peek()
	if item == nil {
		return false
	}

	switch msg := item.(type) {
	case *Transaction:
		return buffer.push(now, msg)
	case *mem.DataReadyRsp:
		return buffer.pop(now, msg)
	default:
		panic("unknown message type in TickingBuffer")
	}

	return false
}

func (buffer *TickingBuffer) push(
	now akita.VTimeInSec,
	msg akita.Msg,
) bool {
	if buffer.inPipeline.CanAccept() {
		pipelineItem := &PipelineItem{
			taskID: akita.GetIDGenerator().Generate(),
			msg:    msg,
		}

		buffer.inPipeline.Accept(now, pipelineItem)

		buffer.ToTop.Retrieve(now)

		return true
	}

	return false
}

func (buffer *TickingBuffer) pop(
	now akita.VTimeInSec,
	msg akita.Msg,
) bool {
	if buffer.outPipeline.CanAccept() {
		pipelineItem := &PipelineItem{
			taskID: akita.GetIDGenerator().Generate(),
			msg:    msg,
		}

		buffer.outPipeline.Accept(now, pipelineItem)

		buffer.ToTop.Retrieve(now)

		return true
	}

	return false
}

func (buffer *TickingBuffer) checkInBuffer() bool {
	item := buffer.inLookupBuffer.Peek()
	if item == nil {
		return false
	}

	pipelineItem := item.(*PipelineItem)

	switch msg := pipelineItem.msg.(type) {
	case *Transaction:
		return buffer.insert(msg)
	default:
		panic("unknown message type in TickingBuffer")
	}

	return false
}

func (buffer *TickingBuffer) checkOutBuffer() bool {
	item := buffer.outLookupBuffer.Peek()
	if item == nil {
		return false
	}

	pipelineItem := item.(*PipelineItem)

	switch msg := pipelineItem.msg.(type) {
	case *mem.DataReadyRsp:
		return buffer.remove(msg)
	default:
		panic("unknown message type in TickingBuffer")
	}

	return false
}

func (buffer *TickingBuffer) insert(
	trans *Transaction,
) bool {
	if _, exists := buffer.storage[trans.msgID]; exists {
		panic("duplicated Transaction in buffer")
	}

	buffer.storage[trans.msgID] = trans

	buffer.inLookupBuffer.Pop()

	return true
}

func (buffer *TickingBuffer) remove(
	rsp *mem.DataReadyRsp,
) bool {
	if _, exists := buffer.storage[rsp.RespondTo]; !exists {
		return false
	}

	trans := buffer.storage[rsp.RespondTo]
	delete(buffer.storage, rsp.RespondTo)

	buffer.pendingIssues = append(buffer.pendingIssues, trans)

	buffer.outLookupBuffer.Pop()

	return true
}

func (buffer *TickingBuffer) sendToTop(
	now akita.VTimeInSec,
) bool {
	if len(buffer.pendingIssues) == 0 {
		return false
	}

	trans := buffer.pendingIssues[0]

	dst := trans.Meta().Src

	trans.Meta().Src = buffer.ToTop
	trans.Meta().Dst = dst
	trans.Meta().SendTime = now

	err := buffer.ToTop.Send(trans)
	if err != nil {
		return false
	}

	buffer.pendingIssues = buffer.pendingIssues[1:]

	return true
}

func NewTickingBuffer(
	engine akita.Engine,
	freq akita.Freq,
) *TickingBuffer {
	buffer := &TickingBuffer{}

	buffer.TickingComponent = akita.NewTickingComponent(
		"TickingBuffer",
		engine,
		freq,
		buffer,
	)

	buffer.ToTop = akita.NewLimitNumMsgPort(buffer, 512, buffer.Name()+".ToTop")

	buffer.inLookupBuffer = util.NewBuffer(32)
	pipelineBuilder := pipelining.MakeBuilder().
		WithPipelineWidth(4).
		WithNumStage(28).
		WithCyclePerStage(1).
		WithPostPipelineBuffer(buffer.inLookupBuffer)
	buffer.inPipeline = pipelineBuilder.Build(buffer.Name() + "_in_pipeline")

	buffer.outLookupBuffer = util.NewBuffer(32)
	outPipelineBuilder := pipelining.MakeBuilder().
		WithPipelineWidth(4).
		WithNumStage(28).
		WithCyclePerStage(1).
		WithPostPipelineBuffer(buffer.outLookupBuffer)
	buffer.outPipeline = outPipelineBuilder.Build(buffer.Name() + "_out_pipeline")

	buffer.storage = make(map[string]*Transaction)

	return buffer
}
