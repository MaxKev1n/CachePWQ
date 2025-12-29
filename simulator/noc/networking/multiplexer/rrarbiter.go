package multiplexer

import (
	"gitlab.com/akita/akita"
	"gitlab.com/akita/noc"
	"gitlab.com/akita/noc/networking/internal/arbitration"
	"gitlab.com/akita/util"
)

// NewRRArbiter creates a new Round-Robin arbiter.
func NewRRArbiter() arbitration.Arbiter {
	return &rrArbiter{}
}

type rrArbiter struct {
	buffers    []util.Buffer
	nextPortID int
}

func (a *rrArbiter) AddBuffer(buf util.Buffer) {
	a.buffers = append(a.buffers, buf)
}

func (a *rrArbiter) Arbitrate(now akita.VTimeInSec) []util.Buffer {
	if len(a.buffers) == 0 {
		panic("No buffer added to the arbiter")
	}

	selectedPort := make([]util.Buffer, 0)
	occupiedOutputPort := make(map[util.Buffer]bool)

	for i := 0; i < len(a.buffers); i++ {
		currPortID := (a.nextPortID + i) % len(a.buffers)
		buf := a.buffers[currPortID]
		item := buf.Peek()
		if item == nil {
			continue
		}

		flit := item.(*noc.Flit)
		// IMPORTANT: Check if the downstream can accept the flit
		if flit.OutputBuf == nil || !flit.OutputBuf.CanPush() {
			continue
		}

		if _, ok := occupiedOutputPort[flit.OutputBuf]; ok {
			continue
		}

		selectedPort = append(selectedPort, buf)
		occupiedOutputPort[flit.OutputBuf] = true

		a.nextPortID = (currPortID + 1) % len(a.buffers)
	}

	return selectedPort
}
