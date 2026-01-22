package noc

import "C"
import (
	"fmt"
	"log"
	"reflect"
	"sync"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/pipelining"
	"gitlab.com/akita/util/tracing"
)

type BookSimPipelineItem struct {
	taskID string
	msg    akita.Msg
}

func (t BookSimPipelineItem) TaskID() string {
	return t.taskID
}

type BookSimEndPoint struct {
	nodeID  int
	nocPort akita.Port
	outPort akita.Port

	inPipeline      pipelining.Pipeline
	inLookupBuffer  util.Buffer
	outPipeline     pipelining.Pipeline
	outLookupBuffer util.Buffer

	numPhysicalPorts int

	srcRRPtr int
	dstRRPtr int

	noc *HybridBookSimNoC
}

func NewBookSimEndPoint(
	NoC *HybridBookSimNoC,
	nodeID int,
	outPort akita.Port,
	size int,
	numPhysicalPorts int,
) *BookSimEndPoint {
	nocPort := akita.NewLimitNumMsgPort(NoC, size, fmt.Sprintf("%s.NocPort[%d]", NoC.Name(), nodeID))

	ep := &BookSimEndPoint{
		nodeID:           nodeID,
		nocPort:          nocPort,
		outPort:          outPort,
		numPhysicalPorts: numPhysicalPorts,
		srcRRPtr:         0,
		dstRRPtr:         0,
		noc:              NoC,
	}

	ep.inLookupBuffer = util.NewBuffer(2 * ep.numPhysicalPorts)
	ep.inPipeline = pipelining.MakeBuilder().
		WithPipelineWidth(ep.numPhysicalPorts).
		WithNumStage(40).
		WithCyclePerStage(1).
		WithPostPipelineBuffer(ep.inLookupBuffer).
		Build(fmt.Sprintf("%s.NocPort[%d]", NoC.Name(), nodeID) + "_in_pipeline")

	ep.outLookupBuffer = util.NewBuffer(2 * ep.numPhysicalPorts)
	ep.outPipeline = pipelining.MakeBuilder().
		WithPipelineWidth(ep.numPhysicalPorts).
		WithNumStage(40).
		WithCyclePerStage(1).
		WithPostPipelineBuffer(ep.outLookupBuffer).
		Build(fmt.Sprintf("%s.NocPort[%d]", NoC.Name(), nodeID) + "_out_pipeline")

	return ep
}

func (e *BookSimEndPoint) Run(now akita.VTimeInSec) bool {
	madeProgess := false

	madeProgess = e.parseFromDevice(now) || madeProgess
	madeProgess = e.inPipeline.Tick(now) || madeProgess
	madeProgess = e.parseFromNoC(now) || madeProgess
	madeProgess = e.outPipeline.Tick(now) || madeProgess

	return madeProgess
}

func (e *BookSimEndPoint) parseFromDevice(now akita.VTimeInSec) bool {
	madeProgess := false

	for {
		item := e.nocPort.Peek()
		if item == nil {
			return madeProgess
		}

		tracing.TraceReqInitiate(
			item,
			now,
			e.noc,
			tracing.MsgIDAtReceiver(item, e.noc),
		)

		if e.isTranslation(item) {
			ok := e.processTranslation(e.inLookupBuffer, item)
			if !ok {
				return madeProgess
			}

			e.nocPort.Retrieve(now)

			madeProgess = true
			continue
		}

		if !e.inPipeline.CanAccept() {
			return madeProgess
		}

		pipelineItem := BookSimPipelineItem{
			taskID: akita.GetIDGenerator().Generate(),
			msg:    item,
		}
		e.inPipeline.Accept(now, pipelineItem)

		e.nocPort.Retrieve(now)
		madeProgess = true
	}
}

func (e *BookSimEndPoint) isTranslation(
	msg akita.Msg,
) bool {
	return false

	//srcName := msg.Meta().Src.Name()
	//dstName := msg.Meta().Dst.Name()
	//
	//return strings.Contains(srcName, "TLB") || strings.Contains(dstName, "TLB")
}

func (e *BookSimEndPoint) processTranslation(
	buffer util.Buffer,
	msg akita.Msg,
) bool {
	if !buffer.CanPush() {
		return false
	}

	pipelineItem := BookSimPipelineItem{
		taskID: akita.GetIDGenerator().Generate(),
		msg:    msg,
	}

	buffer.Push(pipelineItem)

	return true
}

