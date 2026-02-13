package multiplexer

import (
	"fmt"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/noc"
	"gitlab.com/akita/noc/networking/internal/arbitration"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/pipelining"
)

// Switch is an Akita component that can forward request to destination.
type Switch struct {
	*akita.TickingComponent

	ports                []akita.Port
	portToComplexMapping map[akita.Port]portComplex
	routingTable         RoutingTable
	arbiter              arbitration.Arbiter
	numReqPerCycle       int
	bufferSizeInNumFlit  int
}

// addPort adds a new port on the switch.
func (s *Switch) addPort(complex portComplex) {
	s.ports = append(s.ports, complex.localPort)
	s.portToComplexMapping[complex.localPort] = complex
	s.arbiter.AddBuffer(complex.forwardBuffer)
}

// GetRoutingTable returns the routine table used by the switch.
func (s *Switch) GetRoutingTable() RoutingTable {
	return s.routingTable
}

// Tick update the Switch's state.
func (s *Switch) Tick(now akita.VTimeInSec) bool {
	madeProgress := false

	for i := 0; i < s.numReqPerCycle; i++ {
		madeProgress = s.sendOut(now) || madeProgress
		madeProgress = s.forward(now) || madeProgress
		madeProgress = s.route(now) || madeProgress
	}

	madeProgress = s.movePipeline(now) || madeProgress

	for i := 0; i < s.numReqPerCycle; i++ {
		madeProgress = s.startProcessing(now) || madeProgress
	}

	return madeProgress
}

func (s *Switch) startProcessing(now akita.VTimeInSec) (madeProgress bool) {
	for _, port := range s.ports {
		item := port.Peek()
		if item == nil {
			continue
		}

		complex := s.portToComplexMapping[port]
		if !complex.pipeline.CanAccept() {
			continue
		}

		pipelineItem := flitPipelineItem{
			taskID: akita.GetIDGenerator().Generate(),
			flit:   item.(*noc.Flit),
		}
		complex.pipeline.Accept(now, pipelineItem)
		port.Retrieve(now)
		madeProgress = true
	}

	return madeProgress
}

func (s *Switch) movePipeline(now akita.VTimeInSec) (madeProgress bool) {
	for _, port := range s.ports {
		complex := s.portToComplexMapping[port]
		madeProgress = complex.pipeline.Tick(now) || madeProgress
	}

	return madeProgress
}

// assigns a flit the outputbuffer to which it is to be routed
func (s *Switch) route(now akita.VTimeInSec) (madeProgress bool) {
	for _, port := range s.ports {
		portComplex := s.portToComplexMapping[port]
		routeBuf := portComplex.routeBuffer
		forwardBuf := portComplex.forwardBuffer

		item := routeBuf.Peek()
		if item == nil {
			continue
		}

		if !forwardBuf.CanPush() {
			continue
		}

		pipelineItem := item.(flitPipelineItem)
		flit := pipelineItem.flit
		s.assignFlitOutputBuf(flit)
		routeBuf.Pop()
		forwardBuf.Push(flit)
		madeProgress = true
	}

	return madeProgress
}

// The order in which switches tick might also be important
func (s *Switch) forward(now akita.VTimeInSec) (madeProgress bool) {
	inputBuffers := s.arbiter.Arbitrate(now)

	for _, buf := range inputBuffers {
		item := buf.Peek()
		if item == nil {
			continue
		}
		flit := item.(*noc.Flit)

		if !flit.OutputBuf.CanPush() {
			continue
		}

		flit.OutputBuf.Push(flit)
		buf.Pop()
		madeProgress = true
	}

	return madeProgress
}

func (s *Switch) sendOut(now akita.VTimeInSec) (madeProgress bool) {
	for _, port := range s.ports {
		complex := s.portToComplexMapping[port]
		sendOutBuf := complex.sendOutBuffer

		item := sendOutBuf.Peek()
		if item == nil {
			continue
		}

		flit := item.(*noc.Flit)
		flit.Meta().Src = complex.localPort
		flit.Meta().Dst = complex.remotePort
		flit.Meta().SendTime = now
		err := complex.localPort.Send(flit)
		if err == nil {
			sendOutBuf.Pop()
			madeProgress = true
		}
	}

	return madeProgress
}

func (s *Switch) assignFlitOutputBuf(f *noc.Flit) {
	outPort := s.routingTable.Find(f.Msg.Meta().Dst)

	if complex, ok := s.portToComplexMapping[outPort]; ok {
		f.OutputBuf = complex.sendOutBuffer
	} else {
		panic(fmt.Sprintf("No port complex found for port %s", outPort.Name()))
	}
}

func (s *Switch) setFlitNextHopDst(f *noc.Flit) {
	f.Src = f.Dst
	f.Dst = s.portToComplexMapping[f.Src].remotePort
}

