package multiplexer

import (
	"fmt"
	"math"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/noc"
)

// HybridEndPoint is an akita component that deligates sending and receiving actions
// of a few ports.
type HybridEndPoint struct {
	*akita.TickingComponent

	DevicePorts      []akita.Port
	NoCPort          akita.Port
	NetworkPort      akita.Port
	DefaultSwitchDst akita.Port

	flitByteSize             int
	encodingOverhead         float64
	msgOutBuf                []akita.Msg
	msgOutBufSize            int
	flitsToSend              []*noc.Flit
	flitsToAssemble          []*noc.Flit
	flitAssemblingBufferSize int
	assemblingMsg            akita.Msg
	numFlitReqired           int
	numFlitArrived           int
	numReqPerCycle           int

	devicePorts map[akita.Port]struct{}
}

// Send sends a message.
func (ep *HybridEndPoint) Send(msg akita.Msg) *akita.SendError {
	ep.Lock()
	defer ep.Unlock()

	if len(ep.msgOutBuf) >= ep.msgOutBufSize {
		return &akita.SendError{}
	}

	ep.msgOutBuf = append(ep.msgOutBuf, msg)

	ep.TickLater(msg.Meta().SendTime)

	return nil
}

// PlugIn connects a port to the endpoint.
func (ep *HybridEndPoint) PlugIn(port akita.Port, srcBufCap int) {
	if _, exists := ep.devicePorts[port]; exists {
		panic(fmt.Sprintf("HybridEndPoint %s: port %s is already plugged in",
			ep.Name(), port.Name()))
	}

	port.SetConnection(ep)
	ep.DevicePorts = append(ep.DevicePorts, port)
	ep.devicePorts[port] = struct{}{}
	ep.msgOutBufSize = srcBufCap
}

// PlugInNoCPort connects a port to the endpoint.
func (ep *HybridEndPoint) PlugInNoCPort(port akita.Port, srcBufCap int) {
	if ep.NoCPort != nil {
		panic(fmt.Sprintf("HybridEndPoint %s: only one device port is allowed", ep.Name()))
	}

	port.SetConnection(ep)
	ep.NoCPort = port
	ep.msgOutBufSize = srcBufCap
}

// NotifyAvailable triggers the endpoint to continue to tick.
func (ep *HybridEndPoint) NotifyAvailable(now akita.VTimeInSec, port akita.Port) {
	ep.TickLater(now)
}

// Unplug removes the association of a port and an endpoint.
func (ep *HybridEndPoint) Unplug(port akita.Port) {
	panic("not implemented")
}

// Tick update the endpoint state.
func (ep *HybridEndPoint) Tick(now akita.VTimeInSec) bool {
	ep.Lock()
	defer ep.Unlock()

	madeProgress := false

	for i := 0; i < ep.numReqPerCycle; i++ {
		madeProgress = ep.sendFlitOut(now) || madeProgress
		madeProgress = ep.prepareFlits(now) || madeProgress
		madeProgress = ep.tryDeliver(now) || madeProgress
		madeProgress = ep.assemble(now) || madeProgress
		madeProgress = ep.recv(now) || madeProgress
	}

	return madeProgress
}

func (ep *HybridEndPoint) sendFlitOut(now akita.VTimeInSec) bool {
	if len(ep.flitsToSend) == 0 {
		return false
	}

	ep.flitsToSend[0].SendTime = now
	err := ep.NetworkPort.Send(ep.flitsToSend[0])
	if err == nil {
		ep.flitsToSend = ep.flitsToSend[1:]
		if len(ep.flitsToSend) == 0 {
			ep.NoCPort.NotifyAvailable(now)

			for _, p := range ep.DevicePorts {
				p.NotifyAvailable(now)
			}
		}
		return true
	}
	return false
}