func (e *BookSimEndPoint) parseFromNoC(now akita.VTimeInSec) bool {
	madeProgess := false

	for {
		item := e.outLookupBuffer.Peek()
		if item == nil {
			return madeProgess
		}

		msg := item.(BookSimPipelineItem).msg

		msg.Meta().SendTime = now

		err := e.nocPort.Send(msg)
		if err != nil {
			return madeProgess
		}

		tracing.TraceReqFinalize(msg, now, e.noc)

		e.outLookupBuffer.Pop()
		madeProgess = true
	}
}

func (e *BookSimEndPoint) GetSrcNodeID() int {
	// Round-robin selection among physical ports
	srcNodeID := e.srcRRPtr
	e.srcRRPtr = (e.srcRRPtr + 1) % e.numPhysicalPorts
	return e.nodeID + srcNodeID
}

func (e *BookSimEndPoint) GetDstNodeID() int {
	// Round-robin selection among physical ports
	dstNodeID := e.dstRRPtr
	e.dstRRPtr = (e.dstRRPtr + 1) % e.numPhysicalPorts
	return e.nodeID + dstNodeID
}

// HybridBookSimNoC is an Akita component that:
//  1. Fetches messages from nocPorts and injects them into BookSim
//  2. Advances the BookSim simulation each cycle
//  3. Retrieves completed packets from BookSim and delivers them to destination nocPorts
type HybridBookSimNoC struct {
	*akita.TickingComponent

	mutex sync.Mutex

	lib     string
	config  string
	wrapper *NetworkWrapper

	inflightMsg   map[uint64]akita.Msg
	endpoints     []*BookSimEndPoint
	port2EndPoint map[akita.Port]*BookSimEndPoint

	MaxNumSMSidePort  int
	MaxNumMemSidePort int
	MaxNumSMSideNode  int
	MaxNumMemSideNode int

	flitSize int
}

// NewHybridBookSimNoC creates a hybrid BookSim network wrapper.
// config: path to the BookSim configuration file (can be empty string "")
// numNodes: total number of nodes (should match n_shader + n_mem in config)
func NewHybridBookSimNoC(
	name string,
	engine akita.Engine,
) *HybridBookSimNoC {
	NoC := &HybridBookSimNoC{
		inflightMsg: make(map[uint64]akita.Msg),
		flitSize:    40,
	}

	NoC.TickingComponent = akita.NewTickingComponent(name, engine, 2*akita.GHz, NoC)
	NoC.port2EndPoint = make(map[akita.Port]*BookSimEndPoint)

	return NoC
}

// CreateNetwork initializes the BookSim network
func (NoC *HybridBookSimNoC) CreateNetwork(
	config string,
) {
	NoC.config = config
	NoC.endpoints = make([]*BookSimEndPoint, NoC.MaxNumSMSidePort+NoC.MaxNumMemSidePort)
	log.Printf("[HybridBookSimNoC] Created BookSim network %s with %d SM side ports and %d Mem side ports\n",
		NoC.Name(), NoC.MaxNumSMSidePort, NoC.MaxNumMemSidePort)
}

// CreateNetworkWithLib initializes the BookSim network
func (NoC *HybridBookSimNoC) CreateNetworkWithLib(
	lib string,
	config string,
) {
	NoC.lib = lib
	NoC.config = config
	NoC.endpoints = make([]*BookSimEndPoint, NoC.MaxNumSMSidePort+NoC.MaxNumMemSidePort)
	log.Printf("[HybridBookSimNoC] Created BookSim network %s with %d SM side ports and %d Mem side ports\n",
		NoC.Name(), NoC.MaxNumSMSidePort, NoC.MaxNumMemSidePort)
}

