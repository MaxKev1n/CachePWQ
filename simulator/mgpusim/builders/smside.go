package builders

import (
	"fmt"
	"log"
	"math"
	"strconv"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem/cache"
	"gitlab.com/akita/mem/vm/mmu"
	"gitlab.com/akita/mem/vm/tlb"
	"gitlab.com/akita/mgpusim"
	"gitlab.com/akita/mgpusim/yamlconfig"
	noc "gitlab.com/akita/noc/networking/booksim"
	"gitlab.com/akita/noc/networking/chipnetwork"
	"gitlab.com/akita/util/tracing"
)

type SMSideGPUBuilder struct {
	*CommonBuilder

	// specific componenets
	MMUs []mmu.MMU

	numL2TLBSlices int
	numL2TLBSets   int
}

// WithNumL2TLBSlices sets the number of L2 TLB slices.
func (b *SMSideGPUBuilder) WithNumL2TLBSlices(n int) {
	b.numL2TLBSlices = n
}

// Distributed TLB specific function

// MakeSMSideGPUBuilder provides a GPU builder that can builds MCM GPU.
func MakeSMSideGPUBuilder() SMSideGPUBuilder {
	// TODO: should this be using new? is the object being allocated on the stack?
	cbp := CommonBuilder{}
	b := SMSideGPUBuilder{CommonBuilder: &cbp}
	b.SetDefaultCommonBuilderParams()
	return b
}