func (ep *HybridEndPoint) prepareFlits(now akita.VTimeInSec) bool {
	if len(ep.flitsToSend) > 0 {
		return false
	}

	if len(ep.msgOutBuf) == 0 {
		return false
	}

	msg := ep.msgOutBuf[0]
	ep.msgOutBuf = ep.msgOutBuf[1:]
	ep.flitsToSend = ep.msgToFlits(msg)

	// log.Printf("%.12f, EP %s, msg %s start sending\n",
	// 	msg.Meta().SendTime, ep.Name(), msg.Meta().ID)
	return true
}

func (ep *HybridEndPoint) recv(now akita.VTimeInSec) bool {
	recved := ep.NetworkPort.Peek()
	if recved == nil {
		return false
	}

	if len(ep.flitsToAssemble) >= ep.flitAssemblingBufferSize {
		return false
		// log.Printf("warning: flit buffer overflow, " +
		// "double buffer size to prevent deadlock")
		// ep.flitAssemblingBufferSize *= 2
	}

	flit := recved.(*noc.Flit)
	ep.flitsToAssemble = append(ep.flitsToAssemble, flit)

	// if len(ep.flitsToAssemble) >= ep.flitAssemblingBufferSize {
	// 	log.Printf("warning: flit buffer overflow, " +
	// 		"double buffer size to prevent deadlock")
	// 	ep.flitAssemblingBufferSize *= 2
	// }

	ep.NetworkPort.Retrieve(now)
	// log.Printf("%.12f, EP %s, received flit %d(%d), msg %s\n",
	// 	now, ep.Name(), flit.SeqID, flit.NumFlitInMsg, flit.Msg.Meta().ID)

	return true
}

func (ep *HybridEndPoint) assemble(now akita.VTimeInSec) bool {
	madeProgress := false
	newFlits := make([]*noc.Flit, 0)
	for _, f := range ep.flitsToAssemble {
		if ep.assemblingMsg == nil {
			ep.assemblingMsg = f.Msg
			ep.numFlitArrived = 1
			ep.numFlitReqired = f.NumFlitInMsg
			madeProgress = true
		} else if ep.assemblingMsg != nil && f.Msg == ep.assemblingMsg {
			ep.numFlitArrived++
			madeProgress = true
		} else {
			newFlits = append(newFlits, f)
		}
	}
	ep.flitsToAssemble = newFlits
	return madeProgress
}

func (ep *HybridEndPoint) tryDeliver(now akita.VTimeInSec) bool {
	if ep.assemblingMsg == nil {
		return false
	}

	if ep.numFlitArrived < ep.numFlitReqired {
		return false
	}

	ep.assemblingMsg.Meta().RecvTime = now

	dst := ep.NoCPort
	if _, exists := ep.devicePorts[ep.assemblingMsg.Meta().Dst]; exists {
		dst = ep.assemblingMsg.Meta().Dst
	}

	err := dst.Recv(ep.assemblingMsg)
	if err == nil {
		// log.Printf("%.12f, EP %s, msg %s assembled and deliverd\n",
		// 	now, ep.Name(), ep.assemblingMsg.Meta().ID)
		ep.assemblingMsg = nil
		ep.numFlitReqired = 0
		ep.numFlitArrived = 0
		return true
	}
	return false
}

func (ep *HybridEndPoint) msgToFlits(msg akita.Msg) []*noc.Flit {
	numFlit := 1
	if msg.Meta().TrafficBytes > 0 {
		trafficByte := msg.Meta().TrafficBytes
		trafficByte += int(math.Ceil(
			float64(trafficByte) * ep.encodingOverhead))
		numFlit = (trafficByte-1)/ep.flitByteSize + 1
	}

	flits := make([]*noc.Flit, numFlit)
	for i := 0; i < numFlit; i++ {
		flits[i] = noc.FlitBuilder{}.
			WithSrc(ep.NetworkPort).
			WithDst(ep.DefaultSwitchDst).
			WithSeqID(i).
			WithNumFlitInMsg(numFlit).
			WithMsg(msg).
			Build()
	}
	return flits
}