// Establish makes the BookSim network ready for operation
func (NoC *HybridBookSimNoC) Establish() {
	NoC.mutex.Lock()
	defer NoC.mutex.Unlock()

	numSMSideNodes := 0
	numMemSideNodes := 0

	for i := 0; i < NoC.MaxNumSMSidePort; i++ {
		numSMSideNodes += NoC.endpoints[i].numPhysicalPorts
	}

	for i := 0; i < NoC.MaxNumMemSidePort; i++ {
		numMemSideNodes += NoC.endpoints[NoC.MaxNumSMSidePort+i].numPhysicalPorts
	}

	if numSMSideNodes != NoC.MaxNumSMSideNode || numMemSideNodes != NoC.MaxNumMemSideNode {
		panic(fmt.Sprintf("[HybridBookSimNoC] number of nodes mismatch: expected (%d, %d), got (%d, %d)",
			NoC.MaxNumSMSideNode, NoC.MaxNumMemSideNode, numSMSideNodes, numMemSideNodes))
	}

	if NoC.lib == "" {
		NoC.wrapper = NewNetworkWrapper(NoC.config, numSMSideNodes, numMemSideNodes)
	} else {
		NoC.wrapper = NewNetworkWrapperWithLib(NoC.lib, NoC.config, numSMSideNodes, numMemSideNodes)
	}
	log.Printf("[HybridBookSimNoC] Established BookSim network %s (%d %d)\n", NoC.Name(), numSMSideNodes, numMemSideNodes)
}

// Close releases the underlying BookSim network
func (NoC *HybridBookSimNoC) Close() {
	NoC.mutex.Lock()
	defer NoC.mutex.Unlock()

	NoC.wrapper.Close()

	NoC.wrapper = nil
}

// PlugInSMSideMultiPort connects an external port to a specific BookSim node
func (NoC *HybridBookSimNoC) PlugInSMSideMultiPort(
	p akita.Port,
	size int,
	numPhysicalPort int,
) akita.Port {
	NoC.mutex.Lock()
	defer NoC.mutex.Unlock()

	nextID := 0
	nextEndpointID := 0
	for i := 0; i < NoC.MaxNumSMSidePort; i++ {
		if NoC.endpoints[i] == nil {
			break
		}

		nextID += NoC.endpoints[i].numPhysicalPorts
		nextEndpointID++
	}

	for _, ep := range NoC.endpoints {
		if ep != nil && ep.outPort == p {
			panic(fmt.Sprintf("[HybridBookSimNoC] duplicate mapping for node %d", nextID))
		}
	}

	ep := NewBookSimEndPoint(NoC, nextID, p, size, numPhysicalPort)
	NoC.endpoints[nextEndpointID] = ep

	if p.GetConnection() == nil {
		conn := NewBookSimConnection(fmt.Sprintf("BookSimSMSideConn[%d]", nextEndpointID), NoC.Engine, NoC.Freq)
		conn.PlugIn(ep.nocPort, size)
		conn.PlugIn(p, size)
	}

	if _, exists := NoC.port2EndPoint[p]; exists {
		panic("HybridBookSimNoC: duplicate port mapping")
	}
	NoC.port2EndPoint[p] = ep

	return ep.nocPort
}

// PlugInMemSideMultiPort connects an external port to a specific BookSim node
func (NoC *HybridBookSimNoC) PlugInMemSideMultiPort(
	p akita.Port,
	size int,
	numPhysicalPort int,
) akita.Port {
	NoC.mutex.Lock()
	defer NoC.mutex.Unlock()

	nextID := NoC.MaxNumSMSideNode
	nextEndpointID := NoC.MaxNumSMSidePort
	for i := NoC.MaxNumSMSidePort; i < NoC.MaxNumSMSidePort+NoC.MaxNumMemSidePort; i++ {
		if NoC.endpoints[i] == nil {
			break
		}

		nextID += NoC.endpoints[i].numPhysicalPorts
		nextEndpointID++
	}

	for _, ep := range NoC.endpoints {
		if ep != nil && ep.outPort == p {
			panic(fmt.Sprintf("[HybridBookSimNoC] duplicate mapping for node %d", nextID))
		}
	}

	ep := NewBookSimEndPoint(NoC, nextID, p, size, numPhysicalPort)
	NoC.endpoints[nextEndpointID] = ep

	if p.GetConnection() == nil {
		conn := NewBookSimConnection(fmt.Sprintf("BookSimMemSideConn[%d]", nextEndpointID), NoC.Engine, NoC.Freq)
		conn.PlugIn(ep.nocPort, size)
		conn.PlugIn(p, size)
	}

	if _, exists := NoC.port2EndPoint[p]; exists {
		panic("HybridBookSimNoC: duplicate port mapping")
	}
	NoC.port2EndPoint[p] = ep

	return ep.nocPort
}

func (NoC *HybridBookSimNoC) PlugInSMSide(port akita.Port, size int) akita.Port {
	panic("Does not support PlugInSMSide yet")
}

func (NoC *HybridBookSimNoC) PlugInMemSide(port akita.Port, size int) akita.Port {
	panic("Does not support PlugInMemSide yet")
}