func (b SMSideGPUBuilder) Build(name string, id uint64) *mgpusim.GPU {
	b.createGPU(name, id)

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

	b.createIntraChipletNoC(chiplet)
	b.calculateTwoSideComponents(chiplet)

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

// BuildSAs builds shader arrays.
func (b *SMSideGPUBuilder) BuildSAs(chiplet *Chiplet) {
	saBuilder := makeShaderArrayBuilder()
	saBuilder.withEngine(b.engine)
	saBuilder.withFreq(b.freq)
	saBuilder.withGPUID(b.gpu.GPUID)
	saBuilder.withLog2CachelineSize(b.log2CacheLineSize)
	saBuilder.withLog2PageSize(b.log2PageSize)
	saBuilder.withNumCU(b.numCUPerShaderArray)
	saBuilder.withPageTable(b.pageTable)
	saBuilder.withConfig("SMSide")

	if b.enableVisTracing {
		saBuilder.withVisTracer(b.visTracer)
	}

	for i := 0; i < b.numShaderArrayPerChiplet; i++ {
		saName := fmt.Sprintf("%s.SA_%02d", chiplet.name, i)
		sa := saBuilder.Build(saName, i)
		b.collectSAComponents(sa, chiplet)
	}
}

func (b *SMSideGPUBuilder) buildL2TLB(chiplet *Chiplet) {
	b.numL2TLBSets = 64 // 128 // 256 // changed this here
	numWays := 8        // 8 // changed this here

	if b.numL2TLBSets%b.numL2TLBSlices != 0 {
		log.Panicf("numL2TLBSets %d is not divisible by numL2TLBSlices %d",
			b.numL2TLBSets, b.numL2TLBSlices)
	}

	numMSHREntry := 64

	if numMSHREntry%b.numL2TLBSlices != 0 {
		log.Panicf("numMSHREntry %d is not divisible by numL2TLBSlices %d",
			numMSHREntry, b.numL2TLBSlices)
	}

	for i := 0; i < b.numL2TLBSlices; i++ {
		builder := tlb.MakeSMSideTLBBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithNumWays(numWays).
			WithNumSets(b.numL2TLBSets / b.numL2TLBSlices).
			WithNumMSHREntry(numMSHREntry / b.numL2TLBSlices).
			WithNumReqPerCycle(2).
			WithLog2PageSize(b.log2PageSize).
			WithLowModule(b.MMUs[i].ToTopPort()).
			WithAccessLatency(40)

		if b.useCoalescingTLBPort {
			builder = builder.UseCoalescingTLBPort()
		}

		l2TLB := builder.Build(fmt.Sprintf("%s.L2TLB[%d]", chiplet.name, i))
		l2TLB.(*tlb.SMSideTLB).ID = i
		l2TLB.SetLowModuleFinder(&cache.SingleLowModuleFinder{
			LowModule: b.MMUs[i].ToTopPort(),
		})

		b.l2TLBs = append(b.l2TLBs, l2TLB)
		b.gpu.L2TLBs = append(b.gpu.L2TLBs, l2TLB)
		chiplet.L2TLBs = append(chiplet.L2TLBs, l2TLB)

		if b.enableVisTracing {
			tracing.CollectTrace(l2TLB, b.visTracer)
		}
	}
}

func (b *SMSideGPUBuilder) connectCP() {
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

func (b *SMSideGPUBuilder) calculateTwoSideComponents(chiplet *Chiplet) {
	for range chiplet.L1VCaches {
		b.numSMsideComp++
	}

	for range chiplet.L1SCaches {
		b.numSMsideComp++
	}

	for range chiplet.L1IAddrTranslator {
		b.numSMsideComp++
	}

	for range chiplet.L2Caches {
		b.numMemsideComp++
	}

	for range chiplet.L1VTLBs {
		b.numSMsideComp++
	}

	for range chiplet.L1ITLBs {
		b.numSMsideComp++
	}

	for range chiplet.L1STLBs {
		b.numSMsideComp++
	}

	for range chiplet.L2TLBs {
		b.numMemsideComp++
		b.numSMsideComp++
	}

	chiplet.BookSimNoC.MaxNumSMSidePort = b.numSMsideComp
	chiplet.BookSimNoC.MaxNumMemSidePort = b.numMemsideComp

	log.Printf("Chiplet %d has %d SM side components and %d Mem side components\n",
		chiplet.ChipletID, b.numSMsideComp, b.numMemsideComp)

	chiplet.BookSimNoC.CreateNetwork(b.booksimConfig)

	log.Printf("L1Cache from 0 to %v\n",
		len(chiplet.L1VCaches)+len(chiplet.L1SCaches)+len(chiplet.L1IAddrTranslator)-1,
	)
	log.Printf("L1TLB from %v to %v\n",
		len(chiplet.L1VCaches)+len(chiplet.L1SCaches)+len(chiplet.L1IAddrTranslator),
		len(chiplet.L1VCaches)+len(chiplet.L1SCaches)+len(chiplet.L1IAddrTranslator)+
			len(chiplet.L1VTLBs)+len(chiplet.L1ITLBs)+len(chiplet.L1STLBs)-1,
	)
	log.Printf("L2Cache from %v to %v\n",
		b.numSMsideComp, b.numSMsideComp+len(chiplet.L2Caches)-1,
	)
	log.Printf("L2TLB from %v to %v\n",
		b.numSMsideComp+len(chiplet.L2Caches),
		b.numSMsideComp+len(chiplet.L2Caches)+len(chiplet.L2TLBs)-1,
	)
}

func (b *SMSideGPUBuilder) createIntraChipletNoC(chiplet *Chiplet) {
	chiplet.BookSimNoC = noc.NewBookSimNoC(
		fmt.Sprintf("L1ToL2NoC[%d]", chiplet.ChipletID),
		b.engine,
	)

	b.gpu.NoCs = append(b.gpu.NoCs, chiplet.BookSimNoC)
}

func (b *SMSideGPUBuilder) connectL1ToL2NoC(chiplet *Chiplet) {
	fmt.Println("memory address offset:", b.memAddrOffset)
	lowModuleFinder := cache.NewStripedLocalVRemoteLowModuleFinder(b.memAddrOffset, uint64(b.numChiplet*b.numMemoryBankPerChiplet),
		1<<b.log2MemoryBankInterleavingSize, uint64(b.numMemoryBankPerChiplet)*chiplet.ChipletID, uint64(b.numMemoryBankPerChiplet)*chiplet.ChipletID+uint64(b.numMemoryBankPerChiplet-1))
	lowModuleFinder.ModuleForOtherAddresses = chiplet.chipRdmaEngine.ToL1

	for _, l1v := range chiplet.L1VCaches {
		l1v.SetLowModuleFinder(lowModuleFinder)
		chiplet.BookSimNoC.PlugInSMSide(l1v.GetBottomPort(), 16)
	}

	for _, l1s := range chiplet.L1SCaches {
		l1s.SetLowModuleFinder(lowModuleFinder)
		chiplet.BookSimNoC.PlugInSMSide(l1s.GetBottomPort(), 16)
	}

	for _, l1iAT := range chiplet.L1IAddrTranslator {
		l1iAT.SetLowModuleFinder(lowModuleFinder)
		chiplet.BookSimNoC.PlugInSMSide(l1iAT.GetBottomPort(), 16)
	}

	for _, l2 := range chiplet.L2Caches {
		lowModuleFinder.LowModules = append(lowModuleFinder.LowModules,
			l2.TopPort)
		chiplet.BookSimNoC.PlugInMemSide(l2.TopPort, 64)
	}
	chiplet.lowModuleFinderForL1 = lowModuleFinder
}

func (b *SMSideGPUBuilder) connectL1TLBToL2TLBNoC(chiplet *Chiplet) {
	numElemBits := int(math.Log2(float64(b.numL2TLBSets) / float64(b.numL2TLBSlices)))
	numBits := int(math.Log2(float64(b.numL2TLBSets)))
	xorLowModuleFinder := cache.NewPartitionedXORLowModuleFinder(
		numElemBits,
		4,
		numBits,
		int(b.log2PageSize),
	)

	connections := make([]*akita.DirectConnection, b.numL2TLBSlices)

	for i := 0; i < b.numL2TLBSlices; i++ {
		xorLowModuleFinder.LocalLowModules = append(
			xorLowModuleFinder.LocalLowModules,
			chiplet.L2TLBs[i].(*tlb.SMSideTLB).LocalTopPort,
		)
		xorLowModuleFinder.RemoteLowModules = append(
			xorLowModuleFinder.RemoteLowModules,
			chiplet.L2TLBs[i].(*tlb.SMSideTLB).RemoteTopPort,
		)
		chiplet.BookSimNoC.PlugInMemSide(
			chiplet.L2TLBs[i].(*tlb.SMSideTLB).RemoteTopPort,
			64,
		)

		connections[i] = akita.NewDirectConnection(
			fmt.Sprintf("%s.L1TLB-L2TLBConn[%d]", chiplet.name, i),
			b.engine, b.freq,
		)

		connections[i].PlugIn(
			chiplet.L2TLBs[i].(*tlb.SMSideTLB).LocalTopPort,
			64,
		)
	}

	numL1VTLBPerPartition := len(chiplet.L1VTLBs) / b.numL2TLBSlices
	numL1STLBPerPartition := len(chiplet.L1STLBs) / b.numL2TLBSlices
	numL1ITLBPerPartition := len(chiplet.L1ITLBs) / b.numL2TLBSlices

	for i, l1vTLB := range chiplet.L1VTLBs {
		l1vTLB.(*tlb.SMSideL1TLB).SetPartitionedXORLowModuleFinder(xorLowModuleFinder)
		chiplet.BookSimNoC.PlugInSMSide(l1vTLB.(*tlb.SMSideL1TLB).RemotePort, 16)

		l1vTLB.(*tlb.SMSideL1TLB).PartitionIndex = uint64(i) / uint64(numL1VTLBPerPartition)

		connections[l1vTLB.(*tlb.SMSideL1TLB).PartitionIndex].PlugIn(
			l1vTLB.(*tlb.SMSideL1TLB).LocalPort,
			16,
		)
	}

	for i, l1iTLB := range chiplet.L1ITLBs {
		l1iTLB.(*tlb.SMSideL1TLB).SetPartitionedXORLowModuleFinder(xorLowModuleFinder)
		chiplet.BookSimNoC.PlugInSMSide(l1iTLB.(*tlb.SMSideL1TLB).RemotePort, 16)

		l1iTLB.(*tlb.SMSideL1TLB).PartitionIndex = uint64(i) / uint64(numL1ITLBPerPartition)

		connections[l1iTLB.(*tlb.SMSideL1TLB).PartitionIndex].PlugIn(
			l1iTLB.(*tlb.SMSideL1TLB).LocalPort,
			16,
		)
	}

	for i, l1sTLB := range chiplet.L1STLBs {
		l1sTLB.(*tlb.SMSideL1TLB).SetPartitionedXORLowModuleFinder(xorLowModuleFinder)
		chiplet.BookSimNoC.PlugInSMSide(l1sTLB.(*tlb.SMSideL1TLB).RemotePort, 16)

		l1sTLB.(*tlb.SMSideL1TLB).PartitionIndex = uint64(i) / uint64(numL1STLBPerPartition)

		connections[l1sTLB.(*tlb.SMSideL1TLB).PartitionIndex].PlugIn(
			l1sTLB.(*tlb.SMSideL1TLB).LocalPort,
			16,
		)
	}
}

func (b *SMSideGPUBuilder) setupInterchipNetwork() {
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

func (b *SMSideGPUBuilder) InterChipletPorts(c *Chiplet) []akita.Port {
	ports := []akita.Port{
		c.chipRdmaEngine.RequestPort,
		c.chipRdmaEngine.ResponsePort,
	}
	return ports
}

func (b *SMSideGPUBuilder) buildMMU(chiplet *Chiplet) {
	if yamlconfig.OverrideConfig == nil {
		b.buildDefaultMMU(chiplet)
	} else {
		mmuType := yamlconfig.OverrideConfig["MMU.type"]

		switch mmuType {
		case "IdealMMU":
			b.buildIdealMMU(chiplet)
		case "caPWQMMU":
			b.buildCAPWQMMU(chiplet)
		case "MPWMMU":
			panic("SMSideGPUBuilder does not support MPWMMU yet")
		case "BaselineMMU":
			b.buildDefaultMMU(chiplet)
		default:
			log.Panicf("Unsupported MMU type: %s\n", mmuType)
		}
	}
}

func (b *SMSideGPUBuilder) buildIdealMMU(chiplet *Chiplet) {
	mmuBuilder := mmu.MakeIdealMMUBuilder().
		WithEngine(b.engine).
		WithFreq(1 * akita.GHz).
		WithLog2PageSize(b.log2PageSize).
		WithPageTable(b.pageTable)

	if latency, ok := yamlconfig.OverrideConfig["MMU.walkLatency"]; ok {
		latencyInt, err := strconv.Atoi(latency)
		if err != nil {
			log.Panicf("Invalid walk latency: %s\n", latency)
		}

		mmuBuilder = mmuBuilder.WithLatency(latencyInt)
	}

	for i := 0; i < b.numL2TLBSlices; i++ {
		mmu := mmuBuilder.Build(fmt.Sprintf("%s.IdealMMU[%d]", chiplet.name, i))
		mmu.SetCommandProcessorPort(b.gpu.CommandProcessor.ToMMUs)
		b.MMUs = append(b.MMUs, mmu)
		b.gpu.MMUs = append(b.gpu.MMUs, mmu)
	}
}

func (b *SMSideGPUBuilder) buildCAPWQMMU(chiplet *Chiplet) {
	maxNumReqInFlight := 16

	if maxNumReqInFlight%b.numL2TLBSlices != 0 {
		log.Panicf("maxNumReqInFlight %d is not divisible by numL2TLBSlices %d",
			maxNumReqInFlight, b.numL2TLBSlices)
	}

	mmuBuilder := mmu.MakeCaPWQMMUBuilder().
		WithEngine(b.engine).
		WithFreq(1 * akita.GHz).
		WithLog2PageSize(b.log2PageSize).
		WithPageTable(b.pageTable).
		WithNumChiplets(uint64(b.numChiplet)).
		WithMaxNumReqInFlight(maxNumReqInFlight / b.numL2TLBSlices)

	if numWalkers, ok := yamlconfig.OverrideConfig["MMU.numPageWalkers"]; ok {
		numWalkersInt, err := strconv.Atoi(numWalkers)
		if err != nil {
			log.Panicf("Invalid number of walkers %s\n", numWalkersInt)
		}

		if numWalkersInt%b.numL2TLBSlices != 0 {
			log.Panicf("numWalkers %d is not divisible by numL2TLBSlices %d",
				numWalkersInt, b.numL2TLBSlices)
		}

		mmuBuilder = mmuBuilder.WithMaxNumReqInFlight(numWalkersInt / b.numL2TLBSlices)
	}

	for i := 0; i < b.numL2TLBSlices; i++ {
		mmu := mmuBuilder.Build(fmt.Sprintf("%s.caPWQMMU[%d]", chiplet.name, i))
		mmu.SetCommandProcessorPort(b.gpu.CommandProcessor.ToMMUs)
		b.MMUs = append(b.MMUs, mmu)
		b.gpu.MMUs = append(b.gpu.MMUs, mmu)
	}
}

func (b *SMSideGPUBuilder) buildDefaultMMU(chiplet *Chiplet) {
	maxNumReqInFlight := 16

	if maxNumReqInFlight%b.numL2TLBSlices != 0 {
		log.Panicf("maxNumReqInFlight %d is not divisible by numL2TLBSlices %d",
			maxNumReqInFlight, b.numL2TLBSlices)
	}

	mmuBuilder := mmu.MakeMMUBuilder().
		WithEngine(b.engine).
		WithFreq(1 * akita.GHz).
		WithLog2PageSize(b.log2PageSize).
		WithPageTable(b.pageTable).
		WithNumChiplets(uint64(b.numChiplet)).
		WithMaxNumReqInFlight(maxNumReqInFlight / b.numL2TLBSlices)

	if numWalkers, ok := yamlconfig.OverrideConfig["MMU.numPageWalkers"]; ok {
		numWalkersInt, err := strconv.Atoi(numWalkers)
		if err != nil {
			log.Panicf("Invalid number of walkers %s\n", numWalkersInt)
		}

		if numWalkersInt%b.numL2TLBSlices != 0 {
			log.Panicf("numWalkers %d is not divisible by numL2TLBSlices %d",
				numWalkersInt, b.numL2TLBSlices)
		}

		mmuBuilder = mmuBuilder.WithMaxNumReqInFlight(numWalkersInt / b.numL2TLBSlices)
	}

	for i := 0; i < b.numL2TLBSlices; i++ {
		mmu := mmuBuilder.Build(fmt.Sprintf("%s.BaselineMMU[%d]", chiplet.name, i))
		mmu.SetCommandProcessorPort(b.gpu.CommandProcessor.ToMMUs)
		b.MMUs = append(b.MMUs, mmu)
		b.gpu.MMUs = append(b.gpu.MMUs, mmu)
	}
}

func (b *SMSideGPUBuilder) connectL2TLBTOMMU(chiplet *Chiplet) {
	tlbToMMUConn := akita.NewDirectConnection(chiplet.name+".L2TLB-MMU",
		b.engine, b.freq)
	for i, l2tlb := range chiplet.L2TLBs {
		tlbToMMUConn.PlugIn(b.MMUs[i].ToTopPort(), 64)
		tlbToMMUConn.PlugIn(l2tlb.GetBottomPort(), 16)
	}
}

func (b *SMSideGPUBuilder) connectMMUToL2NoC(chiplet *Chiplet) {
	for _, mmu := range b.MMUs {
		mmu.SetLowModuleFinder(chiplet.lowModuleFinderForL1)
		chiplet.BookSimNoC.PlugInSMSide(mmu.TranslationPortPort(), 64)
	}
}

func (b *SMSideGPUBuilder) connectMMUToL2(chiplet *Chiplet) {
	for _, mmu := range b.MMUs {
		mmu.SetLowModuleFinder(chiplet.lowModuleFinderForL1)
		chiplet.L1ToL2Connection.PlugIn(mmu.TranslationPortPort(), 64)
	}
}

func (b *SMSideGPUBuilder) connectCPWithMMUs() {
	for _, mmu := range b.MMUs {
		b.cp.MMUs = append(b.cp.MMUs, mmu.ControlPortPort())
		b.internalConn.PlugIn(mmu.ControlPortPort(), 10)
	}
}
