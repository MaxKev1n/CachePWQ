package mmu

import (
	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/mem/cache/writeback"
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/akitaext"
)

// A IdealMMUBuilder can build MMU component
type IdealMMUBuilder struct {
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
func MakeIdealMMUBuilder() IdealMMUBuilder {
	return IdealMMUBuilder{
		freq:              1 * akita.GHz,
		log2PageSize:      12,
		maxNumReqInFlight: 8, //16,
	}
}

// WithEngine sets the engine to be used with the MMU
func (b IdealMMUBuilder) WithEngine(engine akita.Engine) IdealMMUBuilder {
	b.engine = engine
	return b
}

// WithFreq sets the frequency that the MMU to work at
func (b IdealMMUBuilder) WithFreq(freq akita.Freq) IdealMMUBuilder {
	b.freq = freq
	return b
}

// WithLog2PageSize sets the page size that the mmu support.
func (b IdealMMUBuilder) WithLog2PageSize(log2PageSize uint64) IdealMMUBuilder {
	b.log2PageSize = log2PageSize
	return b
}

// WithPageTable sets the page table that the MMU uses.
func (b IdealMMUBuilder) WithPageTable(pageTable *device.PageTableImpl) IdealMMUBuilder {
	b.pageTable = pageTable
	return b
}

/*
// WithMigrationServiceProvider sets the destination port that can perform
// page migration.
func (b IdealMMUBuilder) WithMigrationServiceProvider(p akita.Port) IdealMMUBuilder {
	b.migrationServiceProvider = p
	return b
}
*/
// WithMaxNumReqInFlight sets the number of requests can be concurrently
// processed by the MMU.
func (b IdealMMUBuilder) WithMaxNumReqInFlight(n int) IdealMMUBuilder {
	b.maxNumReqInFlight = n
	return b
}

/*
// WithPageWalkingLatency sets the number of cycles required for walking a page
// table.
func (b IdealMMUBuilder) WithPageWalkingLatency(n int) IdealMMUBuilder {
	b.pageWalkingLatency = n
	return b
}
*/
// WithNumChiplets sets the number of cycles required for walking a page
// table.
func (b IdealMMUBuilder) WithNumChiplets(n uint64) IdealMMUBuilder {
	b.numChiplets = n
	return b
}

/*
// WithLowAddr sets the number of cycles required for walking a page
// table.
func (b IdealMMUBuilder) WithLowAddr(la uint64) IdealMMUBuilder {
	b.lowAddr = la
	return b
}

// WithTotMem sets the number of cycles required for walking a page
// table.
func (b IdealMMUBuilder) WithTotMem(ha uint64) IdealMMUBuilder {
	b.totMem = ha
	return b
}

// WithBankSize sets the number of cycles required for walking a page
// table.
func (b IdealMMUBuilder) WithBankSize(n uint64) IdealMMUBuilder {
	b.bankSize = n
	return b
}

// WithNumMemoryBankPerChiplet sets the number of cycles required for walking a page
// table.
func (b IdealMMUBuilder) WithNumMemoryBankPerChiplet(n uint64) IdealMMUBuilder {
	b.numMemoryBanksPerChiplet = n
	return b
}
*/
// Build returns a newly created MMU component
func (b IdealMMUBuilder) Build(name string) MMU {
	mmu := new(IdealMMU)
	mmu.TickingComponent = *akita.NewTickingComponent(
		name, b.engine, b.freq, mmu)
	//mmu.migrationQueueSize = 4096

	mmu.ToTop = akita.NewLimitNumMsgPort(mmu, 4096, name+".ToTop")
	mmu.ControlPort = akita.NewLimitNumMsgPort(mmu, 1, name+".ControlPort")

	//mmu.MigrationPort = akita.NewLimitNumMsgPort(mmu, 1, name+".MigrationPort")
	//might want to change capacity later
	mmu.TranslationPort = akita.NewLimitNumMsgPort(mmu, 4096, name+".TranslationPort")
	//mmu.MigrationServiceProvider = b.migrationServiceProvider

	mmu.topSender = akitaext.NewBufferedSender(mmu.ToTop, util.NewBuffer(4096))
	if b.pageTable != nil {
		mmu.pageTable = b.pageTable
	} else {
		panic("no page table!")
	}

	mmu.inflightPWCRequests = make(map[string]*transaction)
	mmu.inflightMemRequests = make([]*mem.ReadReq, 0)
	mmu.mappingMemAccess = make(map[string]*transaction)

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