// ---- Tick Logic ----

// Tick executes one simulation cycle: injection → advancement → ejection
func (NoC *HybridBookSimNoC) Tick(now akita.VTimeInSec) bool {
	NoC.mutex.Lock()
	defer NoC.mutex.Unlock()
	if !NoC.wrapper.Open() {
		panic("[HybridBookSimNoC] not opened yet")
	}

	madeProgress := false

	// Injection phase
	for _, ep := range NoC.endpoints {
		for {
			item := ep.inLookupBuffer.Peek()
			if item == nil {
				break
			}

			msg := item.(BookSimPipelineItem).msg

			issued := false
			for i := 0; i < ep.numPhysicalPorts; i++ {
				srcNode := ep.GetSrcNodeID()

				dstEp := NoC.route(msg)
				if dstEp == nil {
					panic("HybridBookSimNoC: invalid routeFn result (nil)")
				}

				numFlits := NoC.prepareFlits(msg)
				if !NoC.wrapper.CanInject(srcNode, numFlits) {
					continue
				}

				dstNode := dstEp.GetDstNodeID()

				packetID := NoC.wrapper.Send(
					srcNode,
					dstNode,
					msg,
				)

				if _, found := NoC.inflightMsg[packetID]; found {
					panic("HybridBookSimNoC: duplicate packet ID")
				}
				NoC.inflightMsg[packetID] = msg

				tracing.AddTaskStep(
					tracing.MsgIDAtReceiver(msg, NoC),
					now,
					NoC,
					fmt.Sprintf("%d:%s:%d", srcNode, NoC.Name(), dstNode),
				)

				ep.inLookupBuffer.Pop()

				issued = true

				break
			}

			if !issued {
				break
			}

			madeProgress = true
		}
	}

	// Advance network
	NoC.wrapper.Tick()

	// Ejection phase
	for _, ep := range NoC.endpoints {
		for i := 0; i < ep.numPhysicalPorts; i++ {
			node := ep.GetSrcNodeID()

			for {
				ok, packetID := NoC.wrapper.Peek(node)
				if !ok {
					break
				}

				msg, found := NoC.inflightMsg[packetID]
				if !found {
					panic("HybridBookSimNoC: unknown packet ID")
				}

				if ep.isTranslation(msg) {
					ok := ep.processTranslation(ep.outLookupBuffer, msg)
					if !ok {
						break
					}

					NoC.wrapper.Pop(node)

					delete(NoC.inflightMsg, packetID)
					madeProgress = true

					continue
				}

				if !ep.outPipeline.CanAccept() {
					break
				}

				pipelineItem := BookSimPipelineItem{
					taskID: akita.GetIDGenerator().Generate(),
					msg:    msg,
				}
				ep.outPipeline.Accept(now, pipelineItem)

				NoC.wrapper.Pop(node)

				delete(NoC.inflightMsg, packetID)

				madeProgress = true
			}
		}
	}

	if NoC.wrapper.Busy() {
		madeProgress = true
	}

	for _, ep := range NoC.endpoints {
		madeProgress = ep.Run(now) || madeProgress
	}

	return madeProgress
}

// ---- Helper functions ----

func (NoC *HybridBookSimNoC) prepareFlits(msg akita.Msg) int {
	bytes := msg.Meta().TrafficBytes
	if bytes <= 0 {
		panic(fmt.Sprintf("HybridBookSimNoC: %v with non-positive size", reflect.TypeOf(msg)))
	}
	flits := (bytes + NoC.flitSize - 1) / NoC.flitSize
	if flits < 1 {
		flits = 1
	}
	return flits
}

func (NoC *HybridBookSimNoC) route(m akita.Msg) *BookSimEndPoint {
	if ep, exists := NoC.port2EndPoint[m.Meta().Dst]; exists {
		return ep
	}
	panic("HybridBookSimNoC: dst port not mapped to node")
}

func (NoC *HybridBookSimNoC) AddRoute(src akita.Port, dst akita.Port) {
	if _, exists := NoC.port2EndPoint[src]; exists {
		panic("HybridBookSimNoC: duplicate port mapping")
	}

	if _, exists := NoC.port2EndPoint[dst]; !exists {
		panic("HybridBookSimNoC: destination port not mapped to node")
	}

	NoC.port2EndPoint[src] = NoC.port2EndPoint[dst]
}
