package power

/*
#include "gpuwattch_lib.h"
#include "power_interface.h"
#include <stdlib.h>
*/
import "C"
import (
	"log"
	"sync"
	"unsafe"

	"gitlab.com/akita/akita"
)

var (
	Model     *PowerModel
	ModelLock sync.Mutex
)

type Stat struct {
	cStat C.PowerStatC
}

type ModelComp interface {
	UpdateStats(now akita.VTimeInSec) akita.VTimeInSec
}

// PowerModel 是暴露给 MGPUSim 使用的 Go 对象
// 它内部持有 C++ 对象的指针
type PowerModel struct {
	*akita.TickingComponent

	handle C.WattchHandle // 这本质上就是 unsafe.Pointer
	name   string
	config string

	start akita.VTimeInSec
	end   akita.VTimeInSec

	tracer *PowerStatTracer

	CUs []ModelComp
}

// NewNativeModel 相当于构造函数
func NewPowerModel(
	configPath string,
	engine akita.Engine,
) {
	ModelLock.Lock()
	defer ModelLock.Unlock()

	cPath := C.CString(configPath)
	defer C.free(unsafe.Pointer(cPath))

	log.Println("[Go] Requesting C++ object creation...")

	// 调用 C 接口，拿到指针
	ptr := C.Wattch_New(cPath)

	model := &PowerModel{
		handle: ptr,
		name:   "GCN_Power_Model",
		config: configPath,
	}
	model.TickingComponent = akita.NewTickingComponent(
		model.name,
		engine,
		1*akita.MHz,
		model,
	)

	Model = model
}

func Start(now akita.VTimeInSec) {
	if Model == nil {
		return
	}

	if Model.start != 0 {
		Model.reset(now)
	}

	Model.start = now
	Model.TickLater(now)
	log.Printf("[Go] Power model started at time %.12f\n", now)
}

func End(now akita.VTimeInSec) {
	if Model == nil {
		return
	}

	if Model.end != 0 {
		return
	}

	Model.end = now
	log.Printf("[Go] Power model ending at time %.12f\n", now)
	Model.Close()
}

func (m *PowerModel) reset(now akita.VTimeInSec) {
	ModelLock.Lock()
	defer ModelLock.Unlock()

	log.Println("[Go] Resetting power model...")

	cPath := C.CString(m.config)
	defer C.free(unsafe.Pointer(cPath))

	m.handle = C.Wattch_New(cPath)

	if m.tracer != nil {
		m.tracer.Reset()
	}
	m.end = 0
	m.start = now

	m.TickLater(now)
}

func (m *PowerModel) Tick(now akita.VTimeInSec) bool {
	if m.end != 0 && now >= m.end {
		return false
	}

	m.UpdatePowerStat()
	m.Cycle(now)

	return true
}

// UpdatePowerStat 是 Go 的方法，内部转发给 C++
func (m *PowerModel) UpdatePowerStat() {
	ModelLock.Lock()
	defer ModelLock.Unlock()

	stat := m.NewPowerStat()

	C.Wattch_Update(m.handle, stat.cStat)
}

// GetPower 获取当前功率
func (m *PowerModel) GetPower() float64 {
	ModelLock.Lock()
	defer ModelLock.Unlock()

	val := C.Wattch_Get_Power(m.handle)
	return float64(val)
}

// Close 手动释放 C++ 内存 (必须显式调用，因为 Go GC 管不到 C++ 堆内存)
func (m *PowerModel) Close() {
	ModelLock.Lock()
	defer ModelLock.Unlock()

	if m.handle != nil {
		log.Println("[Go] Releasing C++ object...")
		C.Wattch_Delete(m.handle)
		m.handle = nil
	}
}

// Cycle 模拟一个MCPAT时钟周期
func (m *PowerModel) Cycle(now akita.VTimeInSec) {
	ModelLock.Lock()
	defer ModelLock.Unlock()

	log.Printf("[Go] Cycled@%.12f!", now)
	C.Wattch_Cycle(m.handle)
}

