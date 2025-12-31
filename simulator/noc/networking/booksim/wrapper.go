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
	"sync/atomic"
	"unsafe"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/mem/device"
)

// ---- Wrapper ----

type NetworkWrapper struct {
	net       unsafe.Pointer
	api       *C.intersim_api_t
	generator NoCIDGenerator
}

type NoCIDGenerator struct {
	nextID uint64
}

func (g *NoCIDGenerator) Generate() uint64 {
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
