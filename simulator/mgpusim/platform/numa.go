package platform

import (
	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/mgpusim/builders"
	"gitlab.com/akita/mgpusim/driver"
)

// NUMAPlatformBuilder can build a platform that equips DisTLBGPU GPU.
type NUMAPlatformBuilder struct {
	CommonPlatformBuilder
}

// MakeNUMAPlatformBuilder creates a EmuBuilder with default parameters.
func MakeNUMAPlatformBuilder() NUMAPlatformBuilder {
	b := NUMAPlatformBuilder{
		CommonPlatformBuilder{
			numGPU:                   1,
			log2PageSize:             uint64(12),
			numCUPerShaderArray:      uint64(4),
			numShaderArrayPerChiplet: uint64(16),
			numMemoryBankPerChiplet:  uint64(64),
			numChiplets:              uint64(1),
			totalMem:                 16 * mem.GB,
			bankSize:                 256 * mem.MB,
			lowAddr:                  4 * mem.GB,
		}}
	return b
}

func (b NUMAPlatformBuilder) Build() (akita.Engine, *driver.Driver) {
	engine := b.createEngine()

	gpuDriver := driver.NewDriver(engine, b.log2PageSize, b.memAllocatorType)
	gpuBuilder := b.createGPUBuilder(engine, gpuDriver)
	pcieConnector, rootComplexID :=
		b.createConnection(engine, gpuDriver)

	rdmaAddressTable := b.createRDMAAddrTable()

	pmcAddressTable := b.createPMCPageTable()

	b.createGPUs(
		rootComplexID, pcieConnector,
		gpuBuilder, gpuDriver,
		rdmaAddressTable, pmcAddressTable)

	return engine, gpuDriver
}

func (b *NUMAPlatformBuilder) createGPUBuilder(
	engine akita.Engine,
	gpuDriver *driver.Driver,
) builders.Builder {
	gpuBuilder := builders.MakeNUMAGPUBuilder()
	gpuBuilder.WithEngine(engine)
	gpuBuilder.WithNumCUPerShaderArray(int(b.numCUPerShaderArray))
	gpuBuilder.WithNumShaderArrayPerChiplet(int(b.numShaderArrayPerChiplet))
	gpuBuilder.WithNumMemoryBankPerChiplet(int(b.numMemoryBankPerChiplet))
	gpuBuilder.WithNumChiplet(int(b.numChiplets))
	gpuBuilder.WithTotalMem(b.totalMem)
	gpuBuilder.CalculateMemoryParameters()
	gpuBuilder.WithLog2PageSize(b.log2PageSize)
	gpuBuilder.WithPageTable(gpuDriver.PageTable)
	gpuBuilder.WithAlg(b.alg)
	gpuBuilder.WithSchedulingPartition(b.partition)
	gpuBuilder.WithBooksimGlobal(b.booksimGlobal)
	gpuBuilder.WithBookSimDir(b.booksimDir)

	if b.useTimeInstProfiling {
		gpuBuilder.UseTimeInstProfiling()
	}

	if b.useTimeEventAnalysis {
		gpuBuilder.UseTimeEventAnalysis()
	}

	if b.useTLBMonitor {
		gpuBuilder.WithTLBMonitor()
	}

	b.setVisTracer(gpuDriver, gpuBuilder)
	b.setTLBTracer(gpuBuilder)
	b.setMemTracer(gpuBuilder)
	b.setISADebugger(gpuBuilder)

	if b.disableProgressBar {
		gpuBuilder.WithoutProgressBar()
	}

	return gpuBuilder
}
