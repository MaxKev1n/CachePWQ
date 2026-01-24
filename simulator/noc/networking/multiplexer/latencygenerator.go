package multiplexer

import (
	"fmt"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/pipelining"
)

type latencyPipeItem struct {
	taskID string
	msg    akita.Msg
}

func (item latencyPipeItem) TaskID() string {
	return item.taskID
}

type LatencyGenerator struct {
	*akita.TickingComponent

	pipelines     []pipelining.Pipeline
	lookupBuffers []util.Buffer

	inputQueue []akita.Msg

	width int
}

func NewLatencyGenerator(
	name string,
	engine akita.Engine,
	freq akita.Freq,
) *LatencyGenerator {
	generator := &LatencyGenerator{}

	generator.TickingComponent = akita.NewTickingComponent(
		name,
		engine,
		freq,
		generator,
	)

	return generator
}

func (g *LatencyGenerator) Build() {
	// Create 4 pipelines: 1 cycles, 10 cycles, 50 cycles, 100 cycles
	for _, latency := range []int{1, 10, 50, 100} {
		buffer := util.NewBuffer(2 * g.width)
		pipeline := pipelining.MakeBuilder().
			WithPipelineWidth(g.width).
			WithNumStage(latency).
			WithCyclePerStage(1).
			WithPostPipelineBuffer(buffer).
			Build(fmt.Sprintf("%s.Latency[%d]Pipeline", g.Name(), latency))

		g.pipelines = append(g.pipelines, pipeline)
		g.lookupBuffers = append(g.lookupBuffers, buffer)
	}
}

func (g *LatencyGenerator) Tick(now akita.VTimeInSec) bool {
	madeProgress := false

	madeProgress = g.Distribute(now) || madeProgress

	for _, pipeline := range g.pipelines {
		madeProgress = madeProgress || pipeline.Tick(now)
	}

	for _, buffer := range g.lookupBuffers {
		madeProgress = g.Forward(buffer, now) || madeProgress
	}

	return madeProgress
}

func (g *LatencyGenerator) Distribute(now akita.VTimeInSec) bool {
	madeProgress := false

	for {
		if len(g.inputQueue) == 0 {
			return madeProgress
		}

		pipeline := g.pipelines[0]

		if !pipeline.CanAccept() {
			return madeProgress
		}

		item := latencyPipeItem{
			taskID: akita.GetIDGenerator().Generate(),
			msg:    g.inputQueue[0],
		}

		pipeline.Accept(now, item)

		g.inputQueue = g.inputQueue[1:]

		madeProgress = true
	}
}

func (g *LatencyGenerator) Forward(
	buffer util.Buffer,
	now akita.VTimeInSec,
) bool {
	item := buffer.Peek()
	if item == nil {
		return false
	}

	msg := item.(latencyPipeItem).msg

	msg.Meta().RecvTime = now

	err := msg.Meta().Dst.Recv(msg)
	if err != nil {
		return false
	}

	buffer.Pop()

	return true
}

func (g *LatencyGenerator) Send(
	msg akita.Msg,
) *akita.SendError {
	g.inputQueue = append(g.inputQueue, msg)

	g.TickLater(msg.Meta().SendTime)

	return nil
}

func (g *LatencyGenerator) PlugIn(port akita.Port, srcBufCap int) {
	port.SetConnection(g)

	g.width = srcBufCap
}

func (g *LatencyGenerator) Unplug(port akita.Port) {
	panic("not implemented")
}

func (g *LatencyGenerator) NotifyAvailable(now akita.VTimeInSec, port akita.Port) {
	g.TickLater(now)
}
