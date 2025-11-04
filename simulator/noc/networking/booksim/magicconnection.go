package noc

import (
	"fmt"
	// "fmt"
	"strings"

	"gitlab.com/akita/akita"
)

type MagicConnectionEnd struct {
	port    akita.Port
	buf     []akita.Msg
	bufSize int
	busy    bool
}

// MagicConnection connects two components without latency
type MagicConnection struct {
	*akita.TickingComponent

	engine     akita.Engine
	nextPortID int

	ports []akita.Port
	ends  map[akita.Port]*MagicConnectionEnd
}

// PlugIn marks the port connects to this MagicConnection.
func (c *MagicConnection) PlugIn(port akita.Port, sourceSideBufSize int) {
	c.Lock()
	defer c.Unlock()

	c.ports = append(c.ports, port)
	end := &MagicConnectionEnd{}
	end.port = port
	end.bufSize = sourceSideBufSize
	c.ends[port] = end

	port.SetConnection(c)
}

// Unplug marks the port no longer connects to this MagicConnection.
func (c *MagicConnection) Unplug(port akita.Port) {
	panic("not implemented")
}

// NotifyAvailable is called by a port to notify that the connection can
// deliver to the port again.
func (c *MagicConnection) NotifyAvailable(now akita.VTimeInSec, port akita.Port) {
	c.TickNow(now)
}

// Send of a MagicConnection schedules a DeliveryEvent immediately
func (c *MagicConnection) Send(msg akita.Msg) *akita.SendError {
	c.Lock()
	defer c.Unlock()

	c.msgMustBeValid(msg)

	if _, ok := c.ends[msg.Meta().Src]; !ok {
		panic(fmt.Sprintf("port %s not plugged in MagicConnection %s (%s)",
			msg.Meta().Src.Name(),
			c.Name(),
			msg.Meta().Dst.Name()))
	}
	srcEnd := c.ends[msg.Meta().Src]

	if len(srcEnd.buf) >= srcEnd.bufSize {
		srcEnd.busy = true
		return akita.NewSendError()
	}

	srcEnd.buf = append(srcEnd.buf, msg)

	c.TickNow(msg.Meta().SendTime)

	return nil
}

func (c *MagicConnection) msgMustBeValid(msg akita.Msg) {
	c.portMustNotBeNil(msg.Meta().Src)
	c.portMustNotBeNil(msg.Meta().Dst)
	c.srcDstMustNotBeTheSame(msg)
}

func (c *MagicConnection) portMustNotBeNil(port akita.Port) {
	if port == nil {
		panic("src or dst is not given")
	}
}

func (c *MagicConnection) srcDstMustNotBeTheSame(msg akita.Msg) {
	if msg.Meta().Src == msg.Meta().Dst && !strings.Contains(msg.Meta().Src.Name(), "RTU") {
		panic("sending back to src")
	}
}

func (c *MagicConnection) Tick(now akita.VTimeInSec) bool {
	madeProgress := false
	// for {
	// madeProgress := false
	for i := 0; i < len(c.ports); i++ {
		portID := (i + c.nextPortID) % len(c.ports)
		port := c.ports[portID]
		end := c.ends[port]
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

func (c *MagicConnection) forwardMany(
	end *MagicConnectionEnd,
	now akita.VTimeInSec,
) bool {
	madeProgress := false
	for {
		if len(end.buf) == 0 {
			break
		}

		head := end.buf[0]
		head.Meta().RecvTime = now

		err := head.Meta().Dst.Recv(head)
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

func (c *MagicConnection) forwardOne(
	end *MagicConnectionEnd,
	now akita.VTimeInSec,
) bool {
	panic("not implemented")
}

// NewMagicConnection creates a new MagicConnection object
func NewMagicConnection(
	name string,
	engine akita.Engine,
	freq akita.Freq,
) *MagicConnection {
	c := new(MagicConnection)
	c.TickingComponent = akita.NewSecondaryTickingComponent(name, engine, freq, c)
	c.ends = make(map[akita.Port]*MagicConnectionEnd)
	return c
}
