package builders

import (
	"fmt"
	"log"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem/cache"
	"gitlab.com/akita/mgpusim"
	"gitlab.com/akita/mgpusim/tip"
	noc "gitlab.com/akita/noc/networking/booksim"
	"gitlab.com/akita/noc/networking/chipnetwork"
)

type MonolithicGPUBuilder struct {
	*CommonBuilder

	// specific componenets
}

// Distributed TLB specific function

// MakeDistributedTLBGPUBuilder provides a GPU builder that can builds MCM GPU.
func MakeMonolithicGPUBuilder() MonolithicGPUBuilder {
	// TODO: should this be using new? is the object being allocated on the stack?
	cbp := CommonBuilder{}
	b := MonolithicGPUBuilder{CommonBuilder: &cbp}
	b.SetDefaultCommonBuilderParams()
	return b
}

func (b MonolithicGPUBuilder) Build(name string, id uint64) *mgpusim.GPU {
	b.createGPU(name, id)

	b.buildTEA()

	b.buildCP()

	chipRdmaAddressTable := b.createChipRDMAAddrTable()
	rdmaResponsePorts := make([]akita.Port, b.numChiplet)
	// remoteAddressTranslationTable := b.createRemoteAddrTransTable()
	// rtuResponsePorts := make([]akita.Port, 4)
	chipletName := fmt.Sprintf("%s.chiplet_%02d", b.gpuName, 0)
	chiplet := NewChiplet(chipletName, uint64(0))

	b.BuildSAs(chiplet)
	b.buildMemBanks(chiplet)
	b.buildMMU(chiplet)
	// TODO: this may have to be overridden
	b.buildL2TLB(chiplet)

	b.configChipRDMAEngine(chiplet, chipRdmaAddressTable, rdmaResponsePorts)
	// b.configRemoteAddressTranslationUnit(chiplet, remoteAddressTranslationTable, rtuResponsePorts)

	b.createL1ToL2NoC(chiplet)
	b.createL1TLBToL2TLBNoC(chiplet)

	b.connectL1ToL2NoC(chiplet)
	b.connectL2ToDRAM(chiplet)
	b.connectL1TLBToL2TLBNoC(chiplet)
	b.connectL2TLBTOMMU(chiplet)
	b.connectMMUToL2NoC(chiplet)

	b.chiplets = append(b.chiplets, chiplet)

	b.buildPageMigrationController()
	b.setupDMA()

	// b.setupMMUs()
	b.connectCP()
	b.setupInterchipNetwork()

	return b.gpu
}

func (b *MonolithicGPUBuilder) buildTEA() {
	b.teaEngine = tip.NewTimeEventAnalysisEngine(
		b.engine,
	)
}

func (b *MonolithicGPUBuilder) connectCP() {
	b.internalConn = akita.NewDirectConnection(
		b.gpuName+"InternalConn", b.engine, b.freq)
	b.gpu.InternalConnection = b.internalConn

	b.internalConn.PlugIn(b.cp.ToDriver, 1)
	b.internalConn.PlugIn(b.cp.ToDMA, 128)
	b.internalConn.PlugIn(b.cp.ToCaches, 128)
	b.internalConn.PlugIn(b.cp.ToCUs, 128)
	b.internalConn.PlugIn(b.cp.ToTLBs, 128)
	b.internalConn.PlugIn(b.cp.ToAddressTranslators, 128)
	b.internalConn.PlugIn(b.cp.ToRDMA, 4)
	b.internalConn.PlugIn(b.cp.ToPMC, 4)

	b.internalConn.PlugIn(b.cp.ToRTU, 4)
	b.internalConn.PlugIn(b.cp.ToMMUs, 4)

	b.cp.RDMA = b.rdmaEngine.CtrlPort
	b.internalConn.PlugIn(b.cp.RDMA, 1)

	b.cp.DMAEngine = b.dmaEngine.ToCP
	b.internalConn.PlugIn(b.dmaEngine.ToCP, 1)

	b.cp.PMC = b.pageMigrationController.CtrlPort
	b.internalConn.PlugIn(b.pageMigrationController.CtrlPort, 1)

	b.connectCPWithCUs()
	b.connectCPWithAddressTranslators()
	b.connectCPWithCaches()
	b.connectCPWithMMUs()
	b.connectCPWithTLBs()
}

