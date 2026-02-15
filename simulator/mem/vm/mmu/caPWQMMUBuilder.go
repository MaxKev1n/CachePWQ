package mmu

import (
	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem/cache/writeback"
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/akitaext"
)

// A CaPWQMMUBuilder can build MMU component
type CaPWQMMUBuilder struct {
	engine                   akita.Engine
	freq                     akita.Freq
	log2PageSize             uint64
	pageTable                *device.PageTableImpl
	migrationServiceProvider akita.Port
	maxNumReqInFlight        int
	log2CacheLineSize        uint64
	pageWalkCacheSize        uint64
}

// MakeBuilder creates a new builder
func MakeCaPWQMMUBuilder() CaPWQMMUBuilder {
	return CaPWQMMUBuilder{
		freq:              1 * akita.GHz,
		log2PageSize:      12,
		maxNumReqInFlight: 8, //16,
		log2CacheLineSize: 6,
		pageWalkCacheSize: 512, //256, //bytes
	}
}

// WithEngine sets the engine to be used with the MMU
func (b CaPWQMMUBuilder) WithEngine(engine akita.Engine) CaPWQMMUBuilder {
	b.engine = engine
	return b
}

// WithFreq sets the frequency that the MMU to work at
func (b CaPWQMMUBuilder) WithFreq(freq akita.Freq) CaPWQMMUBuilder {
	b.freq = freq
	return b
}

// WithLog2PageSize sets the page size that the mmu support.
func (b CaPWQMMUBuilder) WithLog2PageSize(log2PageSize uint64) CaPWQMMUBuilder {
	b.log2PageSize = log2PageSize
	return b
}

// WithPageTable sets the page table that the MMU uses.
func (b CaPWQMMUBuilder) WithPageTable(pageTable *device.PageTableImpl) CaPWQMMUBuilder {
	b.pageTable = pageTable
	return b
}

// WithMaxNumReqInFlight sets the number of requests can be concurrently
// processed by the MMU.
func (b CaPWQMMUBuilder) WithMaxNumReqInFlight(n int) CaPWQMMUBuilder {
	b.maxNumReqInFlight = n
	return b
}

// WithPageWalkCacheSize sets the size of the page walk cache.
func (b CaPWQMMUBuilder) WithPageWalkCacheSize(size uint64) CaPWQMMUBuilder {
	b.pageWalkCacheSize = size
	return b
}

// WithLog2CacheLineSize sets the cache line size of the cache connected to
// the MMU.
func (b CaPWQMMUBuilder) WithLog2CacheLineSize(
	log2CacheLineSize uint64,
) CaPWQMMUBuilder {
	b.log2CacheLineSize = log2CacheLineSize
	return b
}

// Build returns a newly created MMU component
func (b CaPWQMMUBuilder) Build(name string) MMU {
	mmu := new(CaPWQMMU)
	mmu.TickingComponent = *akita.NewTickingComponent(
		name, b.engine, b.freq, mmu)

	mmu.ToTop = akita.NewLimitNumMsgPort(mmu, 4096, name+".ToTop")
	mmu.ToCache = akita.NewLimitNumMsgPort(mmu, 16, name+".ToCache")
	mmu.TranslationPort = akita.NewLimitNumMsgPort(mmu, 16, name+".TranslationPort")

	mmu.topSender = akitaext.NewBufferedSender(mmu.ToTop, util.NewBuffer(16))
	if b.pageTable != nil {
		mmu.pageTable = b.pageTable
	} else {
		panic("no page table!")
	}

	mmu.maxPageWalkQueueSize = 8 * b.maxNumReqInFlight
	mmu.pageWalkers = make([]*CaPWQPageWalker, 0, b.maxNumReqInFlight)
	for i := 0; i < b.maxNumReqInFlight; i++ {
		walker := newCaPWQPageWalker(mmu, i)

		mmu.pageWalkers = append(mmu.pageWalkers, walker)
	}

	pageWalkCacheBuilder := writeback.MakePageWalkCacheBuilder().
		WithEngine(b.engine).
		WithLog2PageSize(b.log2PageSize).
		WithBitsPerLevel(9).
		WithByteSize(b.pageWalkCacheSize)
	pageWalkCache := pageWalkCacheBuilder.Build("PageWalkCache")
	mmu.PageWalkCache = pageWalkCache.TopPort
	mmu.ToPageWalkCache = akita.NewLimitNumMsgPort(mmu, 4096, name+".ToTop")
	mmuToPageWalkCache := akita.NewDirectConnection("MMUToPageWalkCache", b.engine, b.freq)
	mmuToPageWalkCache.PlugIn(pageWalkCache.TopPort, 4)
	mmuToPageWalkCache.PlugIn(mmu.ToPageWalkCache, 4)

	mmu.log2CacheLineSize = b.log2CacheLineSize

	return mmu
}