// HybridEndPointBuilder can build End Points.
type HybridEndPointBuilder struct {
	engine                   akita.Engine
	freq                     akita.Freq
	flitByteSize             int
	encodingOverhead         float64
	flitAssemblingBufferSize int
	networkPortBufferSize    int
	devicePorts              []akita.Port
	numReqPerCycle           int
}

// MakeHybridEndPointBuilder creates a new HybridEndPointBuilder with default
// configureations.
func MakeHybridEndPointBuilder() HybridEndPointBuilder {
	return HybridEndPointBuilder{
		flitByteSize:             32,
		flitAssemblingBufferSize: 64,
		networkPortBufferSize:    4,
		freq:                     1 * akita.GHz,
		numReqPerCycle:           1,
	}
}

// WithEngine sets the engine of the End Point to build.
func (b HybridEndPointBuilder) WithEngine(e akita.Engine) HybridEndPointBuilder {
	b.engine = e
	return b
}

// WithFreq sets the frequency of the End Point to built.
func (b HybridEndPointBuilder) WithFreq(freq akita.Freq) HybridEndPointBuilder {
	b.freq = freq
	return b
}

// WithFlitByteSize sets the flit byte size that the End Point supports.
func (b HybridEndPointBuilder) WithFlitByteSize(n int) HybridEndPointBuilder {
	b.flitByteSize = n
	return b
}

// WithFreq sets the frequency of the End Point to built.
func (b HybridEndPointBuilder) WithNumReqPerCycle(numReqPerCycle int) HybridEndPointBuilder {
	b.numReqPerCycle = numReqPerCycle
	return b
}

// WithEncodingOverhead sets the encoding overhead.
func (b HybridEndPointBuilder) WithEncodingOverhead(o float64) HybridEndPointBuilder {
	b.encodingOverhead = o
	return b
}

// WithNetworkPortBufferSize sets the network port buffer size of the end point.
func (b HybridEndPointBuilder) WithNetworkPortBufferSize(n int) HybridEndPointBuilder {
	b.networkPortBufferSize = n
	return b
}

// WithFlitAssemblingBufferSize sets the flit assembling buffer size.
func (b HybridEndPointBuilder) WithFlitAssemblingBufferSize(n int) HybridEndPointBuilder {
	b.flitAssemblingBufferSize = n
	return b
}

// WithDevicePorts sets a list of ports that communicate directly through the
// End Point.
func (b HybridEndPointBuilder) WithDevicePorts(ports []akita.Port) HybridEndPointBuilder {
	b.devicePorts = ports
	return b
}

// Build creates a new End Point.
func (b HybridEndPointBuilder) Build(name string) *HybridEndPoint {
	b.engineMustBeGiven()
	b.freqMustBeGiven()
	b.flitByteSizeMustBeGiven()

	ep := &HybridEndPoint{}
	ep.TickingComponent = akita.NewTickingComponent(
		name, b.engine, b.freq, ep)
	ep.flitByteSize = b.flitByteSize
	ep.flitAssemblingBufferSize = b.flitAssemblingBufferSize
	ep.numReqPerCycle = b.numReqPerCycle
	ep.NetworkPort = akita.NewLimitNumMsgPort(
		ep, b.networkPortBufferSize,
		fmt.Sprintf("%s.NetworkPort", ep.Name()))
	ep.devicePorts = make(map[akita.Port]struct{})

	for _, dp := range b.devicePorts {
		ep.PlugIn(dp, 2*b.numReqPerCycle)
	}

	return ep
}

func (b HybridEndPointBuilder) engineMustBeGiven() {
	if b.engine == nil {
		panic("engine is not given")
	}
}

func (b HybridEndPointBuilder) freqMustBeGiven() {
	if b.freq == 0 {
		panic("freq must be given")
	}
}

func (b HybridEndPointBuilder) flitByteSizeMustBeGiven() {
	if b.flitByteSize == 0 {
		panic("flit byte size must be given")
	}
}
