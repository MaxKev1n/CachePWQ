package mmu

import (
	"fmt"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/mem/cache/writeback"
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/akitaext"
)

// A caPWQMMUBuilder can build MMU component
type caPWQMMUBuilder struct {
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
func MakecaPWQMMUBuilder() caPWQMMUBuilder {
	return caPWQMMUBuilder{
		freq:              1 * akita.GHz,
		log2PageSize:      12,
		maxNumReqInFlight: 8, //16,
	}
}

// WithEngine sets the engine to be used with the MMU
func (b caPWQMMUBuilder) WithEngine(engine akita.Engine) caPWQMMUBuilder {
	b.engine = engine
	return b
}

// WithFreq sets the frequency that the MMU to work at
func (b caPWQMMUBuilder) WithFreq(freq akita.Freq) caPWQMMUBuilder {
	b.freq = freq
	return b
}

// WithLog2PageSize sets the page size that the mmu support.
func (b caPWQMMUBuilder) WithLog2PageSize(log2PageSize uint64) caPWQMMUBuilder {
	b.log2PageSize = log2PageSize
	return b
}

// WithPageTable sets the page table that the MMU uses.
func (b caPWQMMUBuilder) WithPageTable(pageTable *device.PageTableImpl) caPWQMMUBuilder {
	b.pageTable = pageTable
	return b
}

/*
// WithMigrationServiceProvider sets the destination port that can perform
// page migration.
func (b caPWQMMUBuilder) WithMigrationServiceProvider(p akita.Port) caPWQMMUBuilder {
	b.migrationServiceProvider = p
	return b
}
*/
// WithMaxNumReqInFlight sets the number of requests can be concurrently
// processed by the MMU.
func (b caPWQMMUBuilder) WithMaxNumReqInFlight(n int) caPWQMMUBuilder {
	b.maxNumReqInFlight = n
	return b
}

/*
// WithPageWalkingLatency sets the number of cycles required for walking a page
// table.
func (b caPWQMMUBuilder) WithPageWalkingLatency(n int) caPWQMMUBuilder {
	b.pageWalkingLatency = n
	return b
}
*/
// WithNumChiplets sets the number of cycles required for walking a page
// table.
func (b caPWQMMUBuilder) WithNumChiplets(n uint64) caPWQMMUBuilder {
	b.numChiplets = n
	return b
}

/*
// WithLowAddr sets the number of cycles required for walking a page
// table.
func (b caPWQMMUBuilder) WithLowAddr(la uint64) caPWQMMUBuilder {
	b.lowAddr = la
	return b
}

// WithTotMem sets the number of cycles required for walking a page
// table.
func (b caPWQMMUBuilder) WithTotMem(ha uint64) caPWQMMUBuilder {
	b.totMem = ha
	return b
}

// WithBankSize sets the number of cycles required for walking a page
// table.
func (b caPWQMMUBuilder) WithBankSize(n uint64) caPWQMMUBuilder {
	b.bankSize = n
	return b
}

// WithNumMemoryBankPerChiplet sets the number of cycles required for walking a page
// table.
func (b caPWQMMUBuilder) WithNumMemoryBankPerChiplet(n uint64) caPWQMMUBuilder {
	b.numMemoryBanksPerChiplet = n
	return b
}
*/
// Build returns a newly created MMU component
func (b caPWQMMUBuilder) Build(name string) MMU {
	mmu := new(caPWQMMU)
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

	for i := 0; i < mmu.maxRequestsInFlight; i++ {
		walker := new(caPWQPageWalker)

		walker.mmu = mmu
		walker.queue = make([]*transaction, 0)

		mmu.pageWalkers = append(mmu.pageWalkers, walker)
	}

	mmu.nextPointer = 0
	mmu.queueCapacity = 8

	mmu.inflightPWCRequests = make(map[string]*transaction)
	mmu.inflightMemRequests = make([]*mem.ReadReq, 0)
	mmu.mappingMemAccess = make(map[string]*transaction)
	mmu.maxMemRequestsInFlight = 512

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

	mmu.tickingBuffer = NewTickingBuffer(
		b.engine,
		b.freq,
	)
	mmu.tickingBuffer.mmu = mmu

	mmu.ToBuffer = akita.NewLimitNumMsgPort(mmu, 4096, name+".ToBuffer")
	mmu.BufferPort = mmu.tickingBuffer.ToTop

	mmuToBuffer := akita.NewDirectConnection("MMUToBuffer", b.engine, b.freq)
	mmuToBuffer.PlugIn(mmu.tickingBuffer.ToTop, 8)
	mmuToBuffer.PlugIn(mmu.ToBuffer, 8)

	mmu.pendingRspFromBuffer = make(map[string]*mem.DataReadyRsp)

	return mmu
}
