package noc

import "C"
import (
	"fmt"
	"log"
	"reflect"
	"sync"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/noc"
	"gitlab.com/akita/util/tracing"
)

// HybridBookSimNoC is an Akita component that:
//  1. Fetches messages from nocPorts and injects them into BookSim
//  2. Advances the BookSim simulation each cycle
//  3. Retrieves completed packets from BookSim and delivers them to destination nocPorts
type HybridBookSimNoC struct {
	*akita.TickingComponent

	mutex sync.Mutex

	wrapper *NetworkWrapper

	inflightMsg map[uint64]akita.Msg
	nocPorts    []akita.Port
	outPorts    []akita.Port
	port2Node   map[akita.Port]int

	MaxNumSMSidePort  int
	MaxNumMemSidePort int

	SMSidePorts  []akita.Port
	MemSidePorts []akita.Port

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

	NoC.TickingComponent = akita.NewTickingComponent(name, engine, 1*akita.GHz, NoC)
	NoC.port2Node = make(map[akita.Port]int)

	return NoC
}

// CreateNetwork initializes the BookSim network
func (NoC *HybridBookSimNoC) CreateNetwork(
	config string,
) {
	NoC.wrapper = NewNetworkWrapper(config, NoC.MaxNumSMSidePort, NoC.MaxNumMemSidePort)

	NoC.nocPorts = make([]akita.Port, NoC.MaxNumSMSidePort+NoC.MaxNumMemSidePort)
	NoC.outPorts = make([]akita.Port, NoC.MaxNumSMSidePort+NoC.MaxNumMemSidePort)
	log.Printf("[HybridBookSimNoC] Created BookSim network with %d SM side ports and %d Mem side ports\n",
		NoC.MaxNumSMSidePort, NoC.MaxNumMemSidePort)
}

// CreateNetworkWithLib initializes the BookSim network
func (NoC *HybridBookSimNoC) CreateNetworkWithLib(
	lib string,
	config string,
) {
	NoC.wrapper = NewNetworkWrapperWithLib(lib, config, NoC.MaxNumSMSidePort, NoC.MaxNumMemSidePort)

	NoC.nocPorts = make([]akita.Port, NoC.MaxNumSMSidePort+NoC.MaxNumMemSidePort)
	NoC.outPorts = make([]akita.Port, NoC.MaxNumSMSidePort+NoC.MaxNumMemSidePort)
	log.Printf("[HybridBookSimNoC] Created BookSim network %s with %d SM side ports and %d Mem side ports\n",
		NoC.Name(), NoC.MaxNumSMSidePort, NoC.MaxNumMemSidePort)
}

// Close releases the underlying BookSim network
func (NoC *HybridBookSimNoC) Close() {
	NoC.mutex.Lock()
	defer NoC.mutex.Unlock()

	NoC.wrapper.Close()

	NoC.wrapper = nil
}

// PlugInSMSide connects an external port to a specific BookSim node
func (NoC *HybridBookSimNoC) PlugInSMSide(p akita.Port, size int) akita.Port {
	NoC.mutex.Lock()
	defer NoC.mutex.Unlock()

	nextID := len(NoC.SMSidePorts)

	for _, port := range NoC.outPorts {
		if port == p {
			panic(fmt.Sprintf("[HybridBookSimNoC] duplicate mapping for node %d", nextID))
		}
	}

	if nextID >= NoC.MaxNumSMSidePort {
		panic(fmt.Sprintf("[HybridBookSimNoC] SMSide node %d out of range", nextID))
	}

	nocPort := akita.NewLimitNumMsgPort(NoC, size, fmt.Sprintf("%s.NocPort[%d]", NoC.Name(), nextID))
	NoC.nocPorts[nextID] = nocPort
	NoC.outPorts[nextID] = p
	NoC.SMSidePorts = append(NoC.SMSidePorts, p)

	if p.GetConnection() == nil {
		conn := NewBookSimConnection(fmt.Sprintf("BookSimSMSideConn[%d]", nextID), NoC.Engine, 1*akita.GHz)
		conn.PlugIn(nocPort, size)
		conn.PlugIn(p, size)
	}

	if _, exists := NoC.port2Node[p]; exists {
		panic("HybridBookSimNoC: duplicate port mapping")
	}
	NoC.port2Node[p] = nextID

	return nocPort
}

