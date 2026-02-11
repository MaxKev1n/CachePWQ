package mmu

import (
	"fmt"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem/cache/writeback"
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/akitaext"
)

// A MPWMMUBuilder can build MMU component
type MPWMMUBuilder struct {
	engine                   akita.Engine
	freq                     akita.Freq
	log2PageSize             uint64
	pageTable                *device.PageTableImpl
	migrationServiceProvider akita.Port
	maxNumReqInFlight        int
	pageWalkingLatency       int
	numChiplets              uint64
	pageWalkCacheSize        uint64
	//	lowAddr                  uint64
	//	totMem                   uint64
	//	bankSize                 uint64
	//	numMemoryBanksPerChiplet uint64
}

// MakeBuilder creates a new builder
func MakeMPWMMUBuilder() MPWMMUBuilder {
	return MPWMMUBuilder{
		freq:              1 * akita.GHz,
		log2PageSize:      12,
		maxNumReqInFlight: 8,   //16,
		pageWalkCacheSize: 512, //256, //bytes
	}
}

// WithEngine sets the engine to be used with the MMU
func (b MPWMMUBuilder) WithEngine(engine akita.Engine) MPWMMUBuilder {
	b.engine = engine
	return b
}

// WithFreq sets the frequency that the MMU to work at
func (b MPWMMUBuilder) WithFreq(freq akita.Freq) MPWMMUBuilder {
	b.freq = freq
	return b
}

// WithLog2PageSize sets the page size that the mmu support.
func (b MPWMMUBuilder) WithLog2PageSize(log2PageSize uint64) MPWMMUBuilder {
	b.log2PageSize = log2PageSize
	return b
}

// WithPageTable sets the page table that the MMU uses.
func (b MPWMMUBuilder) WithPageTable(pageTable *device.PageTableImpl) MPWMMUBuilder {
	b.pageTable = pageTable
	return b
}

// WithPageWalkCacheSize sets the size of the page walk cache.
func (b MPWMMUBuilder) WithPageWalkCacheSize(n uint64) MPWMMUBuilder {
	b.pageWalkCacheSize = n
	return b
}

/*
// WithMigrationServiceProvider sets the destination port that can perform
// page migration.
func (b MPWMMUBuilder) WithMigrationServiceProvider(p akita.Port) MPWMMUBuilder {
	b.migrationServiceProvider = p
	return b
}
*/
// WithMaxNumReqInFlight sets the number of requests can be concurrently
// processed by the MMU.
func (b MPWMMUBuilder) WithMaxNumReqInFlight(n int) MPWMMUBuilder {
	b.maxNumReqInFlight = n
	return b
}

/*
// WithPageWalkingLatency sets the number of cycles required for walking a page
// table.
func (b MPWMMUBuilder) WithPageWalkingLatency(n int) MPWMMUBuilder {
	b.pageWalkingLatency = n
	return b
}
*/
// WithNumChiplets sets the number of cycles required for walking a page
// table.
func (b MPWMMUBuilder) WithNumChiplets(n uint64) MPWMMUBuilder {
	b.numChiplets = n
	return b
}

/*
// WithLowAddr sets the number of cycles required for walking a page
// table.
func (b MPWMMUBuilder) WithLowAddr(la uint64) MPWMMUBuilder {
	b.lowAddr = la
	return b
}

// WithTotMem sets the number of cycles required for walking a page
// table.
func (b MPWMMUBuilder) WithTotMem(ha uint64) MPWMMUBuilder {
	b.totMem = ha
	return b
}

// WithBankSize sets the number of cycles required for walking a page
// table.
func (b MPWMMUBuilder) WithBankSize(n uint64) MPWMMUBuilder {
	b.bankSize = n
	return b
}

// WithNumMemoryBankPerChiplet sets the number of cycles required for walking a page
// table.
func (b MPWMMUBuilder) WithNumMemoryBankPerChiplet(n uint64) MPWMMUBuilder {
	b.numMemoryBanksPerChiplet = n
	return b
}
*/
// Build returns a newly created MMU component
func (b MPWMMUBuilder) Build(name string) MMU {
	mmu := new(MPWMMU)
	mmu.TickingComponent = *akita.NewTickingComponent(
		name, b.engine, b.freq, mmu)
	//mmu.migrationQueueSize = 4096

	mmu.ToTop = akita.NewLimitNumMsgPort(mmu, 4096, name+".ToTop")
	mmu.ControlPort = akita.NewLimitNumMsgPort(mmu, 1, name+".ControlPort")

	//mmu.MigrationPort = akita.NewLimitNumMsgPort(mmu, 1, name+".MigrationPort")
	//might want to change capacity later
	mmu.TranslationPort = akita.NewLimitNumMsgPort(mmu, 16, name+".TranslationPort")
	//mmu.MigrationServiceProvider = b.migrationServiceProvider

	mmu.topSender = akitaext.NewBufferedSender(mmu.ToTop, util.NewBuffer(4096))
	if b.pageTable != nil {
		mmu.pageTable = b.pageTable
	} else {
		panic("no page table!")
	}
	//mmu.latency = b.pageWalkingLatency
	//mmu.PageAccesedByDeviceID = make(map[uint64][]uint64)
	pageWalkCacheBuilder := writeback.MakePageWalkCacheBuilder().
		WithEngine(b.engine).
		WithLog2PageSize(b.log2PageSize).
		WithBitsPerLevel(9).
		WithByteSize(b.pageWalkCacheSize)
	pageWalkCache := pageWalkCacheBuilder.Build("PageWalkCache")
	mmu.PageWalkCache = pageWalkCache.TopPort
	mmu.pageWalkCachePort = akita.NewLimitNumMsgPort(mmu, 4096, name+".ToTop")
	mmuToPageWalkCache := akita.NewDirectConnection("MMUToPageWalkCache", b.engine, b.freq)
	mmuToPageWalkCache.PlugIn(pageWalkCache.TopPort, 4)
	mmuToPageWalkCache.PlugIn(mmu.pageWalkCachePort, 4)
	mmu.sendStateInfo = false

	mmu.nextPointer = 0
	mmu.queueCapacity = 8

	for i := 0; i < b.maxNumReqInFlight; i++ {
		pageWalker := new(MPWPageWalker)

		pageWalker.mmu = mmu
		pageWalker.status = 0
		pageWalker.requestVector = make([]bool, mmu.queueCapacity)
		pageWalker.outstandingReqs = make(map[string]*Transaction)

		mmu.pageWalkers = append(mmu.pageWalkers, pageWalker)
	}
	fmt.Println("num walkers:", b.maxNumReqInFlight)

	mmu.inflightPWCRequests = make(map[string]*Transaction)
	mmu.mappingMemAccess = make(map[string]*Transaction)

	return mmu
}
