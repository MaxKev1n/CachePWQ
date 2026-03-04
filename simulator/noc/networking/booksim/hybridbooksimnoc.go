package noc

import "C"
import (
	"container/list"
	"fmt"
	"log"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/tracing"
)

type BookSimPipelineItem struct {
	taskID     string
	msg        akita.Msg
	leftCycles uint64
}

var latencyMatrix [][]uint64

func init() {
	latencyMatrix = [][]uint64{
		{50, 60, 70, 80, 150, 160, 170, 180},
		{60, 70, 80, 50, 160, 170, 180, 150},
		{70, 80, 50, 60, 170, 180, 150, 160},
		{80, 50, 60, 70, 180, 150, 160, 170},
		{150, 160, 170, 180, 50, 60, 70, 80},
		{160, 170, 180, 150, 60, 70, 80, 50},
		{170, 180, 150, 160, 70, 80, 50, 60},
		{180, 150, 160, 170, 80, 50, 60, 70},
	}
}

func GetNUMALatency(
	srcPortName string,
	dstPortName string,
) uint64 {
	if !strings.Contains(srcPortName, "GPC") &&
		!strings.Contains(srcPortName, "MP") {
		return 50
	}

	if !strings.Contains(dstPortName, "GPC") &&
		!strings.Contains(dstPortName, "MP") {
		return 50
	}

	var gpcID, mpID, l2ID int
	// identify GPC or MP ID from port name
	if strings.Contains(dstPortName, "MP") {
		gpcID = extractGPCID(srcPortName)
		mpID = extractMPID(dstPortName)
		l2ID = extractL2ID(dstPortName)
	} else {
		mpID = extractMPID(srcPortName)
		l2ID = extractL2ID(srcPortName)
		gpcID = extractGPCID(dstPortName)
	}

	return latencyMatrix[gpcID][mpID] + uint64(l2ID)*2
}

func extractGPCID(portName string) int {
	id, err := strconv.Atoi(strings.Split(portName, ".")[2][4:6])
	if err != nil {
		panic(fmt.Sprintf("failed to extract GPC ID from port name %s: %v", portName, err))
	}

	return id
}

func extractMPID(portName string) int {
	id, err := strconv.Atoi(strings.Split(portName, ".")[2][3:5])
	if err != nil {
		panic(fmt.Sprintf("failed to extract MP ID from port name %s: %v", portName, err))
	}

	return id
}

func extractL2ID(portName string) int {
	id, err := strconv.Atoi(strings.Split(portName, ".")[3][3:5])
	if err != nil {
		panic(fmt.Sprintf("failed to extract L2 ID from port name %s: %v", portName, err))
	}

	return id
}

func (t BookSimPipelineItem) TaskID() string {
	return t.taskID
}

type BookSimEndPoint struct {
	nodeID  int
	nocPort akita.Port
	outPort akita.Port

	inList          *list.List
	outList         *list.List
	inLookupBuffer  util.Buffer
	outLookupBuffer util.Buffer

	numPhysicalPorts int

	srcRRPtr int
	dstRRPtr int

	noc *HybridBookSimNoC

	subNetworkID int
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
	ep.outLookupBuffer = util.NewBuffer(2 * ep.numPhysicalPorts)

	ep.inList = list.New()
	ep.inList.Init()

	ep.outList = list.New()
	ep.outList.Init()

	return ep
}

func (e *BookSimEndPoint) setSubNetworkID(id int) {
	e.subNetworkID = id
}

func (e *BookSimEndPoint) Run(now akita.VTimeInSec) bool {
	madeProgress := false

	for i := 0; i < e.numPhysicalPorts; i++ {
		madeProgress = e.parseFromDevice(now) || madeProgress
	}

	madeProgress = e.TickList(e.inList) || madeProgress
	madeProgress = e.parseFromNoC(now) || madeProgress
	madeProgress = e.TickList(e.outList) || madeProgress

	return madeProgress
}