// PlugInMemSide connects an external port to a specific BookSim node
func (NoC *HybridBookSimNoC) PlugInMemSide(p akita.Port, size int) akita.Port {
	NoC.mutex.Lock()
	defer NoC.mutex.Unlock()

	nextID := len(NoC.MemSidePorts) + NoC.MaxNumSMSidePort

	for _, port := range NoC.outPorts {
		if port == p {
			panic(fmt.Sprintf("[HybridBookSimNoC] duplicate mapping for node %d", nextID))
		}
	}

	if nextID >= NoC.MaxNumMemSidePort+NoC.MaxNumSMSidePort {
		panic(fmt.Sprintf("[HybridBookSimNoC] MemSide node %d out of range", nextID))
	}

	nocPort := akita.NewLimitNumMsgPort(NoC, size, fmt.Sprintf("%s.NocPort[%d]", NoC.Name(), nextID))
	NoC.nocPorts[nextID] = nocPort
	NoC.outPorts[nextID] = p
	NoC.MemSidePorts = append(NoC.MemSidePorts, p)

	if p.GetConnection() == nil {
		conn := NewBookSimConnection(fmt.Sprintf("BookSimMemSideConn[%d]", nextID), NoC.Engine, 1*akita.GHz)
		conn.PlugIn(nocPort, size)
		conn.PlugIn(p, size)
	}

	if _, exists := NoC.port2Node[p]; exists {
		panic("HybridBookSimNoC: duplicate port mapping")
	}
	NoC.port2Node[p] = nextID

	return nocPort
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
	for srcNode, in := range NoC.nocPorts {
		for {
			msg := in.Peek()
			if msg == nil {
				break
			}

			tracing.TraceReqInitiate(
				msg,
				now,
				NoC,
				tracing.MsgIDAtReceiver(msg, NoC),
			)

			dstNode := NoC.route(msg)
			if dstNode < 0 {
				panic("HybridBookSimNoC: invalid routeFn result (<0)")
			}

			numFlits := NoC.prepareFlits(msg)
			if !NoC.wrapper.CanInject(srcNode, numFlits) {
				break
			}

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

			in.Retrieve(now)

			madeProgress = true
		}
	}

	// Advance network
	NoC.wrapper.Tick()

	// Ejection phase
	for node, out := range NoC.nocPorts {
		for {
			ok, packetID := NoC.wrapper.Peek(node)
			if !ok {
				break
			}

			msg, found := NoC.inflightMsg[packetID]
			if !found {
				panic("HybridBookSimNoC: unknown packet ID")
			}

			msg.Meta().SendTime = now

			err := out.Send(msg)
			if err != nil {
				break
			}

			tracing.TraceReqFinalize(msg, now, NoC)

			NoC.wrapper.Pop(node)

			delete(NoC.inflightMsg, packetID)

			madeProgress = true
		}
	}

	if NoC.wrapper.Busy() {
		madeProgress = true
	}

	return madeProgress
}

// ---- Helper functions ----

func (NoC *HybridBookSimNoC) prepareFlits(msg akita.Msg) int {
	if _, ok := msg.(*noc.Flit); ok {
		return 1
	}

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

func (NoC *HybridBookSimNoC) route(m akita.Msg) int {
	if node, exists := NoC.port2Node[m.Meta().Dst]; exists {
		return node
	}
	panic("HybridBookSimNoC: dst port not mapped to node")
}

func (NoC *HybridBookSimNoC) AddRoute(src akita.Port, dst akita.Port) {
	if _, exists := NoC.port2Node[src]; exists {
		panic("HybridBookSimNoC: duplicate port mapping")
	}

	if _, exists := NoC.port2Node[dst]; !exists {
		panic("HybridBookSimNoC: destination port not mapped to node")
	}

	NoC.port2Node[src] = NoC.port2Node[dst]
}
