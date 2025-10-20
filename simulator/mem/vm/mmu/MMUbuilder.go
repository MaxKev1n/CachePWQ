package mmu

import (
	"fmt"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem/cache/writeback"
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/akitaext"
)

// A MMUBuilder can build MMU component
type MMUBuilder struct {
	engine                   akita.Engine
	freq                     akita.Freq
	log2PageSize             uint64
	pageTable                *device.PageTableImpl
	migrationServiceProvider akita.Port
	maxNumReqInFlight        int
	pageWalkingLatency       int
	numChiplets              uint64
	//	lowAddr                  uint64
	//	totMem                   uint64
	//	bankSize                 uint64
	//	numMemoryBanksPerChiplet uint64
}

// MakeBuilder creates a new builder
func MakeMMUBuilder() MMUBuilder {
	return MMUBuilder{
		freq:              1 * akita.GHz,
		log2PageSize:      12,
		maxNumReqInFlight: 8, //16,
	}
}

// WithEngine sets the engine to be used with the MMU
func (b MMUBuilder) WithEngine(engine akita.Engine) MMUBuilder {
	b.engine = engine
	return b
}

// WithFreq sets the frequency that the MMU to work at
func (b MMUBuilder) WithFreq(freq akita.Freq) MMUBuilder {
	b.freq = freq
	return b
}

// WithLog2PageSize sets the page size that the mmu support.
func (b MMUBuilder) WithLog2PageSize(log2PageSize uint64) MMUBuilder {
	b.log2PageSize = log2PageSize
	return b
}

// WithPageTable sets the page table that the MMU uses.
func (b MMUBuilder) WithPageTable(pageTable *device.PageTableImpl) MMUBuilder {
	b.pageTable = pageTable
	return b
}

/*
// WithMigrationServiceProvider sets the destination port that can perform
// page migration.
func (b MMUBuilder) WithMigrationServiceProvider(p akita.Port) MMUBuilder {
	b.migrationServiceProvider = p
	return b
}
*/
// WithMaxNumReqInFlight sets the number of requests can be concurrently
// processed by the MMU.
func (b MMUBuilder) WithMaxNumReqInFlight(n int) MMUBuilder {
	b.maxNumReqInFlight = n
	return b
}

/*
// WithPageWalkingLatency sets the number of cycles required for walking a page
// table.
func (b MMUBuilder) WithPageWalkingLatency(n int) MMUBuilder {
	b.pageWalkingLatency = n
	return b
}
*/
// WithNumChiplets sets the number of cycles required for walking a page
// table.
func (b MMUBuilder) WithNumChiplets(n uint64) MMUBuilder {
	b.numChiplets = n
	return b
}

/*
// WithLowAddr sets the number of cycles required for walking a page
// table.
func (b MMUBuilder) WithLowAddr(la uint64) MMUBuilder {
	b.lowAddr = la
	return b
}

// WithTotMem sets the number of cycles required for walking a page
// table.
func (b MMUBuilder) WithTotMem(ha uint64) MMUBuilder {
	b.totMem = ha
	return b
}

// WithBankSize sets the number of cycles required for walking a page
// table.
func (b MMUBuilder) WithBankSize(n uint64) MMUBuilder {
	b.bankSize = n
	return b
}

// WithNumMemoryBankPerChiplet sets the number of cycles required for walking a page
// table.
func (b MMUBuilder) WithNumMemoryBankPerChiplet(n uint64) MMUBuilder {
	b.numMemoryBanksPerChiplet = n
	return b
}
*/
// Build returns a newly created MMU component
func (b MMUBuilder) Build(name string) MMU {
	mmu := new(MMUImpl)
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
	mmu.maxRequestsInFlight = b.maxNumReqInFlight
	fmt.Println("num walkers:", mmu.maxRequestsInFlight)
	//mmu.latency = b.pageWalkingLatency
	//mmu.PageAccesedByDeviceID = make(map[uint64][]uint64)
	pageWalkCacheBuilder := writeback.MakePageWalkCacheBuilder().
		WithEngine(b.engine).
		WithLog2PageSize(b.log2PageSize).
		WithBitsPerLevel(9)
	pageWalkCache := pageWalkCacheBuilder.Build("PageWalkCache")
	mmu.PageWalkCache = pageWalkCache.TopPort
	mmu.pageWalkCachePort = akita.NewLimitNumMsgPort(mmu, 4096, name+".ToTop")
	mmuToPageWalkCache := akita.NewDirectConnection("MMUToPageWalkCache", b.engine, b.freq)
	mmuToPageWalkCache.PlugIn(pageWalkCache.TopPort, 4)
	mmuToPageWalkCache.PlugIn(mmu.pageWalkCachePort, 4)
	mmu.sendStateInfo = false
	return mmu
}
