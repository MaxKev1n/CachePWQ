package multiplexer

import (
	"fmt"
	"log"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/noc"
	"gitlab.com/akita/noc/networking/internal/arbitration"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/pipelining"
)

type flitPipelineItem struct {
	taskID string
	flit   *noc.Flit
}

func (f flitPipelineItem) TaskID() string {
	return f.taskID
}

// A portComplex is the infrastructure related to a port.
type portComplex struct {
	// localPort is the port that is equipped on the switch.
	localPort akita.Port

	// remotePort is the port that is connected to the localPort.
	remotePort akita.Port

	// Data arrived at the local port needs to be processed in a pipeline. There
	// is a processing pipeline for each local port.
	pipeline pipelining.Pipeline

	// The flits here are buffered after the pipeline and are waiting to be
	// assigned with an output buffer.
	routeBuffer util.Buffer

	// The flits here are buffered to wait to be forwarded to the output buffer.
	forwardBuffer util.Buffer

	// The flits here are waiting to be sent to the next hop.
	sendOutBuffer util.Buffer
}

type Multiplexer struct {
	*akita.TickingComponent

	LowSidePorts []akita.Port
	HighSidePort akita.Port

	portToComplexMapping map[akita.Port]portComplex

	uplinkArbiter arbitration.Arbiter

	numReqPerCycle      int
	switchLatency       int
	bufferSizeInNumFlit int

	RoutingTable RoutingTable
}

func (m *Multiplexer) Tick(now akita.VTimeInSec) bool {
	madeProgress := false

	for i := 0; i < m.numReqPerCycle; i++ {
		madeProgress = m.sendOut(now) || madeProgress

		madeProgress = m.forward(now) || madeProgress

		madeProgress = m.route(now) || madeProgress

		madeProgress = m.movePipeline(now) || madeProgress

		madeProgress = m.startProcessing(now) || madeProgress
	}

	return madeProgress
}

