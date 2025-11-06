package noc

/*
#cgo linux LDFLAGS: -ldl
#cgo darwin LDFLAGS: -ldl
#include <stdlib.h>
#include <dlfcn.h>

// ---- 函数指针类型 ----
typedef void* (*create_fn_t)(const char* cfg_path, int n_sms, int n_mems);
typedef int   (*can_inject_fn_t)(void* net, int node, int n_flits);
typedef void  (*inject_fn_t)(void* net, int src, int dst, unsigned long long pkt_id, int msg_type, int size_bytes);
typedef void  (*cycle_fn_t)(void* net);
typedef int   (*peek_fn_t)(void* net, int node, unsigned long long* pkt_id_out);
typedef void  (*pop_fn_t)(void* net, int node);
typedef int   (*busy_fn_t)(void* net);
typedef void  (*destroy_fn_t)(void* net);

// ---- 动态 API 表 ----
typedef struct {
    void* handle;
    create_fn_t     create;
    can_inject_fn_t can_inject;
    inject_fn_t     inject;
    cycle_fn_t      cycle;
    peek_fn_t       peek;
    pop_fn_t        pop;
    busy_fn_t       busy;
    destroy_fn_t    destroy;
} intersim_api_t;

// ---- 动态加载 ----
static intersim_api_t* load_intersim(const char* path) {
    intersim_api_t* api = (intersim_api_t*)calloc(1, sizeof(intersim_api_t));
    if (!api) return NULL;

#if defined(__linux__)
    int flags = RTLD_NOW | RTLD_LOCAL | 0x00008; // RTLD_DEEPBIND (Linux only)
#else
    int flags = RTLD_NOW | RTLD_LOCAL;
#endif

    api->handle = dlopen(path, flags);
    if (!api->handle) return NULL;

    api->create     = (create_fn_t)     dlsym(api->handle, "booksim_create");
    api->can_inject = (can_inject_fn_t) dlsym(api->handle, "booksim_can_inject");
    api->inject     = (inject_fn_t)     dlsym(api->handle, "booksim_inject");
    api->cycle      = (cycle_fn_t)      dlsym(api->handle, "booksim_cycle");
    api->peek       = (peek_fn_t)       dlsym(api->handle, "booksim_peek");
    api->pop        = (pop_fn_t)        dlsym(api->handle, "booksim_pop");
    api->busy       = (busy_fn_t)       dlsym(api->handle, "booksim_busy");
    api->destroy    = (destroy_fn_t)    dlsym(api->handle, "booksim_destroy");
    return api;
}

static void unload_intersim(intersim_api_t* api) {
    if (!api) return;
    if (api->handle) dlclose(api->handle);
    free(api);
}

// ---- C 层包装 ----
static inline void* call_create(intersim_api_t* api, const char* cfg, int sms, int mems) {
    return api->create(cfg, sms, mems);
}
static inline int call_caninject(intersim_api_t* api, void* net, int node, int n_flits) {
    return api->can_inject(net, node, n_flits);
}
static inline void call_inject(intersim_api_t* api, void* net, int src, int dst, unsigned long long pkt_id, int msg_type, int size_bytes) {
    api->inject(net, src, dst, pkt_id, msg_type, size_bytes);
}
static inline void call_cycle(intersim_api_t* api, void* net) {
    api->cycle(net);
}
static inline int call_peek(intersim_api_t* api, void* net, int node, unsigned long long* pkt_id_out) {
    return api->peek(net, node, pkt_id_out);
}
static inline void call_pop(intersim_api_t* api, void* net, int node) {
    api->pop(net, node);
}
static inline int call_busy(intersim_api_t* api, void* net) {
    return api->busy(net);
}
static inline void call_destroy(intersim_api_t* api, void* net) {
    api->destroy(net);
}
*/
import "C"

