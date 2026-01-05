package writeback

import (
	"log"
	"math/rand"

	"github.com/tebeka/atexit"
	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
)

var AgentImpl *Agent

type Agent struct {
	*akita.TickingComponent

	MsgsToSend []akita.Msg

	InflightReqs map[string]struct{}

	startTime akita.VTimeInSec
	endTime   akita.VTimeInSec

	numReqs uint64
}

func NewAgent(
	engine akita.Engine,
	freq akita.Freq,
	srcPorts []akita.Port,
	dstPorts []akita.Port,
	numReqs uint64,
) *Agent {
	a := &Agent{}

	a.TickingComponent = akita.NewTickingComponent(
		"WritebackAgent",
		engine,
		freq,
		a,
	)
	a.InflightReqs = make(map[string]struct{})
	a.numReqs = numReqs

	for i := uint64(0); i < a.numReqs; i++ {
		srcPortID := rand.Intn(len(srcPorts))
		dstPortID := rand.Intn(len(dstPorts))

		addr := 0x84001000 + dstPortID*0x1000

		msg := mem.ReadReqBuilder{}.
			WithSrc(srcPorts[srcPortID]).
			WithDst(dstPorts[dstPortID]).
			WithAddress(uint64(addr)).
			WithByteSize(64).
			Build()
		a.MsgsToSend = append(a.MsgsToSend, msg)

		a.InflightReqs[msg.Meta().ID] = struct{}{}
	}

	return a
}

// Tick tries to receive requests and send requests out.
func (a *Agent) Tick(now akita.VTimeInSec) bool {
	madeProgress := false
	for i := 0; i < 4096; i++ {
		madeProgress = a.send(now) || madeProgress
	}

	return madeProgress
}

func (a *Agent) send(now akita.VTimeInSec) bool {
	if len(a.MsgsToSend) == 0 {
		return false
	}

	msg := a.MsgsToSend[0]
	msg.Meta().SendTime = now
	err := msg.Meta().Src.Send(msg)
	if err == nil {
		a.MsgsToSend = a.MsgsToSend[1:]

		if a.startTime == 0 {
			a.startTime = now
		}

		return true
	}

	return false
}

func (a *Agent) Recv(ID string) {
	if _, ok := a.InflightReqs[ID]; ok {
		delete(a.InflightReqs, ID)

		if len(a.InflightReqs) == 0 {
			a.endTime = a.Engine.CurrentTime()
			elapsed := a.endTime - a.startTime
			log.Printf("All writeback requests are done.\n")
			log.Printf("Elapsed time: %.12f seconds\n", float64(elapsed))
			log.Printf("Bandwidth: %f GB/s\n",
				float64(a.numReqs)*64/1e9/float64(elapsed))

			atexit.Exit(0)
		}
	}
}
