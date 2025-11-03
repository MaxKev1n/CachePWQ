package noc

/*
#cgo CFLAGS: -I${SRCDIR}/native
#cgo LDFLAGS: -L${SRCDIR}/native -lintersim
#include <stdlib.h>
#include "booksim_shim.hpp"
*/
import "C"

import (
	"fmt"
	"log"
	"reflect"
	"sync"
	"sync/atomic"
	"unsafe"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/mem/device"
)

// BookSimNoC is an Akita component that:
//  1. Fetches messages from nocPorts and injects them into BookSim
//  2. Advances the BookSim simulation each cycle
//  3. Retrieves completed packets from BookSim and delivers them to destination nocPorts
type BookSimNoC struct {
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
) *BookSimNoC {
	noc := &BookSimNoC{
		inflightMsg: make(map[uint64]akita.Msg),
		flitSize:    40,
	}

	noc.TickingComponent = akita.NewTickingComponent(name, engine, 1*akita.GHz, noc)
	noc.port2Node = make(map[akita.Port]int)

	return noc
}

// CreateNetwork initializes the BookSim network
func (noc *BookSimNoC) CreateNetwork(
	config string,
) {
	noc.wrapper = NewNetworkWrapper(config, noc.MaxNumSMSidePort, noc.MaxNumMemSidePort)

	noc.nocPorts = make([]akita.Port, noc.MaxNumSMSidePort+noc.MaxNumMemSidePort)
	noc.outPorts = make([]akita.Port, noc.MaxNumSMSidePort+noc.MaxNumMemSidePort)
	log.Printf("[BookSimNoC] Created BookSim network with %d SM side ports and %d Mem side ports\n",
		noc.MaxNumSMSidePort, noc.MaxNumMemSidePort)
}

// Close releases the underlying BookSim network
func (noc *BookSimNoC) Close() {
	noc.mutex.Lock()
	defer noc.mutex.Unlock()

	noc.wrapper.Close()

	noc.wrapper = nil
}

// PlugInSMSide connects an external port to a specific BookSim node
func (noc *BookSimNoC) PlugInSMSide(p akita.Port, size int) {
	noc.mutex.Lock()
	defer noc.mutex.Unlock()

	nextID := len(noc.SMSidePorts)

	for _, port := range noc.nocPorts {
		if port == p {
			panic(fmt.Sprintf("[BookSimNoC] duplicate mapping for node %d", nextID))
		}
	}

	if nextID >= noc.MaxNumSMSidePort {
		panic(fmt.Sprintf("[BookSimNoC] SMSide node %d out of range", nextID))
	}

	nocPort := akita.NewLimitNumMsgPort(noc, size, fmt.Sprintf("%s.NocPort[%d]", noc.Name(), nextID))
	noc.nocPorts[nextID] = nocPort
	noc.outPorts[nextID] = p
	noc.SMSidePorts = append(noc.SMSidePorts, p)

	conn := NewBookSimConnection(fmt.Sprintf("BookSimSMSideConn[%d]", nextID), noc.Engine, 1*akita.GHz)
	conn.PlugIn(nocPort, size)
	conn.PlugIn(p, size)

	if _, exists := noc.port2Node[p]; exists {
		panic("BookSimNoC: duplicate port mapping")
	}
	noc.port2Node[p] = nextID
}

// PlugInMemSide connects an external port to a specific BookSim node
func (noc *BookSimNoC) PlugInMemSide(p akita.Port, size int) {
	noc.mutex.Lock()
	defer noc.mutex.Unlock()

	nextID := len(noc.MemSidePorts) + noc.MaxNumSMSidePort

	for _, port := range noc.nocPorts {
		if port == p {
			panic(fmt.Sprintf("[BookSimNoC] duplicate mapping for node %d", nextID))
		}
	}

	if nextID >= noc.MaxNumMemSidePort+noc.MaxNumSMSidePort {
		panic(fmt.Sprintf("[BookSimNoC] MemSide node %d out of range", nextID))
	}

	nocPort := akita.NewLimitNumMsgPort(noc, size, fmt.Sprintf("%s.NocPort[%d]", noc.Name(), nextID))
	noc.nocPorts[nextID] = nocPort
	noc.outPorts[nextID] = p
	noc.MemSidePorts = append(noc.MemSidePorts, p)

	conn := NewBookSimConnection(fmt.Sprintf("BookSimMemSideConn[%d]", nextID), noc.Engine, 1*akita.GHz)
	conn.PlugIn(nocPort, size)
	conn.PlugIn(p, size)

	if _, exists := noc.port2Node[p]; exists {
		panic("BookSimNoC: duplicate port mapping")
	}
	noc.port2Node[p] = nextID
}

