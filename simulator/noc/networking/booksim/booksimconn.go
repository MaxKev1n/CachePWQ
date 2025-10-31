package noc

import (
	"fmt"
	// "fmt"
	"strings"

	"gitlab.com/akita/akita"
)

type BookSimConnectionEnd struct {
	port    akita.Port
	buf     []akita.Msg
	bufSize int
	busy    bool
}

// BookSimConnection connects two components without latency
type BookSimConnection struct {
	*akita.TickingComponent

	engine     akita.Engine
	nextPortID int

	devicePort akita.Port
	nocPort    akita.Port
	deviceEnd  *BookSimConnectionEnd
	nocEnd     *BookSimConnectionEnd

	ports []akita.Port
	ends  []*BookSimConnectionEnd
}

// PlugIn marks the port connects to this BookSimConnection.
func (c *BookSimConnection) PlugIn(port akita.Port, sourceSideBufSize int) {
	c.Lock()
	defer c.Unlock()

	end := &BookSimConnectionEnd{}
	end.port = port
	end.bufSize = sourceSideBufSize
	c.ends = append(c.ends, end)
	c.ports = append(c.ports, port)

	if strings.Contains(port.Name(), "NocPort") {
		if c.nocPort != nil || c.nocEnd != nil {
			panic("Noc already connected")
		}
		c.nocPort = port
		c.nocEnd = end
	} else {
		if c.devicePort != nil || c.deviceEnd != nil {
			panic("Device already connected")
		}
		c.devicePort = port
		c.deviceEnd = end
	}

	port.SetConnection(c)
}

// Unplug marks the port no longer connects to this BookSimConnection.
func (c *BookSimConnection) Unplug(port akita.Port) {
	panic("not implemented")
}

// NotifyAvailable is called by a port to notify that the connection can
// deliver to the port again.
func (c *BookSimConnection) NotifyAvailable(now akita.VTimeInSec, port akita.Port) {
	c.TickNow(now)
}

// Send of a BookSimConnection schedules a DeliveryEvent immediately
func (c *BookSimConnection) Send(msg akita.Msg) *akita.SendError {
	c.Lock()
	defer c.Unlock()

	c.msgMustBeValid(msg)

	var srcEnd *BookSimConnectionEnd

	src := msg.Meta().Src
	dst := msg.Meta().Dst

	if src == c.devicePort {
		srcEnd = c.deviceEnd
	} else if dst == c.devicePort {
		srcEnd = c.nocEnd
	} else {
		panic(fmt.Sprintf("src port %s is not connected to this connection", src.Name()))
	}

	if len(srcEnd.buf) >= srcEnd.bufSize {
		srcEnd.busy = true
		return akita.NewSendError()
	}

	srcEnd.buf = append(srcEnd.buf, msg)

	c.TickNow(msg.Meta().SendTime)

	return nil
}

func (c *BookSimConnection) msgMustBeValid(msg akita.Msg) {
	c.portMustNotBeNil(msg.Meta().Src)
	c.portMustNotBeNil(msg.Meta().Dst)
	c.srcDstMustNotBeTheSame(msg)
}

func (c *BookSimConnection) portMustNotBeNil(port akita.Port) {
	if port == nil {
		panic("src or dst is not given")
	}
}

func (c *BookSimConnection) srcDstMustNotBeTheSame(msg akita.Msg) {
	if msg.Meta().Src == msg.Meta().Dst && !strings.Contains(msg.Meta().Src.Name(), "RTU") {
		panic("sending back to src")
	}
}

func (c *BookSimConnection) Tick(now akita.VTimeInSec) bool {
	madeProgress := false
	// for {
	// madeProgress := false
	for i := 0; i < len(c.ports); i++ {
		portID := (i + c.nextPortID) % len(c.ports)
		end := c.ends[portID]
		madeProgress = c.forwardMany(end, now) || madeProgress
	}
	// if !madeProgress {
	// break
	// }
	// }
	c.nextPortID = (c.nextPortID + 1) % len(c.ports)
	// return true
	return madeProgress
}

func (c *BookSimConnection) forwardMany(
	end *BookSimConnectionEnd,
	now akita.VTimeInSec,
) bool {
	madeProgress := false
	for {
		if len(end.buf) == 0 {
			break
		}

		head := end.buf[0]
		head.Meta().RecvTime = now

		var dst akita.Port

		port := end.port
		if port == c.devicePort {
			dst = c.nocPort
		} else if port == c.nocPort {
			dst = c.devicePort
		} else {
			panic("unknown port")
		}

		err := dst.Recv(head)
		if err != nil {
			break
		}

		madeProgress = true
		end.buf = end.buf[1:]

		if end.busy {
			end.port.NotifyAvailable(now)
			end.busy = false
		}
	}

	return madeProgress
}

func (c *BookSimConnection) forwardOne(
	end *BookSimConnectionEnd,
	now akita.VTimeInSec,
) bool {
	panic("not implemented")
}

// NewBookSimConnection creates a new BookSimConnection object
func NewBookSimConnection(
	name string,
	engine akita.Engine,
	freq akita.Freq,
) *BookSimConnection {
	c := new(BookSimConnection)
	c.TickingComponent = akita.NewSecondaryTickingComponent(name, engine, freq, c)
	return c
}
