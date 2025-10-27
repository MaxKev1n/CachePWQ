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
	"gitlab.com/akita/noc/networking/chipnetwork"
	"gitlab.com/akita/util/tracing"
)

type SMSideGPUBuilder struct {
	*CommonBuilder

	// specific componenets
	MMUs []mmu.MMU

	numL2TLBSlices int
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

	b.connectL1ToL2(chiplet)
	b.connectL2ToDRAM(chiplet)
	b.connectL1TLBToL2TLB(chiplet)
	b.connectL2TLBTOMMU(chiplet)
	b.connectMMUToL2(chiplet)

	b.chiplets = append(b.chiplets, chiplet)

	b.buildPageMigrationController()
	b.setupDMA()

	// b.setupMMUs()
	b.connectCP()
	b.setupInterchipNetwork()

	return b.gpu
}

func (b *SMSideGPUBuilder) buildL2TLB(chiplet *Chiplet) {
	numSets := 64 // 128 // 256 // changed this here
	numWays := 8  // 8 // changed this here

	if numSets%b.numL2TLBSlices != 0 {
		log.Panicf("numSets %d is not divisible by numL2TLBSlices %d",
			numSets, b.numL2TLBSlices)
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
			WithNumSets(numSets / b.numL2TLBSlices).
			WithNumMSHREntry(numMSHREntry / b.numL2TLBSlices).
			WithNumReqPerCycle(1).
			WithLog2PageSize(b.log2PageSize).
			WithLowModule(b.MMUs[i].ToTopPort()).
			WithNoCLatency(40).
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

func (b *SMSideGPUBuilder) connectL1TLBToL2TLB(chiplet *Chiplet) {
	tlbConn := akita.NewDirectConnection(chiplet.name+"L1TLB-L2TLB",
		b.engine, b.freq)

	var lowModuleFinder cache.LowModuleFinder

	numBits := int(math.Log2(float64(b.numL2TLBSlices)))
	xorLowModuleFinder := cache.NewXORLowModuleFinder(
		b.numL2TLBSlices,
		4,
		numBits,
		16,
	)

	for i := 0; i < b.numL2TLBSlices; i++ {
		xorLowModuleFinder.LowModules = append(
			xorLowModuleFinder.LowModules,
			chiplet.L2TLBs[i].GetTopPort(),
		)
		tlbConn.PlugIn(chiplet.L2TLBs[i].GetTopPort(), 64)
	}

	lowModuleFinder = xorLowModuleFinder

	for _, l1vTLB := range chiplet.L1VTLBs {
		l1vTLB.SetLowModuleFinder(lowModuleFinder)
		tlbConn.PlugIn(l1vTLB.BottomPort, 16)
	}

	for _, l1iTLB := range chiplet.L1ITLBs {
		l1iTLB.SetLowModuleFinder(lowModuleFinder)
		tlbConn.PlugIn(l1iTLB.BottomPort, 16)
	}

	for _, l1sTLB := range chiplet.L1STLBs {
		l1sTLB.SetLowModuleFinder(lowModuleFinder)
		tlbConn.PlugIn(l1sTLB.BottomPort, 16)
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