func (s *Switch) createPortComplex(
	latency int,
	local, remote akita.Port,
) portComplex {
	sendOutBuf := util.NewBuffer(2 * s.numReqPerCycle)
	forwardBuf := util.NewBuffer(2 * s.numReqPerCycle)
	routeBuf := util.NewBuffer(2 * s.numReqPerCycle)
	pipeline := pipelining.NewPipeline(
		local.Name()+"pipeline", latency, 1, routeBuf)

	pc := portComplex{
		localPort:     local,
		remotePort:    remote,
		pipeline:      pipeline,
		routeBuffer:   routeBuf,
		forwardBuffer: forwardBuf,
		sendOutBuffer: sendOutBuf,
	}

	return pc
}

// ConnectEndPointToSwitch connects an EndPoint to a Switch.
func (s *Switch) ConnectEndPointToSwitch(
	ep *EndPoint,
	latency int,
	freq akita.Freq,
) (switchPort akita.Port) {
	port := akita.NewLimitNumMsgPort(s, s.bufferSizeInNumFlit,
		fmt.Sprintf("%s.Port%d", s.Name(), len(s.ports)))
	conn := akita.NewDirectConnection(
		fmt.Sprintf("%s-%s", ep.NetworkPort.Name(), port.Name()),
		s.Engine, freq)
	conn.PlugIn(port, 2*s.numReqPerCycle)
	conn.PlugIn(ep.NetworkPort, 2*s.numReqPerCycle)

	s.addPort(s.createPortComplex(latency, port, ep.NetworkPort))

	ep.DefaultSwitchDst = port

	return port
}

// SwitchBuilder can build switches
type SwitchBuilder struct {
	engine              akita.Engine
	freq                akita.Freq
	routingTable        RoutingTable
	arbiter             arbitration.Arbiter
	numReqPerCycle      int
	bufferSizeInNumFlit int
}

// WithEngine sets the engine that the switch to build uses.
func (b SwitchBuilder) WithEngine(engine akita.Engine) SwitchBuilder {
	b.engine = engine
	return b
}

// WithFreq sets the frequency that the switch to build works at.
func (b SwitchBuilder) WithFreq(freq akita.Freq) SwitchBuilder {
	b.freq = freq
	return b
}

// WithArbiter sets the arbiter to be used by the swtich to build.
func (b SwitchBuilder) WithArbiter(arbiter arbitration.Arbiter) SwitchBuilder {
	b.arbiter = arbiter
	return b
}

// WithRoutingTable sets the routing table to be used by the switch to build.
func (b SwitchBuilder) WithRoutingTable(rt RoutingTable) SwitchBuilder {
	b.routingTable = rt
	return b
}

func (b SwitchBuilder) WithNumReqPerCycle(numReqPerCycle int) SwitchBuilder {
	b.numReqPerCycle = numReqPerCycle
	return b
}

// WithBufferSizeInNumFlit sets the buffer size at each port of the switch to be built.
func (b SwitchBuilder) WithBufferSizeInNumFlit(
	size int,
) SwitchBuilder {
	b.bufferSizeInNumFlit = size
	return b
}

// Build creates a new switch
func (b SwitchBuilder) Build(name string) *Switch {
	b.engineMustBeGiven()
	b.freqMustNotBeZero()
	b.routingTableMustBeGiven()
	b.arbiterMustBeGiven()

	s := &Switch{}
	s.TickingComponent = akita.NewTickingComponent(name, b.engine, b.freq, s)
	s.routingTable = b.routingTable
	s.arbiter = b.arbiter
	s.portToComplexMapping = make(map[akita.Port]portComplex)
	s.numReqPerCycle = b.numReqPerCycle
	s.bufferSizeInNumFlit = b.bufferSizeInNumFlit
	return s
}

func (b SwitchBuilder) engineMustBeGiven() {
	if b.engine == nil {
		panic("engine of switch is not given")
	}
}

func (b SwitchBuilder) freqMustNotBeZero() {
	if b.freq == 0 {
		panic("switch frequency cannot be 0")
	}
}

func (b SwitchBuilder) routingTableMustBeGiven() {
	if b.routingTable == nil {
		panic("switch requires a routing table to operate")
	}
}

func (b SwitchBuilder) arbiterMustBeGiven() {
	if b.arbiter == nil {
		panic("switch requires an arbiter to operate")
	}
}

// ConnectSwitches connect two switches together.
func ConnectSwitches(
	a, b *Switch,
	latency int,
	numReqPerCycle int,
	engine akita.Engine,
	freq akita.Freq,
) (portOnA, portOnB akita.Port) {
	portA := akita.NewLimitNumMsgPort(a, a.bufferSizeInNumFlit,
		fmt.Sprintf("%s.Port%d", a.Name(), len(a.ports)))
	portB := akita.NewLimitNumMsgPort(b, b.bufferSizeInNumFlit,
		fmt.Sprintf("%s.Port%d", b.Name(), len(b.ports)))
	conn := akita.NewDirectConnection(
		fmt.Sprintf("%s-%s", portA.Name(), portB.Name()),
		engine, freq)
	conn.PlugIn(portA, 2*numReqPerCycle)
	conn.PlugIn(portB, 2*numReqPerCycle)

	a.addPort(a.createPortComplex(latency, portA, portB))
	b.addPort(b.createPortComplex(latency, portB, portA))

	return portA, portB
}
