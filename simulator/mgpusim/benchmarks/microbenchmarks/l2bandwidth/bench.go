package l2bandwidth

import (
	"log"

	"gitlab.com/akita/mgpusim/driver"
	"gitlab.com/akita/mgpusim/insts"
	"gitlab.com/akita/mgpusim/kernels"
)

// KernelArgs 必须与 OpenCL Kernel 中的参数顺序和类型严格一致
type KernelArgs struct {
	SrcArray            driver.GPUPtr
	DstArray            driver.GPUPtr
	Stride              uint32
	HiddenGlobalOffsetX int64
	HiddenGlobalOffsetY int64
	HiddenGlobalOffsetZ int64
}

type Benchmark struct {
	driver  *driver.Driver
	context *driver.Context
	gpus    []int
	queues  []*driver.CommandQueue
	kernel  *insts.HsaCo

	useUnifiedMemory bool

	length uint32 // 线程数量 (Global Work Size)
	stride uint32 // 元素步长

	sArray driver.GPUPtr
	dArray driver.GPUPtr
}

func NewBenchmark(driver *driver.Driver) *Benchmark {
	b := new(Benchmark)
	b.driver = driver
	b.context = driver.Init()
	b.loadProgram()

	// 硬件参数配置
	numBanks := uint32(32)
	striping := uint32(4096) // 4KB

	b.stride = (numBanks * striping) / 4

	// 设置线程数。1024 个线程足以产生足够的并发来掩盖 L2 延迟
	b.length = 64 * 4

	return b
}

func (b *Benchmark) loadProgram() {
	hsacoBytes := _escFSMustByte(false, "/kernels.hsaco")
	b.kernel = kernels.LoadProgramFromMemory(hsacoBytes, "l2_slice_bandwidth")
	if b.kernel == nil {
		log.Panic("Failed to load kernel binary")
	}
}

func (b *Benchmark) Verify() {
	panic("implement me")
}

func (b *Benchmark) SetUnifiedMemory() {
	b.useUnifiedMemory = true
}

func (b *Benchmark) SetLASPMemoryAlloc() {
	panic("implement me")
}

func (b *Benchmark) SetLASPHSLMemoryAlloc() {
	panic("implement me")
}

func (b *Benchmark) initMem() {
	size := uint64(b.length) * uint64(b.stride) * 4

	b.sArray = b.driver.AllocateMemory(b.context, size*2)
	b.dArray = b.sArray + driver.GPUPtr(size)
}

func (b *Benchmark) exec() {
	for _, queue := range b.queues {
		kernArg := KernelArgs{
			b.sArray,
			b.dArray,
			b.stride,
			0, 0, 0,
		}

		b.driver.EnqueueLaunchKernel(
			queue, b.kernel,
			[3]uint32{b.length, 1, 1},
			[3]uint16{64, 1, 1},
			&kernArg,
		)
		b.driver.DrainCommandQueue(queue)
	}
}

// SelectGPU 配置 GPU 和队列
func (b *Benchmark) SelectGPU(gpuIDs []int) {
	b.gpus = gpuIDs
	b.queues = make([]*driver.CommandQueue, 0, len(b.gpus))
	for _, gpu := range b.gpus {
		b.driver.SelectGPU(b.context, gpu)
		b.queues = append(b.queues, b.driver.CreateCommandQueue(b.context))
	}
}

func (b *Benchmark) Run() {
	b.initMem()
	b.exec()
}