func (m *Multiplexer) startProcessing(now akita.VTimeInSec) bool {
	madeProgress := false
	allPorts := append(m.LowSidePorts, m.HighSidePort)

	for _, port := range allPorts {
		item := port.Peek()
		if item == nil {
			continue
		}

		complex := m.portToComplexMapping[port]
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

func (m *Multiplexer) route(now akita.VTimeInSec) bool {
	madeProgress := false
	allPorts := append(m.LowSidePorts, m.HighSidePort)

	for _, port := range allPorts {
		complex := m.portToComplexMapping[port]
		routeBuf := complex.routeBuffer
		forwardBuf := complex.forwardBuffer

		item := routeBuf.Peek()
		if item == nil {
			continue
		}

		if !forwardBuf.CanPush() {
			continue
		}

		flit := item.(flitPipelineItem).flit

		if port == m.HighSidePort {
			m.assignDownlinkOutputBuf(flit)
		} else {
			highSideComplex := m.portToComplexMapping[m.HighSidePort]
			flit.OutputBuf = highSideComplex.sendOutBuffer
		}

		routeBuf.Pop()
		forwardBuf.Push(flit)
		madeProgress = true
	}
	return madeProgress
}

func (m *Multiplexer) forward(now akita.VTimeInSec) bool {
	inputBuffers := m.uplinkArbiter.Arbitrate(now)
	madeProgress := false

	for _, buf := range inputBuffers {
		item := buf.Peek()
		if item == nil {
			continue
		}
		flit := item.(*noc.Flit)

		if flit.OutputBuf.CanPush() {
			flit.OutputBuf.Push(flit)
			buf.Pop()
			madeProgress = true
		}
	}

	// process high-side port separately
	highSideComplex := m.portToComplexMapping[m.HighSidePort]
	highSideBuf := highSideComplex.forwardBuffer
	item := highSideBuf.Peek()
	if item == nil {
		return madeProgress
	}

	flit := item.(*noc.Flit)
	if flit.OutputBuf.CanPush() {
		flit.OutputBuf.Push(flit)
		highSideBuf.Pop()
		madeProgress = true
	}

	return madeProgress
}

func (m *Multiplexer) movePipeline(now akita.VTimeInSec) bool {
	madeProgress := false
	for _, complex := range m.portToComplexMapping {
		madeProgress = complex.pipeline.Tick(now) || madeProgress
	}
	return madeProgress
}

func (m *Multiplexer) sendOut(now akita.VTimeInSec) bool {
	madeProgress := false
	for _, complex := range m.portToComplexMapping {
		item := complex.sendOutBuffer.Peek()
		if item == nil {
			continue
		}

		flit := item.(*noc.Flit)
		flit.Meta().Src = complex.localPort
		flit.Meta().Dst = complex.remotePort
		flit.Meta().SendTime = now

		err := complex.localPort.Send(flit)
		if err == nil {
			complex.sendOutBuffer.Pop()
			madeProgress = true
		}
	}
	return madeProgress
}

func (m *Multiplexer) assignDownlinkOutputBuf(f *noc.Flit) {
	finalDestination := f.Msg.Meta().Dst

	outPort, ok := m.RoutingTable.Find(finalDestination)
	if !ok {
		log.Printf("%s->%s\n", f.Msg.Meta().Src.Name(), finalDestination.Name())
		panic(fmt.Sprintf("No route found for destination %s in Multiplexer %s",
			finalDestination.Name(), m.Name()))
	}

	complex, ok := m.portToComplexMapping[outPort]
	if !ok {
		panic(fmt.Sprintf("Port %s is not registered in Multiplexer %s",
			outPort.Name(), m.Name()))
	}

	f.OutputBuf = complex.sendOutBuffer
}

// createPortComplex initializes the internal structures for a given port pair.
func (m *Multiplexer) createPortComplex(
	local, remote akita.Port,
) portComplex {

	sendOutBuf := util.NewBuffer(2 * m.numReqPerCycle)
	forwardBuf := util.NewBuffer(2 * m.numReqPerCycle)
	routeBuf := util.NewBuffer(2 * m.numReqPerCycle)
	pipeline := pipelining.NewPipeline(
		local.Name()+"pipeline", m.switchLatency, 1, routeBuf)

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

// AddLowSidePort set the Multiplexer low-side port (usually multiple).
func (m *Multiplexer) AddLowSidePort(
	ep *EndPoint,
) akita.Port {
	local := akita.NewLimitNumMsgPort(m, m.bufferSizeInNumFlit,
		fmt.Sprintf("%s.LowSidePort.%d", m.Name(), len(m.LowSidePorts)))

	conn := akita.NewDirectConnection(
		fmt.Sprintf("%s-%s", ep.NetworkPort.Name(), local.Name()),
		m.Engine,
		m.Freq,
	)

	conn.PlugIn(local, 2*m.numReqPerCycle)
	conn.PlugIn(ep.NetworkPort, 2*m.numReqPerCycle)

	complex := m.createPortComplex(local, ep.NetworkPort)

	m.LowSidePorts = append(m.LowSidePorts, local)
	m.portToComplexMapping[local] = complex

	m.uplinkArbiter.AddBuffer(complex.forwardBuffer)

	ep.DefaultSwitchDst = local

	return local
}

// SetHighSideEndPoint sets the Multiplexer high-side endpoint (usually single).
func (m *Multiplexer) SetHighSideEndPoint(
	ep *EndPoint,
) akita.Port {
	local := akita.NewLimitNumMsgPort(m, m.bufferSizeInNumFlit,
		fmt.Sprintf("%s.HighSidePort", m.Name()))

	conn := akita.NewDirectConnection(
		fmt.Sprintf("%s-%s", ep.NetworkPort.Name(), local.Name()),
		m.Engine,
		m.Freq,
	)

	conn.PlugIn(local, 2*m.numReqPerCycle)
	conn.PlugIn(ep.NetworkPort, 2*m.numReqPerCycle)

	complex := m.createPortComplex(local, ep.NetworkPort)

	m.HighSidePort = local
	m.portToComplexMapping[local] = complex

	ep.DefaultSwitchDst = local

	return local
}

// SetHighSideHybridEndPoint sets the Multiplexer high-side endpoint (usually single).
func (m *Multiplexer) SetHighSideHybridEndPoint(
	ep *HybridEndPoint,
) akita.Port {
	local := akita.NewLimitNumMsgPort(m, m.bufferSizeInNumFlit,
		fmt.Sprintf("%s.HighSidePort", m.Name()))

	conn := akita.NewDirectConnection(
		fmt.Sprintf("%s-%s", ep.NetworkPort.Name(), local.Name()),
		m.Engine,
		m.Freq,
	)

	conn.PlugIn(local, 2*m.numReqPerCycle)
	conn.PlugIn(ep.NetworkPort, 2*m.numReqPerCycle)

	complex := m.createPortComplex(local, ep.NetworkPort)

	m.HighSidePort = local
	m.portToComplexMapping[local] = complex

	ep.DefaultSwitchDst = local

	return local
}

// SetHighSidePort sets the Multiplexer high-side port (usually single).
func (m *Multiplexer) SetHighSidePort(
	remote akita.Port,
) akita.Port {
	local := akita.NewLimitNumMsgPort(m, m.bufferSizeInNumFlit,
		fmt.Sprintf("%s.HighSidePort", m.Name()))

	conn := akita.NewDirectConnection(
		fmt.Sprintf("%s-%s", remote.Name(), local.Name()),
		m.Engine,
		m.Freq,
	)

	conn.PlugIn(local, 2*m.numReqPerCycle)
	conn.PlugIn(remote, 2*m.numReqPerCycle)

	complex := m.createPortComplex(local, remote)

	m.HighSidePort = local
	m.portToComplexMapping[local] = complex

	return local
}

// AddRoute adds a routing entry to the Multiplexer's routing table.
func (m *Multiplexer) AddRoute(
	srcPort akita.Port,
	dstPort akita.Port,
) {
	m.RoutingTable.AddRoute(srcPort, dstPort)
}

// ConnectSwitches connect two switches together.
func ConnectMultiplexers(
	engine akita.Engine,
	a, b *Multiplexer,
	freq akita.Freq,
) (portOnA, portOnB akita.Port) {
	portA := akita.NewLimitNumMsgPort(a, a.bufferSizeInNumFlit,
		fmt.Sprintf("%s.Port%d", a.Name(), len(a.LowSidePorts)+1))
	portB := akita.NewLimitNumMsgPort(b, b.bufferSizeInNumFlit,
		fmt.Sprintf("%s.Port%d", b.Name(), len(b.LowSidePorts)+1))

	conn := akita.NewDirectConnection(
		fmt.Sprintf("%s-%s", portA.Name(), portB.Name()),
		engine, freq)
	conn.PlugIn(portA, 2*a.numReqPerCycle)
	conn.PlugIn(portB, 2*b.numReqPerCycle)

	portComplexA := a.createPortComplex(portA, portB)
	portComplexB := b.createPortComplex(portB, portA)

	if a.HighSidePort != nil {
		panic("multiplexer A already has high-side port set")
	}

	a.HighSidePort = portA
	a.portToComplexMapping[portComplexA.localPort] = portComplexA

	b.LowSidePorts = append(b.LowSidePorts, portComplexB.localPort)
	b.portToComplexMapping[portComplexB.localPort] = portComplexB
	b.uplinkArbiter.AddBuffer(portComplexB.forwardBuffer)

	for _, srcPort := range a.RoutingTable.GetAllSrcPorts() {
		b.RoutingTable.AddRoute(srcPort, portB)
	}

	return portA, portB
}

// MultiplexerBuilder helps building a Multiplexer.
type MultiplexerBuilder struct {
	engine              akita.Engine
	freq                akita.Freq
	switchLatency       int
	bufferSizeInNumFlit int
	numReqPerCycle      int
	routingTable        RoutingTable
}

func MakeMultiplexerBuilder() MultiplexerBuilder {
	return MultiplexerBuilder{
		bufferSizeInNumFlit: 16,
		switchLatency:       1,
		numReqPerCycle:      1,
	}
}

// WithEngine sets the engine of the Multiplexer to build.
func (b MultiplexerBuilder) WithEngine(e akita.Engine) MultiplexerBuilder {
	b.engine = e
	return b
}

// WithFreq sets the frequency of the Multiplexer to build.
func (b MultiplexerBuilder) WithFreq(freq akita.Freq) MultiplexerBuilder {
	b.freq = freq
	return b
}

// WithNumReqPerCycle sets the number of requests the Multiplexer can handle
// per cycle.
func (b MultiplexerBuilder) WithNumReqPerCycle(
	numReqPerCycle int,
) MultiplexerBuilder {
	b.numReqPerCycle = numReqPerCycle
	return b
}

// WithSwitchLatency sets the internal switch latency of the Multiplexer.
func (b MultiplexerBuilder) WithSwitchLatency(
	latency int,
) MultiplexerBuilder {
	b.switchLatency = latency
	return b
}

// WithBufferSizeInNumFlit sets the buffer size in number of flits.
func (b MultiplexerBuilder) WithBufferSizeInNumFlit(
	size int,
) MultiplexerBuilder {
	b.bufferSizeInNumFlit = size
	return b
}

// WithRoutingTable sets the routing table of the Multiplexer.
func (b MultiplexerBuilder) WithRoutingTable(
	table RoutingTable,
) MultiplexerBuilder {
	b.routingTable = table
	return b
}

func (b MultiplexerBuilder) Build(name string) *Multiplexer {
	m := &Multiplexer{}
	m.TickingComponent = akita.NewTickingComponent(name, b.engine, b.freq, m)
	m.portToComplexMapping = make(map[akita.Port]portComplex)

	if b.routingTable == nil {
		panic("Routing table must be provided to build a Multiplexer")
	}

	m.RoutingTable = b.routingTable
	m.uplinkArbiter = NewRRArbiter()

	m.numReqPerCycle = b.numReqPerCycle
	m.switchLatency = b.switchLatency
	m.bufferSizeInNumFlit = b.bufferSizeInNumFlit

	return m
}