func (b *MonolithicGPUBuilder) connectL1TLBToL2TLB(chiplet *Chiplet) {
	tlbConn := akita.NewDirectConnection(chiplet.name+"L1TLB-L2TLB",
		b.engine, b.freq)
	tlbConn.PlugIn(chiplet.L2TLBs[0].GetTopPort(), 64)

	var lowModuleFinder cache.LowModuleFinder

	singeLowModuleFinder := new(cache.SingleLowModuleFinder)
	singeLowModuleFinder.LowModule = chiplet.L2TLBs[0].GetTopPort()

	chiplet.L2TLBs[0].SetTLBFinder(singeLowModuleFinder)

	lowModuleFinder = singeLowModuleFinder

	for _, l1vTLB := range chiplet.L1VTLBs {
		l1vTLB.SetLowModuleFinder(lowModuleFinder)
		tlbConn.PlugIn(l1vTLB.GetBottomPort(), 16)
	}

	for _, l1iTLB := range chiplet.L1ITLBs {
		l1iTLB.SetLowModuleFinder(lowModuleFinder)
		tlbConn.PlugIn(l1iTLB.GetBottomPort(), 16)
	}

	for _, l1sTLB := range chiplet.L1STLBs {
		l1sTLB.SetLowModuleFinder(lowModuleFinder)
		tlbConn.PlugIn(l1sTLB.GetBottomPort(), 16)
	}
}

func (b *MonolithicGPUBuilder) createL1ToL2NoC(chiplet *Chiplet) {
	chiplet.L1ToL2NoC = noc.NewBookSimNoC(
		fmt.Sprintf("L1ToL2NoC[%d]", chiplet.ChipletID),
		b.engine,
	)

	b.gpu.NoCs = append(b.gpu.NoCs, chiplet.L1ToL2NoC)

	numSMSidePorts := 0
	numMemorySidePorts := 0

	for range chiplet.L1IAddrTranslator {
		numSMSidePorts++
	}

	for range chiplet.L1VCaches {
		numSMSidePorts++
	}

	for range chiplet.L1SCaches {
		numSMSidePorts++
	}

	for range chiplet.L2Caches {
		numMemorySidePorts++
	}

	// Monolithic MMU
	numSMSidePorts++

	chiplet.L1ToL2NoC.MaxNumSMSidePort = numSMSidePorts
	chiplet.L1ToL2NoC.MaxNumMemSidePort = numMemorySidePorts

	log.Printf("%s has %d SM side components and %d Mem side components\n",
		chiplet.L1ToL2NoC.Name(), numSMSidePorts, numMemorySidePorts)

	chiplet.L1ToL2NoC.CreateNetworkWithLib(
		b.booksimDir+"libintersim_memory.dylib", b.booksimMemory,
	)
}

func (b *MonolithicGPUBuilder) createL1TLBToL2TLBNoC(chiplet *Chiplet) {
	chiplet.L1TLBToL2TLBNoC = noc.NewBookSimNoC(
		fmt.Sprintf("L1TLBToL2TLBNoC[%d]", chiplet.ChipletID),
		b.engine,
	)

	b.gpu.NoCs = append(b.gpu.NoCs, chiplet.L1TLBToL2TLBNoC)

	numSMSidePorts := 0
	numMemorySidePorts := 0

	for range chiplet.L1ITLBs {
		numSMSidePorts++
	}

	for range chiplet.L1STLBs {
		numSMSidePorts++
	}

	for range chiplet.L1VTLBs {
		numSMSidePorts++
	}

	numMemorySidePorts++

	chiplet.L1TLBToL2TLBNoC.MaxNumSMSidePort = numSMSidePorts
	chiplet.L1TLBToL2TLBNoC.MaxNumMemSidePort = numMemorySidePorts

	log.Printf("%s has %d SM side components and %d Mem side components\n",
		chiplet.L1TLBToL2TLBNoC.Name(), numSMSidePorts, numMemorySidePorts)

	chiplet.L1TLBToL2TLBNoC.CreateNetworkWithLib(
		b.booksimDir+"libintersim_tlb.dylib", b.booksimTLB,
	)
}