func (e *BookSimEndPoint) parseFromNoC(now akita.VTimeInSec) bool {
	madeProgress := false

	for {
		item := e.outLookupBuffer.Peek()
		if item == nil {
			return madeProgress
		}

		msg := item.(BookSimPipelineItem).msg

		msg.Meta().SendTime = now

		err := e.nocPort.Send(msg)
		if err != nil {
			return madeProgress
		}

		tracing.TraceReqFinalize(msg, now, e.noc)

		e.outLookupBuffer.Pop()
		madeProgress = true
	}
}

func (e *BookSimEndPoint) TickList(
	processList *list.List,
) bool {
	if processList.Len() == 0 {
		return false
	}

	madeProgress := false

	for elem := processList.Front(); elem != nil; {
		next := elem.Next()

		item := elem.Value.(BookSimPipelineItem)
		if item.leftCycles > 0 {
			item.leftCycles--
			elem.Value = item

			madeProgress = true
		} else {
			if processList == e.inList {
				if e.inLookupBuffer.CanPush() {
					e.inLookupBuffer.Push(item)

					processList.Remove(elem)
				}
			} else {
				if e.outLookupBuffer.CanPush() {
					e.outLookupBuffer.Push(item)

					processList.Remove(elem)
				}
			}

			madeProgress = true
		}

		elem = next
	}

	return madeProgress
}

func (e *BookSimEndPoint) parseFromDevice(now akita.VTimeInSec) bool {
	item := e.nocPort.Peek()
	if item == nil {
		return false
	}

	if e.inList.Len() >= e.numPhysicalPorts*50 {
		return false
	}

	tracing.TraceReqInitiate(
		item,
		now,
		e.noc,
		tracing.MsgIDAtReceiver(item, e.noc),
	)

	pipelineItem := BookSimPipelineItem{
		taskID: akita.GetIDGenerator().Generate(),
		msg:    item,
		leftCycles: GetNUMALatency(
			item.Meta().Src.Name(),
			item.Meta().Dst.Name(),
		),
	}
	e.inList.PushBack(pipelineItem)

	e.nocPort.Retrieve(now)

	return true
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

	rrPtr int
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

// PlugInNUMASMSideMultiPort connects an external port to a specific BookSim node
func (NoC *HybridBookSimNoC) PlugInNUMASMSideMultiPort(
	p akita.Port,
	size int,
	numPhysicalPort int,
	subNetId int,
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

	ep.setSubNetworkID(subNetId)

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

// PlugInNUMAMemSideMultiPort connects an external port to a specific BookSim node
func (NoC *HybridBookSimNoC) PlugInNUMAMemSideMultiPort(
	p akita.Port,
	size int,
	numPhysicalPort int,
	subNetId int,
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

	ep.setSubNetworkID(subNetId)

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
	numEndpoints := len(NoC.endpoints)

	// Injection phase
	startPtr := NoC.rrPtr
	for i := 0; i < numEndpoints; i++ {
		currIdx := (startPtr + i) % numEndpoints
		ep := NoC.endpoints[currIdx]

		for {
			item := ep.inLookupBuffer.Peek()
			if item == nil {
				break
			}

			msg := item.(BookSimPipelineItem).msg

			issued := false
			for p := 0; p < ep.numPhysicalPorts; p++ {
				srcNode := ep.GetSrcNodeID()

				dstEp := NoC.Route(msg)
				if dstEp == nil {
					panic("HybridBookSimNoC: invalid routeFn result (nil)")
				}

				if !NoC.wrapper.CanInject(srcNode, msg.Meta().TrafficBytes) {
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

				NoC.rrPtr = (currIdx + 1) % numEndpoints
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
		remaining := ep.numPhysicalPorts

		for p := 0; p < ep.numPhysicalPorts; p++ {
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

				if remaining <= 0 {
					break
				}

				if ep.outList.Len() >= ep.numPhysicalPorts*50 {
					break
				}

				pipelineItem := BookSimPipelineItem{
					taskID: akita.GetIDGenerator().Generate(),
					msg:    msg,
					leftCycles: GetNUMALatency(
						msg.Meta().Src.Name(),
						msg.Meta().Dst.Name(),
					),
				}
				ep.outList.PushBack(pipelineItem)

				remaining -= 1

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

func (NoC *HybridBookSimNoC) Route(m akita.Msg) *BookSimEndPoint {
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
