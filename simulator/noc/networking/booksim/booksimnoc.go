package noc

import (
	"fmt"
	"log"
	"reflect"
	"strings"
	"sync"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/util/tracing"
)

type BookSimNoC interface {
	CreateNetwork(config string)
	CreateNetworkWithLib(lib string, config string)
	PlugInSMSide(p akita.Port, size int, numPhysicalPort int) akita.Port
	PlugInMemSide(p akita.Port, size int, numPhysicalPort int) akita.Port
}

// BookSimNoCImpl is an Akita component that:
//  1. Fetches messages from nocPorts and injects them into BookSim
//  2. Advances the BookSim simulation each cycle
//  3. Retrieves completed packets from BookSim and delivers them to destination nocPorts
type BookSimNoCImpl struct {
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

// NewBookSimNoC creates a BookSim network wrapper.
// config: path to the BookSim configuration file (can be empty string "")
// numNodes: total number of nodes (should match n_shader + n_mem in config)
func NewBookSimNoC(
	name string,
	engine akita.Engine,
) *BookSimNoCImpl {
	noc := &BookSimNoCImpl{
		inflightMsg: make(map[uint64]akita.Msg),
		flitSize:    40,
	}

	noc.TickingComponent = akita.NewTickingComponent(name, engine, 1*akita.GHz, noc)
	noc.port2Node = make(map[akita.Port]int)

	return noc
}

// CreateNetwork initializes the BookSim network
func (noc *BookSimNoCImpl) CreateNetwork(
	config string,
) {
	noc.wrapper = NewNetworkWrapper(config, noc.MaxNumSMSidePort, noc.MaxNumMemSidePort)

	noc.nocPorts = make([]akita.Port, noc.MaxNumSMSidePort+noc.MaxNumMemSidePort)
	noc.outPorts = make([]akita.Port, noc.MaxNumSMSidePort+noc.MaxNumMemSidePort)
	log.Printf("[BookSimNoCImpl] Created BookSim network with %d SM side ports and %d Mem side ports\n",
		noc.MaxNumSMSidePort, noc.MaxNumMemSidePort)
}

// CreateNetworkWithLib initializes the BookSim network
func (noc *BookSimNoCImpl) CreateNetworkWithLib(
	lib string,
	config string,
) {
	noc.wrapper = NewNetworkWrapperWithLib(lib, config, noc.MaxNumSMSidePort, noc.MaxNumMemSidePort)

	noc.nocPorts = make([]akita.Port, noc.MaxNumSMSidePort+noc.MaxNumMemSidePort)
	noc.outPorts = make([]akita.Port, noc.MaxNumSMSidePort+noc.MaxNumMemSidePort)
	log.Printf("[BookSimNoCImpl] Created BookSim network %s with %d SM side ports and %d Mem side ports\n",
		noc.Name(), noc.MaxNumSMSidePort, noc.MaxNumMemSidePort)
}

// Close releases the underlying BookSim network
func (noc *BookSimNoCImpl) Close() {
	noc.mutex.Lock()
	defer noc.mutex.Unlock()

	noc.wrapper.Close()

	noc.wrapper = nil
}

// PlugInSMSide connects an external port to a specific BookSim node
func (noc *BookSimNoCImpl) PlugInSMSide(p akita.Port, size int, numPhysicalPort int) akita.Port {
	noc.mutex.Lock()
	defer noc.mutex.Unlock()

	nextID := len(noc.SMSidePorts)

	for _, port := range noc.nocPorts {
		if port == p {
			panic(fmt.Sprintf("[BookSimNoCImpl] duplicate mapping for node %d", nextID))
		}
	}

	if nextID >= noc.MaxNumSMSidePort {
		panic(fmt.Sprintf("[BookSimNoCImpl] SMSide node %d out of range", nextID))
	}

	nocPort := akita.NewLimitNumMsgPort(noc, size, fmt.Sprintf("%s.NocPort[%d]", noc.Name(), nextID))
	noc.nocPorts[nextID] = nocPort
	noc.outPorts[nextID] = p
	noc.SMSidePorts = append(noc.SMSidePorts, p)

	conn := NewBookSimConnection(fmt.Sprintf("BookSimSMSideConn[%d]", nextID), noc.Engine, 1*akita.GHz)
	conn.PlugIn(nocPort, size)
	conn.PlugIn(p, size)

	if _, exists := noc.port2Node[p]; exists {
		panic("BookSimNoCImpl: duplicate port mapping")
	}
	noc.port2Node[p] = nextID

	return nocPort
}

// PlugInSMSide connects an external port to a specific BookSim node
func (noc *BookSimNoCImpl) PlugInMagicSMSide(p akita.Port, size int, numPhysicalPort int) akita.Port {
	noc.mutex.Lock()
	defer noc.mutex.Unlock()

	nextID := len(noc.SMSidePorts)

	for _, port := range noc.nocPorts {
		if port == p {
			panic(fmt.Sprintf("[BookSimNoCImpl] duplicate mapping for node %d", nextID))
		}
	}

	if nextID >= noc.MaxNumSMSidePort {
		panic(fmt.Sprintf("[BookSimNoCImpl] SMSide node %d out of range", nextID))
	}

	nocPort := akita.NewLimitNumMsgPort(noc, size, fmt.Sprintf("%s.NocPort[%d]", noc.Name(), nextID))
	noc.nocPorts[nextID] = nocPort
	noc.outPorts[nextID] = p
	noc.SMSidePorts = append(noc.SMSidePorts, p)

	var conn akita.Connection

	if strings.Contains(p.Name(), "TLB") {
		conn = NewMagicConnection(fmt.Sprintf("BookSimMagicSMSideConn[%d]", nextID), noc.Engine, 1*akita.GHz)
	} else {
		conn = NewBookSimConnection(fmt.Sprintf("BookSimSMSideConn[%d]", nextID), noc.Engine, 1*akita.GHz)
	}

	conn.PlugIn(nocPort, size)
	conn.PlugIn(p, size)

	if _, exists := noc.port2Node[p]; exists {
		panic("BookSimNoCImpl: duplicate port mapping")
	}
	noc.port2Node[p] = nextID

	return nocPort
}

// PlugInMemSide connects an external port to a specific BookSim node
func (noc *BookSimNoCImpl) PlugInMemSide(p akita.Port, size int, numPhysicalPort int) akita.Port {
	noc.mutex.Lock()
	defer noc.mutex.Unlock()

	nextID := len(noc.MemSidePorts) + noc.MaxNumSMSidePort

	for _, port := range noc.nocPorts {
		if port == p {
			panic(fmt.Sprintf("[BookSimNoCImpl] duplicate mapping for node %d", nextID))
		}
	}

	if nextID >= noc.MaxNumMemSidePort+noc.MaxNumSMSidePort {
		panic(fmt.Sprintf("[BookSimNoCImpl] MemSide node %d out of range", nextID))
	}

	nocPort := akita.NewLimitNumMsgPort(noc, size, fmt.Sprintf("%s.NocPort[%d]", noc.Name(), nextID))
	noc.nocPorts[nextID] = nocPort
	noc.outPorts[nextID] = p
	noc.MemSidePorts = append(noc.MemSidePorts, p)

	conn := NewBookSimConnection(fmt.Sprintf("BookSimMemSideConn[%d]", nextID), noc.Engine, 1*akita.GHz)
	conn.PlugIn(nocPort, size)
	conn.PlugIn(p, size)

	if _, exists := noc.port2Node[p]; exists {
		panic("BookSimNoCImpl: duplicate port mapping")
	}
	noc.port2Node[p] = nextID

	return nocPort
}

// PlugInMemSide connects an external port to a specific BookSim node
func (noc *BookSimNoCImpl) PlugInMagicMemSide(p akita.Port, size int, numPhysicalPort int) akita.Port {
	noc.mutex.Lock()
	defer noc.mutex.Unlock()

	nextID := len(noc.MemSidePorts) + noc.MaxNumSMSidePort

	for _, port := range noc.nocPorts {
		if port == p {
			panic(fmt.Sprintf("[BookSimNoCImpl] duplicate mapping for node %d", nextID))
		}
	}

	if nextID >= noc.MaxNumMemSidePort+noc.MaxNumSMSidePort {
		panic(fmt.Sprintf("[BookSimNoCImpl] MemSide node %d out of range", nextID))
	}

	nocPort := akita.NewLimitNumMsgPort(noc, size, fmt.Sprintf("%s.NocPort[%d]", noc.Name(), nextID))
	noc.nocPorts[nextID] = nocPort
	noc.outPorts[nextID] = p
	noc.MemSidePorts = append(noc.MemSidePorts, p)

	var conn akita.Connection

	if strings.Contains(p.Name(), "TLB") {
		conn = NewMagicConnection(fmt.Sprintf("BookSimMagicMemSideConn[%d]", nextID), noc.Engine, 1*akita.GHz)
	} else {
		conn = NewBookSimConnection(fmt.Sprintf("BookSimMemSideConn[%d]", nextID), noc.Engine, 1*akita.GHz)
	}
	conn.PlugIn(nocPort, size)
	conn.PlugIn(p, size)

	if _, exists := noc.port2Node[p]; exists {
		panic("BookSimNoCImpl: duplicate port mapping")
	}
	noc.port2Node[p] = nextID

	return nocPort
}

// ---- Tick Logic ----

// Tick executes one simulation cycle: injection → advancement → ejection
func (noc *BookSimNoCImpl) Tick(now akita.VTimeInSec) bool {
	noc.mutex.Lock()
	defer noc.mutex.Unlock()
	if !noc.wrapper.Open() {
		panic("[BookSimNoCImpl] not opened yet")
	}

	madeProgress := false

	// Injection phase
	for srcNode, in := range noc.nocPorts {
		for {
			msg := in.Peek()
			if msg == nil {
				break
			}

			tracing.TraceReqInitiate(
				msg,
				now,
				noc,
				tracing.MsgIDAtReceiver(msg, noc),
			)

			dstNode := noc.route(msg)
			if dstNode < 0 {
				panic("BookSimNoCImpl: invalid routeFn result (<0)")
			}

			numFlits := noc.prepareFlits(msg)
			if !noc.wrapper.CanInject(srcNode, numFlits) {
				break
			}

			packetID := noc.wrapper.Send(
				srcNode,
				dstNode,
				msg,
			)

			if _, found := noc.inflightMsg[packetID]; found {
				panic("BookSimNoCImpl: duplicate packet ID")
			}
			noc.inflightMsg[packetID] = msg

			tracing.AddTaskStep(
				tracing.MsgIDAtReceiver(msg, noc),
				now,
				noc,
				fmt.Sprintf("%d:%s:%d", srcNode, noc.Name(), dstNode),
			)

			in.Retrieve(now)

			madeProgress = true
		}
	}

	// Advance network
	noc.wrapper.Tick()

	// Ejection phase
	for node, out := range noc.nocPorts {
		for {
			ok, packetID := noc.wrapper.Peek(node)
			if !ok {
				break
			}

			msg, found := noc.inflightMsg[packetID]
			if !found {
				panic("BookSimNoCImpl: unknown packet ID")
			}

			msg.Meta().SendTime = now

			err := out.Send(msg)
			if err != nil {
				break
			}

			tracing.TraceReqFinalize(msg, now, noc)

			noc.wrapper.Pop(node)

			delete(noc.inflightMsg, packetID)

			madeProgress = true
		}
	}

	if noc.wrapper.Busy() {
		madeProgress = true
	}

	return madeProgress
}

// ---- Helper functions ----

func (noc *BookSimNoCImpl) prepareFlits(msg akita.Msg) int {
	bytes := msg.Meta().TrafficBytes
	if bytes <= 0 {
		panic(fmt.Sprintf("BookSimNoCImpl: %v with non-positive size", reflect.TypeOf(msg)))
	}
	flits := (bytes + noc.flitSize - 1) / noc.flitSize
	if flits < 1 {
		flits = 1
	}
	return flits
}

func (noc *BookSimNoCImpl) route(m akita.Msg) int {
	if node, exists := noc.port2Node[m.Meta().Dst]; exists {
		return node
	}
	panic("BookSimNoCImpl: dst port not mapped to node")
}
