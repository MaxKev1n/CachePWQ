package mmu

import (
	"log"

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

	storage map[string]*transaction

	pendingIssues []*transaction

	pipeline     pipelining.Pipeline
	lookupBuffer util.Buffer

	mmu MMU
}

func (buffer *TickingBuffer) Tick(now akita.VTimeInSec) bool {
	madeProgress := false

	for i := 0; i < 32; i++ {
		madeProgress = madeProgress || buffer.parseFromTop(now)
	}

	//madeProgress = madeProgress || buffer.pipeline.Tick(now)
	//
	//for i := 0; i < 4; i++ {
	//	madeProgress = madeProgress || buffer.check()
	//}

	for i := 0; i < 32; i++ {
		madeProgress = madeProgress || buffer.sendToTop(now)
	}

	return madeProgress
}

func (buffer *TickingBuffer) parseFromTop(now akita.VTimeInSec) bool {
	item := buffer.ToTop.Retrieve(now)
	if item == nil {
		return false
	}

	switch msg := item.(type) {
	case *transaction:
		buffer.insert(msg)
	case *mem.DataReadyRsp:
		buffer.remove(msg)
	default:
		panic("unknown message type in TickingBuffer")
	}

	return true
}

func (buffer *TickingBuffer) push(
	now akita.VTimeInSec,
	msg akita.Msg,
) bool {
	if buffer.pipeline.CanAccept() {
		pipelineItem := &PipelineItem{
			taskID: akita.GetIDGenerator().Generate(),
			msg:    msg,
		}

		buffer.pipeline.Accept(now, pipelineItem)

		buffer.ToTop.Retrieve(now)

		return true
	}

	return false
}

func (buffer *TickingBuffer) check() bool {
	item := buffer.lookupBuffer.Peek()
	if item == nil {
		return false
	}

	pipelineItem := item.(*PipelineItem)

	switch msg := pipelineItem.msg.(type) {
	case *transaction:
		buffer.insert(msg)
	case *mem.DataReadyRsp:
		buffer.remove(msg)
	default:
		panic("unknown message type in TickingBuffer")
	}

	buffer.lookupBuffer.Pop()

	return true
}

func (buffer *TickingBuffer) insert(
	trans *transaction,
) {
	if _, exists := buffer.storage[trans.msgID]; exists {
		panic("duplicated transaction in buffer")
	}

	buffer.storage[trans.msgID] = trans
}

func (buffer *TickingBuffer) remove(
	rsp *mem.DataReadyRsp,
) {
	if _, exists := buffer.storage[rsp.RespondTo]; !exists {
		log.Panicf("transaction %s not found in buffer\n", rsp.RespondTo)
	}

	trans := buffer.storage[rsp.RespondTo]
	delete(buffer.storage, rsp.RespondTo)

	buffer.pendingIssues = append(buffer.pendingIssues, trans)
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

	buffer.lookupBuffer = util.NewBuffer(8)
	pipelineBuilder := pipelining.MakeBuilder().
		WithPipelineWidth(4).
		WithNumStage(1).
		WithCyclePerStage(1).
		WithPostPipelineBuffer(buffer.lookupBuffer)
	buffer.pipeline = pipelineBuilder.Build(buffer.Name() + "_pipeline")

	buffer.storage = make(map[string]*transaction)

	return buffer
}
