package ring

import (
	"fmt"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/pipelining"
)

type pipelineItem struct {
	taskID string
	msg    akita.Msg
}

func (f pipelineItem) TaskID() string {
	return f.taskID
}

type portComplex struct {
	port          akita.Port
	msgOutBuffer  []akita.Msg
	msgOutBufSize int
}

// Switch is an Akita component that can forward request to destination.
type Switch struct {
	*akita.TickingComponent

	ports                []akita.Port
	portToComplexMapping map[akita.Port]*portComplex
	numReqPerCycle       int
	bufferSizeInNumFlit  int

	NetworkPort        akita.Port
	DefaultNetworkPort akita.Port

	pipeline      pipelining.Pipeline
	sendOutBuffer util.Buffer
}

// Send sends a message.
func (s *Switch) Send(msg akita.Msg) *akita.SendError {
	s.Lock()
	defer s.Unlock()

	pc, exist := s.portToComplexMapping[msg.Meta().Src]
	if !exist {
		pc = s.portToComplexMapping[s.NetworkPort]
	}

	if len(pc.msgOutBuffer) >= pc.msgOutBufSize {
		return &akita.SendError{}
	}

	pc.msgOutBuffer = append(pc.msgOutBuffer, msg)

	s.TickLater(msg.Meta().SendTime)

	return nil
}

// PlugIn connects a port to the endpoint.
func (s *Switch) PlugIn(port akita.Port, srcBufCap int) {
	port.SetConnection(s)

	pc := s.createPortComplex(port, srcBufCap)

	if _, exist := s.portToComplexMapping[port]; exist {
		panic(fmt.Sprintf("Port %s is already connected to switch %s", port.Name(), s.Name()))
	}

	s.ports = append(s.ports, port)
	s.portToComplexMapping[port] = pc
}

// PlugInDefaultNetworkPort connects the default network port to the switch.
func (s *Switch) PlugInDefaultNetworkPort(port akita.Port) {
	s.DefaultNetworkPort = port
}

// NotifyAvailable triggers the endpoint to continue to tick.
func (s *Switch) NotifyAvailable(now akita.VTimeInSec, port akita.Port) {
	s.TickLater(now)
}

// Unplug removes the association of a port and an endpoint.
func (s *Switch) Unplug(port akita.Port) {
	panic("not implemented")
}

func (s *Switch) createPortComplex(
	port akita.Port,
	srcBufCap int,
) *portComplex {
	pc := &portComplex{
		port:          port,
		msgOutBuffer:  make([]akita.Msg, 0),
		msgOutBufSize: srcBufCap,
	}

	return pc
}

// Tick update the Switch's state.
func (s *Switch) Tick(now akita.VTimeInSec) bool {
	madeProgress := false

	for i := 0; i < s.numReqPerCycle; i++ {
		madeProgress = s.sendOut(now) || madeProgress
	}

	madeProgress = s.movePipeline(now) || madeProgress

	for i := 0; i < s.numReqPerCycle; i++ {
		madeProgress = s.startProcessing(now) || madeProgress
	}

	return madeProgress
}

func (s *Switch) startProcessing(now akita.VTimeInSec) (madeProgress bool) {
	for _, port := range s.ports {
		pc, exist := s.portToComplexMapping[port]
		if !exist {
			panic(fmt.Sprintf("Port %s is not connected to switch %s", port.Name(), s.Name()))
		}

		if len(pc.msgOutBuffer) == 0 {
			continue
		}

		msg := pc.msgOutBuffer[0]

		_, exist = s.portToComplexMapping[msg.Meta().Dst]
		if exist {
			msg.Meta().RecvTime = now

			err := msg.Meta().Dst.Recv(msg)
			if err != nil {
				continue
			}

			pc.msgOutBuffer = pc.msgOutBuffer[1:]
			//log.Printf("%.12f, Switch %s, msg %s delivered to %s\n",
			//	msg.Meta().RecvTime, s.Name(), msg.Meta().ID, msg.Meta().Dst.Name())

			madeProgress = true
		} else {
			pc := s.portToComplexMapping[port]
			if !s.pipeline.CanAccept() {
				continue
			}

			pipelineItem := pipelineItem{
				taskID: akita.GetIDGenerator().Generate(),
				msg:    msg,
			}
			s.pipeline.Accept(now, pipelineItem)
			//log.Printf("%.12f, Switch %s, msg %s move to pipeline %s\n",
			//	now, s.Name(), msg.Meta().ID, s.pipeline.Name())

			pc.msgOutBuffer = pc.msgOutBuffer[1:]

			madeProgress = true
		}
	}

	return madeProgress
}

func (s *Switch) movePipeline(now akita.VTimeInSec) (madeProgress bool) {
	return s.pipeline.Tick(now)
}

func (s *Switch) sendOut(now akita.VTimeInSec) bool {
	item := s.sendOutBuffer.Peek()
	if item == nil {
		return false
	}

	msg := item.(pipelineItem).msg

	msg.Meta().SendTime = now

	err := s.DefaultNetworkPort.Send(msg)
	if err != nil {
		return false
	}

	//log.Printf("%.12f, Switch %s, msg %s delivered to %s\n",
	//	msg.Meta().SendTime, s.Name(), msg.Meta().ID, s.DefaultNetworkPort.Name())

	s.sendOutBuffer.Pop()

	return true
}

func (s *Switch) CreateNetworkPort() {
	s.NetworkPort = akita.NewLimitNumMsgPort(s, s.bufferSizeInNumFlit,
		fmt.Sprintf("%s.NetworkPort", s.Name()))

	s.NetworkPort.SetConnection(s)

	pc := &portComplex{
		port:          s.NetworkPort,
		msgOutBuffer:  make([]akita.Msg, 0),
		msgOutBufSize: s.bufferSizeInNumFlit,
	}

	s.ports = append(s.ports, pc.port)
	s.portToComplexMapping[pc.port] = pc
}

// SwitchBuilder can build switches
type SwitchBuilder struct {
	engine              akita.Engine
	freq                akita.Freq
	numReqPerCycle      int
	bufferSizeInNumFlit int
	latency             int
	linkWidth           int
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

// WithLatency sets the latency of the switch to be built.
func (b SwitchBuilder) WithLatency(latency int) SwitchBuilder {
	b.latency = latency
	return b
}

// WithLinkWidth sets the link width of the switch to be built.
func (b SwitchBuilder) WithLinkWidth(linkWidth int) SwitchBuilder {
	b.linkWidth = linkWidth
	return b
}

// Build creates a new switch
func (b SwitchBuilder) Build(name string) *Switch {
	b.engineMustBeGiven()
	b.freqMustNotBeZero()

	s := &Switch{}
	s.TickingComponent = akita.NewTickingComponent(name, b.engine, b.freq, s)
	s.portToComplexMapping = make(map[akita.Port]*portComplex)
	s.numReqPerCycle = b.numReqPerCycle
	s.bufferSizeInNumFlit = b.bufferSizeInNumFlit

	s.sendOutBuffer = util.NewBuffer(2 * b.numReqPerCycle)
	pipelineBuilder := pipelining.MakeBuilder().
		WithPipelineWidth(b.linkWidth).
		WithNumStage(b.latency).
		WithCyclePerStage(1).
		WithPostPipelineBuffer(s.sendOutBuffer)
	s.pipeline = pipelineBuilder.Build(s.Name() + ".pipeline")

	s.CreateNetworkPort()

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