func (b *MonolithicGPUBuilder) connectL1ToL2NoC(chiplet *Chiplet) {
	fmt.Println("memory address offset:", b.memAddrOffset)
	lowModuleFinder := cache.NewStripedLocalVRemoteLowModuleFinder(b.memAddrOffset, uint64(b.numChiplet*b.numMemoryBankPerChiplet),
		1<<b.log2MemoryBankInterleavingSize, uint64(b.numMemoryBankPerChiplet)*chiplet.ChipletID, uint64(b.numMemoryBankPerChiplet)*chiplet.ChipletID+uint64(b.numMemoryBankPerChiplet-1))
	lowModuleFinder.ModuleForOtherAddresses = chiplet.chipRdmaEngine.ToL1

	for _, l1v := range chiplet.L1VCaches {
		l1v.SetLowModuleFinder(lowModuleFinder)
		chiplet.L1ToL2NoC.PlugInSMSide(l1v.GetBottomPort(), 16)
	}

	for _, l1s := range chiplet.L1SCaches {
		l1s.SetLowModuleFinder(lowModuleFinder)
		chiplet.L1ToL2NoC.PlugInSMSide(l1s.GetBottomPort(), 16)
	}

	for _, l1iAT := range chiplet.L1IAddrTranslator {
		l1iAT.SetLowModuleFinder(lowModuleFinder)
		chiplet.L1ToL2NoC.PlugInSMSide(l1iAT.GetBottomPort(), 16)
	}

	for _, l2 := range chiplet.L2Caches {
		lowModuleFinder.LowModules = append(lowModuleFinder.LowModules,
			l2.TopPort)
		chiplet.L1ToL2NoC.PlugInMemSide(l2.TopPort, 64)
	}
	chiplet.lowModuleFinderForL1 = lowModuleFinder
}

func (b *MonolithicGPUBuilder) connectL1TLBToL2TLBNoC(chiplet *Chiplet) {
	var lowModuleFinder cache.LowModuleFinder

	singeLowModuleFinder := new(cache.SingleLowModuleFinder)
	singeLowModuleFinder.LowModule = chiplet.L2TLBs[0].GetTopPort()

	chiplet.L2TLBs[0].SetTLBFinder(singeLowModuleFinder)

	lowModuleFinder = singeLowModuleFinder

	for _, l1vTLB := range chiplet.L1VTLBs {
		l1vTLB.SetLowModuleFinder(lowModuleFinder)
		chiplet.L1TLBToL2TLBNoC.PlugInSMSide(l1vTLB.GetBottomPort(), 16)
	}

	for _, l1iTLB := range chiplet.L1ITLBs {
		l1iTLB.SetLowModuleFinder(lowModuleFinder)
		chiplet.L1TLBToL2TLBNoC.PlugInSMSide(l1iTLB.GetBottomPort(), 16)
	}

	for _, l1sTLB := range chiplet.L1STLBs {
		l1sTLB.SetLowModuleFinder(lowModuleFinder)
		chiplet.L1TLBToL2TLBNoC.PlugInSMSide(l1sTLB.GetBottomPort(), 16)
	}

	chiplet.L1TLBToL2TLBNoC.PlugInMemSide(chiplet.L2TLBs[0].GetTopPort(), 64)
}

func (b *MonolithicGPUBuilder) connectMMUToL2NoC(chiplet *Chiplet) {
	chiplet.MMU.SetLowModuleFinder(chiplet.lowModuleFinderForL1)
	chiplet.L1ToL2NoC.PlugInSMSide(chiplet.MMU.TranslationPortPort(), 64)
}

func (b *MonolithicGPUBuilder) setupInterchipNetwork() {
	chipConnector := chipnetwork.NewInterChipletConnector().
		WithEngine(b.engine).
		WithSwitchLatency(360).
		WithFreq(1 * akita.GHz).
		WithFlitByteSize(64).
		WithNumReqPerCycle(12).
		WithNetworkName("ICN")
	chipConnector.CreateNetwork()
	for _, chiplet := range b.chiplets {
		chipConnector.PlugInChip(b.InterChipletPorts(chiplet))
	}
	chipConnector.MakeNetwork()
}

func (b *MonolithicGPUBuilder) InterChipletPorts(c *Chiplet) []akita.Port {
	ports := []akita.Port{
		c.chipRdmaEngine.RequestPort,
		c.chipRdmaEngine.ResponsePort,
	}
	return ports
}