// ---- Tick Logic ----

// Tick executes one simulation cycle: injection → advancement → ejection
func (noc *BookSimNoC) Tick(now akita.VTimeInSec) bool {
	noc.mutex.Lock()
	defer noc.mutex.Unlock()
	if !noc.wrapper.Open() {
		panic("[BookSimNoC] not opened yet")
	}

	madeProgress := false

	// Injection phase
	for srcNode, in := range noc.nocPorts {
		for {
			msg := in.Peek()
			if msg == nil {
				break
			}

			dstNode := noc.route(msg)
			if dstNode < 0 {
				panic("BookSimNoC: invalid routeFn result (<0)")
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
				panic("BookSimNoC: duplicate packet ID")
			}
			noc.inflightMsg[packetID] = msg

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
				panic("BookSimNoC: unknown packet ID")
			}

			msg.Meta().SendTime = now

			err := out.Send(msg)
			if err != nil {
				break
			}

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

func (noc *BookSimNoC) prepareFlits(msg akita.Msg) int {
	bytes := msg.Meta().TrafficBytes
	if bytes <= 0 {
		panic(fmt.Sprintf("BookSimNoC: %v with non-positive size", reflect.TypeOf(msg)))
	}
	flits := (bytes + noc.flitSize - 1) / noc.flitSize
	if flits < 1 {
		flits = 1
	}
	return flits
}

func (noc *BookSimNoC) route(m akita.Msg) int {
	return noc.port2Node[m.Meta().Dst]
}

// ---- Wrapper ----

type NetworkWrapper struct {
	net C.booksim_net_t

	generator BookSimIDGenerator
}

type BookSimIDGenerator struct {
	nextID uint64
}

func (g *BookSimIDGenerator) Generate() uint64 {
	idNumber := atomic.AddUint64(&g.nextID, 1)
	return idNumber
}

func NewNetworkWrapper(
	config string,
	numCUs int,
	numMems int,
) *NetworkWrapper {
	var path *C.char
	if config != "" {
		path = C.CString(config)
		defer C.free(unsafe.Pointer(path))
	} else {
		panic("BookSimNoC: config is required")
	}

	wrapper := &NetworkWrapper{}

	wrapper.generator = BookSimIDGenerator{
		nextID: 0,
	}
	wrapper.net = C.booksim_create(path, C.int(numCUs), C.int(numMems))

	return wrapper
}

func (wrapper *NetworkWrapper) CanInject(
	node int,
	numFlits int,
) bool {
	return C.booksim_can_inject(wrapper.net, C.int(node), C.int(numFlits)) != 0
}

func (wrapper *NetworkWrapper) Send(
	srcNode int,
	dstNode int,
	msg akita.Msg,
) uint64 {
	packetID := wrapper.generator.Generate()

	var msgType int

	switch msg.(type) {
	case *device.TranslationRsp:
		msgType = 1
	case *mem.DataReadyRsp:
		msgType = 1
	case *mem.WriteReq:
		msgType = 2
	case *mem.WriteDoneRsp:
		msgType = 3
	default:
		msgType = 0
	}

	C.booksim_inject(
		wrapper.net,
		C.int(srcNode),
		C.int(dstNode),
		C.ulonglong(packetID),
		C.int(msgType),
		C.int(msg.Meta().TrafficBytes),
	)

	return packetID
}

func (wrapper *NetworkWrapper) Tick() {
	C.booksim_cycle(wrapper.net)
}

func (wrapper *NetworkWrapper) Pop(
	node int,
) {
	C.booksim_pop(wrapper.net, C.int(node))
}

func (wrapper *NetworkWrapper) Peek(
	node int,
) (bool, uint64) {
	var packetID C.ulonglong
	ok := C.booksim_peek(wrapper.net, C.int(node), &packetID)
	if ok == 0 {
		return false, 0
	}
	return true, uint64(packetID)
}

func (wrapper *NetworkWrapper) Busy() bool {
	return C.booksim_busy(wrapper.net) != 0
}

func (wrapper *NetworkWrapper) Open() bool {
	return wrapper.net != nil
}

func (wrapper *NetworkWrapper) Close() {
	if wrapper.net != nil {
		C.booksim_destroy(wrapper.net)
		wrapper.net = nil
	}
}