// SetPowerStatTracer sets the tracer for power model
func (m *PowerModel) SetPowerStatTracer(tracer *PowerStatTracer) {
	m.tracer = tracer
}

func (m *PowerModel) NewPowerStat() Stat {
	cStat := C.PowerStatC{}

	for name, value := range m.tracer.stepCount {
		switch name {
		case "num_instructions":
			cStat.total_inst = C.uint64_t(value)
		case "num_fp_instructions":
			cStat.fp_inst = C.uint64_t(value)
		case "num_int_instructions":
			cStat.int_inst = C.uint64_t(value)
		case "register_reads":
			cStat.reg_reads = C.uint64_t(value)
		case "register_writes":
			cStat.reg_writes = C.uint64_t(value)
		case "register_non_reg_ops":
			cStat.reg_non_reg_ops = C.uint64_t(value)
		case "LDS_accesses":
			cStat.LDS_accesses = C.uint64_t(value)
		case "l1_read_hits":
			cStat.l1_read_hits = C.uint64_t(value)
		case "l1_read_misses":
			cStat.l1_read_misses = C.uint64_t(value)
		case "l1_write_hits":
			cStat.l1_write_hits = C.uint64_t(value)
		case "l1_write_misses":
			cStat.l1_write_misses = C.uint64_t(value)
		case "l1i_hits":
			cStat.l1i_hits = C.uint64_t(value)
		case "l1i_misses":
			cStat.l1i_misses = C.uint64_t(value)
		case "l1tlb_hits":
			cStat.l1tlb_hits = C.uint64_t(value)
		case "l1tlb_misses":
			cStat.l1tlb_misses = C.uint64_t(value)
		case "l2tlb_hits":
			cStat.l2tlb_hits = C.uint64_t(value)
		case "l2tlb_misses":
			cStat.l2tlb_misses = C.uint64_t(value)
		case "l3tlb_hits":
			cStat.l3tlb_hits = C.uint64_t(value)
		case "l3tlb_misses":
			cStat.l3tlb_misses = C.uint64_t(value)
		case "l2_read_hits":
			cStat.l2_read_hits = C.uint64_t(value)
		case "l2_read_misses":
			cStat.l2_read_misses = C.uint64_t(value)
		case "l2_write_hits":
			cStat.l2_write_hits = C.uint64_t(value)
		case "l2_write_misses":
			cStat.l2_write_misses = C.uint64_t(value)
		case "dram_reads":
			cStat.dram_reads = C.uint64_t(value)
		case "dram_writes":
			cStat.dram_writes = C.uint64_t(value)
		case "total_active_lanes":
			value = value / (4 * 16) / uint64(len(m.CUs))
			cStat.AveragePipeDutyCycle = C.double(value)
		case "fpu_accesses":
			cStat.FPU_accesses = C.uint64_t(value)
		case "iAlu_accesses":
			cStat.IAlu_accesses = C.uint64_t(value)
		case "SFU_accesses":
			cStat.SFU_accesses = C.uint64_t(value)
		case "num_active_threads":
			cStat.num_active_threads = C.uint64_t(value)
		case "num_active_wf":
			cStat.num_active_wf = C.uint64_t(value)
		case "active_sp_lanes":
			value = value / (4 * 16) / uint64(len(m.CUs))
			cStat.active_sp_lanes = C.uint64_t(value)
		case "active_sfu_lanes":
			value = value / (4 * 16) / uint64(len(m.CUs))
			cStat.active_sfu_lanes = C.uint64_t(value)
		case "num_flits_cu_to_mem":
			cStat.num_flits_cu_to_mem = C.uint64_t(value)
		case "num_flits_mem_to_cu":
			cStat.num_flits_mem_to_cu = C.uint64_t(value)
		default:
			log.Panicf("[Go] Warning: Unknown performance counter name %s\n", name)
		}
	}

	active_cus := akita.VTimeInSec(0)
	for _, cu := range m.CUs {
		active_cus += cu.UpdateStats(m.Engine.CurrentTime())
	}
	cStat.num_active_cus = C.uint64_t(1 * akita.GHz.Cycle(active_cus))

	return Stat{cStat: cStat}
}