import (
	"fmt"
	"log"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/util/tracing"
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

// CreateNetworkWithLib initializes the BookSim network
func (noc *BookSimNoC) CreateNetworkWithLib(
	lib string,
	config string,
) {
	noc.wrapper = NewNetworkWrapperWithLib(lib, config, noc.MaxNumSMSidePort, noc.MaxNumMemSidePort)

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

// PlugInSMSide connects an external port to a specific BookSim node
func (noc *BookSimNoC) PlugInMagicSMSide(p akita.Port, size int) {
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

	var conn akita.Connection

	if strings.Contains(p.Name(), "TLB") {
		conn = NewMagicConnection(fmt.Sprintf("BookSimMagicSMSideConn[%d]", nextID), noc.Engine, 1*akita.GHz)
	} else {
		conn = NewBookSimConnection(fmt.Sprintf("BookSimSMSideConn[%d]", nextID), noc.Engine, 1*akita.GHz)
	}

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

// PlugInMemSide connects an external port to a specific BookSim node
func (noc *BookSimNoC) PlugInMagicMemSide(p akita.Port, size int) {
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

	var conn akita.Connection

	if strings.Contains(p.Name(), "TLB") {
		conn = NewMagicConnection(fmt.Sprintf("BookSimMagicMemSideConn[%d]", nextID), noc.Engine, 1*akita.GHz)
	} else {
		conn = NewBookSimConnection(fmt.Sprintf("BookSimMemSideConn[%d]", nextID), noc.Engine, 1*akita.GHz)
	}
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

			tracing.TraceReqInitiate(
				msg,
				now,
				noc,
				tracing.MsgIDAtReceiver(msg, noc),
			)

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

			tracing.AddTaskStep(
				tracing.MsgIDAtReceiver(msg, noc),
				now,
				noc,
				fmt.Sprintf("%d:L1ToL2Noc:%d", srcNode, dstNode),
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
				panic("BookSimNoC: unknown packet ID")
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
	if node, exists := noc.port2Node[m.Meta().Dst]; exists {
		return node
	}
	panic("BookSimNoC: dst port not mapped to node")
}

// ---- Wrapper ----

type NetworkWrapper struct {
	net       unsafe.Pointer
	api       *C.intersim_api_t
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
	return NewNetworkWrapperWithLib("/Users/chenzihang/codes/CachePWQ/simulator/noc/networking/booksim/native/libintersim.dylib", config, numCUs, numMems)
}

func NewNetworkWrapperWithLib(
	libPath string,
	config string,
	nSMS int,
	nMems int,
) *NetworkWrapper {
	if libPath == "" || config == "" {
		panic("BookSimNoC: libPath/config required")
	}

	cLib := C.CString(libPath)
	defer C.free(unsafe.Pointer(cLib))
	api := C.load_intersim(cLib)
	if api == nil {
		panic(fmt.Sprintf("[BookSimNoC] dlopen failed for %s", libPath))
	}

	cCfg := C.CString(config)
	defer C.free(unsafe.Pointer(cCfg))
	net := C.call_create(api, cCfg, C.int(nSMS), C.int(nMems))
	if net == nil {
		C.unload_intersim(api)
		panic(fmt.Sprintf("[BookSimNoC] booksim_create failed for %s", libPath))
	}

	return &NetworkWrapper{net: net, api: api}
}

func (wrapper *NetworkWrapper) CanInject(
	node int,
	numFlits int,
) bool {
	return C.call_caninject(wrapper.api, wrapper.net, C.int(node), C.int(numFlits)) != 0
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

	C.call_inject(
		wrapper.api,
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
	C.call_cycle(wrapper.api, wrapper.net)
}

func (wrapper *NetworkWrapper) Pop(
	node int,
) {
	C.call_pop(wrapper.api, wrapper.net, C.int(node))
}

func (wrapper *NetworkWrapper) Peek(
	node int,
) (bool, uint64) {
	var packetID C.ulonglong
	ok := C.call_peek(wrapper.api, wrapper.net, C.int(node), &packetID)
	if ok == 0 {
		return false, 0
	}
	return true, uint64(packetID)
}

func (wrapper *NetworkWrapper) Busy() bool {
	return C.call_busy(wrapper.api, wrapper.net) != 0
}

func (wrapper *NetworkWrapper) Open() bool {
	return wrapper.net != nil
}

func (wrapper *NetworkWrapper) Close() {
	if wrapper.net != nil {
		C.call_destroy(wrapper.api, wrapper.net)
		wrapper.net = nil
	}
}
