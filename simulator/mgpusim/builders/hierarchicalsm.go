package builders

import (
	"fmt"
	"log"
	"math"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/mem/cache"
	"gitlab.com/akita/mem/cache/writeback"
	"gitlab.com/akita/mem/idealmemcontroller"
	"gitlab.com/akita/mem/vm/tlb"
	"gitlab.com/akita/mgpusim"
	"gitlab.com/akita/mgpusim/tip"
	noc "gitlab.com/akita/noc/networking/booksim"
	"gitlab.com/akita/noc/networking/chipnetwork"
	"gitlab.com/akita/noc/networking/multiplexer"
	"gitlab.com/akita/util/tracing"
)

type HierarchicalSMSideGPUBuilder struct {
	*CommonBuilder

	// specific componenets
}

func MakeHierarchicalSMSideGPUBuilder() HierarchicalSMSideGPUBuilder {
	// TODO: should this be using new? is the object being allocated on the stack?
	cbp := CommonBuilder{}
	b := HierarchicalSMSideGPUBuilder{CommonBuilder: &cbp}
	b.SetDefaultCommonBuilderParams()
	return b
}

func (b HierarchicalSMSideGPUBuilder) Build(name string, id uint64) *mgpusim.GPU {
	b.createGPU(name, id)

	if b.useTimeInstProfiling {
		b.buildTEA()
	}

	if b.useTimeEventAnalysis {
		b.TipEngine.UseTimeEventAnalysis()
	}

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
	b.buildL2TLB(chiplet)

	b.configChipRDMAEngine(chiplet, chipRdmaAddressTable, rdmaResponsePorts)
	// b.configRemoteAddressTranslationUnit(chiplet, remoteAddressTranslationTable, rtuResponsePorts)

	b.createGlobalNoC(chiplet)

	b.establishL1ToL2RoutingPath(chiplet)
	b.establishL1TLBToL2TLBRoutingPath(chiplet)
	b.establishNonUniformL1TLBToL2TLBNetwork(chiplet)

	b.connectL2ToDRAM(chiplet)
	b.connectL2TLBTOMMU(chiplet)
	b.connectMMUToGlobalNoC(chiplet)

	b.establishTPC(chiplet)
	b.establishGPC(chiplet)
	b.establishL2Partition(chiplet)

	b.connectGlobalNoC(chiplet)

	b.chiplets = append(b.chiplets, chiplet)

	b.buildPageMigrationController()
	b.setupDMA()

	// b.setupMMUs()
	b.connectCP()
	b.setupInterchipNetwork()

	chiplet.GlobalNoC.Establish()

	return b.gpu
}

func (b *HierarchicalSMSideGPUBuilder) buildTEA() {
	b.TipEngine = tip.NewTimeEventAnalysisEngine(
		b.engine,
	)
}

func (b *HierarchicalSMSideGPUBuilder) connectCP() {
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
	b.connectCPWithTLBs()
}

func (b *HierarchicalSMSideGPUBuilder) createGlobalNoC(chiplet *Chiplet) {
	chiplet.GlobalNoC = noc.NewHybridBookSimNoC(
		fmt.Sprintf("HybridGlobalNoC[%d]", chiplet.ChipletID),
		b.engine,
	)

	b.gpu.NoCs = append(b.gpu.NoCs, chiplet.GlobalNoC)

	maxCUsPerGPC := 16     // same as NVIDIA A100
	maxL2PerPartition := 4 // same as NVIDIA V100

	chiplet.GlobalNoC.MaxNumSMSidePort = ((len(chiplet.CUs) - 1) / maxCUsPerGPC) + 1
	chiplet.GlobalNoC.MaxNumSMSideNode = chiplet.GlobalNoC.MaxNumSMSidePort * 4

	chiplet.GlobalNoC.MaxNumMemSidePort = ((len(chiplet.L2Caches) - 1) / maxL2PerPartition) + 1
	chiplet.GlobalNoC.MaxNumMemSideNode = chiplet.GlobalNoC.MaxNumMemSidePort * 16

	// Monolithic MMU
	chiplet.GlobalNoC.MaxNumSMSidePort++
	chiplet.GlobalNoC.MaxNumSMSideNode++

	log.Printf("%s has %d SM side components and %d Mem side components\n",
		chiplet.GlobalNoC.Name(), chiplet.GlobalNoC.MaxNumSMSidePort, chiplet.GlobalNoC.MaxNumMemSidePort)

	chiplet.GlobalNoC.CreateNetworkWithLib(
		b.booksimDir+"libintersim.dylib", b.booksimGlobal,
	)
}

func (b *HierarchicalSMSideGPUBuilder) establishL1ToL2RoutingPath(chiplet *Chiplet) {
	fmt.Println("memory address offset:", b.memAddrOffset)
	lowModuleFinder := cache.NewStripedLocalVRemoteLowModuleFinder(b.memAddrOffset, uint64(b.numChiplet*b.numMemoryBankPerChiplet),
		1<<b.log2MemoryBankInterleavingSize, uint64(b.numMemoryBankPerChiplet)*chiplet.ChipletID, uint64(b.numMemoryBankPerChiplet)*chiplet.ChipletID+uint64(b.numMemoryBankPerChiplet-1))
	lowModuleFinder.ModuleForOtherAddresses = chiplet.chipRdmaEngine.ToL1

	for _, l1v := range chiplet.L1VCaches {
		l1v.SetLowModuleFinder(lowModuleFinder)
	}

	for _, l1s := range chiplet.L1SCaches {
		l1s.SetLowModuleFinder(lowModuleFinder)
	}

	for _, l1iAT := range chiplet.L1IAddrTranslator {
		l1iAT.SetLowModuleFinder(lowModuleFinder)
	}

	for _, l2 := range chiplet.L2Caches {
		lowModuleFinder.LowModules = append(lowModuleFinder.LowModules,
			l2.TopPort)
	}
	chiplet.lowModuleFinderForL1 = lowModuleFinder
}

func (b *HierarchicalSMSideGPUBuilder) establishTPC(chiplet *Chiplet) {
	maxCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/maxCUsPerGPC + 1
	numTPCs := numGPCs * (maxCUsPerGPC / 2) // 2 CUs per TPC

	for tpcID := 0; tpcID < numTPCs; tpcID++ {
		routingTable := multiplexer.NewMapRoutingTable()

		gpcID := tpcID / (maxCUsPerGPC / 2)
		localID := tpcID % (maxCUsPerGPC / 2)

		mux := multiplexer.MakeMultiplexerBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithNumReqPerCycle(2).
			WithSwitchLatency(2).
			WithBufferSizeInNumFlit(16).
			WithRoutingTable(routingTable).
			Build(fmt.Sprintf("%s.GPC[%d].TPCMux[%d]", chiplet.name, gpcID, localID))

		chiplet.tpcMux = append(chiplet.tpcMux, mux)
	}

	for i, l1v := range chiplet.L1VCaches {
		ep := multiplexer.MakeEndPointBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithDevicePorts([]akita.Port{l1v.GetBottomPort()}).
			WithNumReqPerCycle(1).
			WithNetworkPortBufferSize(1).
			WithFlitByteSize(32).
			Build(fmt.Sprintf("%s.L1VCache[%d]", chiplet.name, i))

		tpcID := i / 2
		mux := chiplet.tpcMux[tpcID]

		localPort := mux.AddLowSidePort(ep)
		mux.AddRoute(l1v.GetBottomPort(), localPort)
	}
}

func (b *HierarchicalSMSideGPUBuilder) establishGPC(chiplet *Chiplet) {
	maxCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/maxCUsPerGPC + 1
	numTPCs := numGPCs * (maxCUsPerGPC / 2) // 2 CUs per TPC

	for gpcID := 0; gpcID < numGPCs; gpcID++ {
		routingTable := multiplexer.NewMapRoutingTable()

		mux := multiplexer.MakeMultiplexerBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithNumReqPerCycle(8).
			WithSwitchLatency(15).
			WithBufferSizeInNumFlit(64).
			WithRoutingTable(routingTable).
			Build(fmt.Sprintf("%s.GPCMux[%d]", chiplet.name, gpcID))

		chiplet.gpcMux = append(chiplet.gpcMux, mux)
	}

	for tpcID := 0; tpcID < numTPCs; tpcID++ {
		gpcID := tpcID / (maxCUsPerGPC / 2)

		tpcMux := chiplet.tpcMux[tpcID]
		gpcMux := chiplet.gpcMux[gpcID]

		multiplexer.ConnectMultiplexers(
			b.engine,
			tpcMux,
			gpcMux,
			b.freq,
		)
	}

	numSAPerGPC := b.numShaderArrayPerChiplet / numGPCs

	for i, l1s := range chiplet.L1SCaches {
		ep := multiplexer.MakeEndPointBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithDevicePorts([]akita.Port{l1s.GetBottomPort()}).
			WithFlitByteSize(32).
			WithNumReqPerCycle(1).
			WithNetworkPortBufferSize(1).
			Build(fmt.Sprintf("%s.L1SCache[%d]", chiplet.name, i))

		gpcID := i / numSAPerGPC
		mux := chiplet.gpcMux[gpcID]

		localPort := mux.AddLowSidePort(ep)
		mux.AddRoute(l1s.GetBottomPort(), localPort)
	}

	for i, l1i := range chiplet.L1IAddrTranslator {
		ep := multiplexer.MakeEndPointBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithDevicePorts([]akita.Port{l1i.GetBottomPort()}).
			WithFlitByteSize(32).
			WithNumReqPerCycle(1).
			WithNetworkPortBufferSize(1).
			Build(fmt.Sprintf("%s.L1ICache[%d]", chiplet.name, i))

		gpcID := i / numSAPerGPC
		mux := chiplet.gpcMux[gpcID]

		localPort := mux.AddLowSidePort(ep)
		mux.AddRoute(l1i.GetBottomPort(), localPort)
	}
}

func (b *HierarchicalSMSideGPUBuilder) establishL2Partition(chiplet *Chiplet) {
	maxL2PerPartition := 4
	numMux := (len(chiplet.L2Caches)-1)/maxL2PerPartition + 1

	for muxID := 0; muxID < numMux; muxID++ {
		routingTable := multiplexer.NewMapRoutingTable()

		mux := multiplexer.MakeMultiplexerBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithNumReqPerCycle(32).
			WithSwitchLatency(15).
			WithBufferSizeInNumFlit(128).
			WithRoutingTable(routingTable).
			Build(fmt.Sprintf("%s.L2Partition[%d]", chiplet.name, muxID))

		chiplet.l2Mux = append(chiplet.l2Mux, mux)
	}

	for i, l2 := range chiplet.L2Caches {
		ep := multiplexer.MakeEndPointBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithDevicePorts([]akita.Port{l2.TopPort}).
			WithFlitByteSize(32).
			WithNumReqPerCycle(4).
			WithNetworkPortBufferSize(4).
			Build(fmt.Sprintf("%s.L2Cache[%d]", chiplet.name, i))

		muxID := i / maxL2PerPartition
		mux := chiplet.l2Mux[muxID]

		localPort := mux.AddLowSidePort(ep)
		mux.AddRoute(l2.TopPort, localPort)
	}
}

func (b *HierarchicalSMSideGPUBuilder) connectGlobalNoC(chiplet *Chiplet) {
	for i, mux := range chiplet.gpcMux {
		ep := multiplexer.MakeHybridEndPointBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithFlitByteSize(32).
			WithFlitAssemblingBufferSize(128).
			WithNumReqPerCycle(64).
			WithNetworkPortBufferSize(64).
			Build(fmt.Sprintf("%s.GPCHighSideEndPoint[%d]", chiplet.name, i))

		mux.SetHighSideHybridEndPoint(ep)

		nocPort := chiplet.GlobalNoC.PlugInSMSideMultiPort(ep.NetworkPort, 64, 4)
		for _, port := range mux.RoutingTable.GetAllSrcPorts() {
			chiplet.GlobalNoC.AddRoute(port, ep.NetworkPort)
		}
		ep.PlugInNoCPort(nocPort, 64)
	}

	for i, mux := range chiplet.l2Mux {
		ep := multiplexer.MakeHybridEndPointBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithFlitByteSize(32).
			WithFlitAssemblingBufferSize(128).
			WithNumReqPerCycle(64).
			WithNetworkPortBufferSize(64).
			Build(fmt.Sprintf("%s.L2HighSideEndPoint[%d]", chiplet.name, i))

		mux.SetHighSideHybridEndPoint(ep)

		nocPort := chiplet.GlobalNoC.PlugInMemSideMultiPort(ep.NetworkPort, 64, 16)
		for _, port := range mux.RoutingTable.GetAllSrcPorts() {
			chiplet.GlobalNoC.AddRoute(port, ep.NetworkPort)
		}
		ep.PlugInNoCPort(nocPort, 64)
	}

	chiplet.GlobalNoC.PlugInSMSideMultiPort(chiplet.MMU.TranslationPortPort(), 64, 1)
}

func (b *HierarchicalSMSideGPUBuilder) establishL1TLBToL2TLBRoutingPath(chiplet *Chiplet) {
	numCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/numCUsPerGPC + 1

	if numGPCs != len(chiplet.L2TLBs) {
		panic("numGPCs != len(chiplet.L2TLBs)")
	}

	numElemBits := int(math.Log2(float64(256) / float64(4)))
	numBits := int(math.Log2(float64(256)))
	interleavedLowModuleFinder := cache.NewPartitionedXORLowModuleFinder(
		numElemBits,
		4,
		numBits,
		int(b.log2PageSize),
	)

	for i := 0; i < numGPCs; i++ {
		for j := i * numCUsPerGPC; j < (i+1)*numCUsPerGPC; j++ {
			chiplet.L1VTLBs[j].SetLowModuleFinder(interleavedLowModuleFinder)
			chiplet.L1VTLBs[j].SetGPCID(i)
		}

		numSAPerGPC := b.numShaderArrayPerChiplet / numGPCs
		if b.numShaderArrayPerChiplet%numGPCs != 0 {
			panic("numShaderArrayPerChiplet not divisible by numGPCs")
		}

		for j := i * numSAPerGPC; j < (i+1)*numSAPerGPC; j++ {
			chiplet.L1STLBs[j].SetLowModuleFinder(interleavedLowModuleFinder)
			chiplet.L1ITLBs[j].SetLowModuleFinder(interleavedLowModuleFinder)
			chiplet.L1STLBs[j].SetGPCID(i)
			chiplet.L1ITLBs[j].SetGPCID(i)
		}
	}

	for i, l2tlb := range chiplet.L2TLBs {
		interleavedLowModuleFinder.LowModules = append(interleavedLowModuleFinder.LowModules,
			l2tlb.GetTopPort())

		l2tlb.SetTLBFinder(interleavedLowModuleFinder)
		l2tlb.SetGPCID(i)
	}
}

func (b *HierarchicalSMSideGPUBuilder) establishUniformL1TLBToL2TLBNetwork(chiplet *Chiplet) {
	gpcSwitch := multiplexer.SwitchBuilder{}.
		WithEngine(b.engine).
		WithFreq(b.freq).
		WithArbiter(multiplexer.NewRRArbiter()).
		WithRoutingTable(multiplexer.NewMapRoutingTable()).
		WithNumReqPerCycle(128).
		WithBufferSizeInNumFlit(128).
		Build(fmt.Sprintf("%s.TLBSwitch", chiplet.name))

	for i, l1vtlb := range chiplet.L1VTLBs {
		ep := multiplexer.MakeEndPointBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithDevicePorts([]akita.Port{l1vtlb.GetBottomPort()}).
			WithNumReqPerCycle(1).
			WithFlitByteSize(32).
			Build(fmt.Sprintf("%s.L1VTLB[%d]", chiplet.name, i))

		switchPort := gpcSwitch.ConnectEndPointToSwitch(ep, 5, b.freq)
		rt := gpcSwitch.GetRoutingTable()
		rt.AddRoute(l1vtlb.GetBottomPort(), switchPort)
	}

	for i, l1itlb := range chiplet.L1ITLBs {
		ep := multiplexer.MakeEndPointBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithDevicePorts([]akita.Port{l1itlb.GetBottomPort()}).
			WithFlitByteSize(32).
			WithNumReqPerCycle(1).
			WithNetworkPortBufferSize(1).
			Build(fmt.Sprintf("%s.L1ITLB[%d]", chiplet.name, i))

		switchPort := gpcSwitch.ConnectEndPointToSwitch(ep, 5, b.freq)
		rt := gpcSwitch.GetRoutingTable()
		rt.AddRoute(l1itlb.GetBottomPort(), switchPort)
	}

	for i, l1stlb := range chiplet.L1STLBs {
		ep := multiplexer.MakeEndPointBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithDevicePorts([]akita.Port{l1stlb.GetBottomPort()}).
			WithFlitByteSize(32).
			WithNumReqPerCycle(1).
			WithNetworkPortBufferSize(1).
			Build(fmt.Sprintf("%s.L1STLB[%d]", chiplet.name, i))

		switchPort := gpcSwitch.ConnectEndPointToSwitch(ep, 5, b.freq)
		rt := gpcSwitch.GetRoutingTable()
		rt.AddRoute(l1stlb.GetBottomPort(), switchPort)
	}

	for i, l2tlb := range chiplet.L2TLBs {
		ep := multiplexer.MakeEndPointBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithDevicePorts([]akita.Port{l2tlb.GetTopPort()}).
			WithFlitByteSize(32).
			WithNumReqPerCycle(8).
			WithNetworkPortBufferSize(8).
			Build(fmt.Sprintf("%s.L2TLB[%d]", chiplet.name, i))

		switchPort := gpcSwitch.ConnectEndPointToSwitch(ep, 5, b.freq)
		rt := gpcSwitch.GetRoutingTable()
		rt.AddRoute(l2tlb.GetTopPort(), switchPort)
	}
}

func (b *HierarchicalSMSideGPUBuilder) establishNonUniformL1TLBToL2TLBNetwork(chiplet *Chiplet) {
	numCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/numCUsPerGPC + 1

	if numGPCs != len(chiplet.L2TLBs) {
		panic("numGPCs != len(chiplet.L2TLBs)")
	}

	gpcSwitches := make([]*multiplexer.Switch, 0)

	for i := 0; i < numGPCs; i++ {
		gpcSwitch := multiplexer.SwitchBuilder{}.
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithArbiter(multiplexer.NewRRArbiter()).
			WithRoutingTable(multiplexer.NewMapRoutingTable()).
			WithNumReqPerCycle(32).
			WithBufferSizeInNumFlit(32).
			Build(fmt.Sprintf("%s.TLBSwitch[%d]", chiplet.name, i))

		gpcSwitches = append(gpcSwitches, gpcSwitch)

		rt := gpcSwitch.GetRoutingTable()

		for j := i * numCUsPerGPC; j < (i+1)*numCUsPerGPC; j++ {
			l1vtlb := chiplet.L1VTLBs[j]

			ep := multiplexer.MakeEndPointBuilder().
				WithEngine(b.engine).
				WithFreq(b.freq).
				WithDevicePorts([]akita.Port{l1vtlb.GetBottomPort()}).
				WithNumReqPerCycle(1).
				WithFlitByteSize(32).
				Build(fmt.Sprintf("%s.L1VTLB[%d]", chiplet.name, i))

			switchPort := gpcSwitch.ConnectEndPointToSwitch(ep, 5, b.freq)
			rt.AddRoute(l1vtlb.GetBottomPort(), switchPort)
		}

		numSAPerGPC := b.numShaderArrayPerChiplet / numGPCs
		if b.numShaderArrayPerChiplet%numGPCs != 0 {
			panic("numShaderArrayPerChiplet not divisible by numGPCs")
		}

		for j := i * numSAPerGPC; j < (i+1)*numSAPerGPC; j++ {
			l1stlb := chiplet.L1STLBs[j]

			l1stlbEp := multiplexer.MakeEndPointBuilder().
				WithEngine(b.engine).
				WithFreq(b.freq).
				WithDevicePorts([]akita.Port{l1stlb.GetBottomPort()}).
				WithFlitByteSize(32).
				WithNumReqPerCycle(1).
				WithNetworkPortBufferSize(1).
				Build(fmt.Sprintf("%s.L1STLB[%d]", chiplet.name, i))

			switchPort := gpcSwitch.ConnectEndPointToSwitch(l1stlbEp, 5, b.freq)
			rt.AddRoute(l1stlb.GetBottomPort(), switchPort)

			l1itlb := chiplet.L1ITLBs[j]

			l1itblEp := multiplexer.MakeEndPointBuilder().
				WithEngine(b.engine).
				WithFreq(b.freq).
				WithDevicePorts([]akita.Port{l1itlb.GetBottomPort()}).
				WithFlitByteSize(32).
				WithNumReqPerCycle(1).
				WithNetworkPortBufferSize(1).
				Build(fmt.Sprintf("%s.L1ITLB[%d]", chiplet.name, i))

			switchPort2 := gpcSwitch.ConnectEndPointToSwitch(l1itblEp, 5, b.freq)
			rt.AddRoute(l1itlb.GetBottomPort(), switchPort2)
		}

		l2tlb := chiplet.L2TLBs[i]

		ep := multiplexer.MakeEndPointBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithDevicePorts([]akita.Port{l2tlb.GetTopPort()}).
			WithFlitByteSize(32).
			WithNumReqPerCycle(8).
			WithNetworkPortBufferSize(8).
			Build(fmt.Sprintf("%s.L2TLB[%d]", chiplet.name, i))

		switchPort := gpcSwitch.ConnectEndPointToSwitch(ep, 5, b.freq)
		rt.AddRoute(l2tlb.GetTopPort(), switchPort)
	}

	// Establish ring topology connections between GPC switches
	for i := 0; i < numGPCs; i++ {
		switchA := gpcSwitches[i]
		switchB := gpcSwitches[(i+1)%numGPCs]

		portA, _ := multiplexer.ConnectSwitches(
			switchA,
			switchB,
			40,
			32,
			b.engine,
			b.freq,
		)
		switchA.GetRoutingTable().SetDefaultPort(portA)
	}
}

func (b *HierarchicalSMSideGPUBuilder) connectMMUToGlobalNoC(chiplet *Chiplet) {
	chiplet.MMU.SetLowModuleFinder(chiplet.lowModuleFinderForL1)
}

func (b *HierarchicalSMSideGPUBuilder) buildMemBanks(chiplet *Chiplet) {
	l2Builder := writeback.MakeBuilder().
		WithEngine(b.engine).
		WithFreq(b.freq).
		WithLog2BlockSize(b.log2CacheLineSize).
		WithWayAssociativity(16).
		WithByteSize(128 * mem.KB).
		WithNumMSHREntry(32).
		WithNumReqPerCycle(4).
		WithBankLatency(10).
		WithPipelineLatency(80).
		WithNumBanks(1)

	for i := 0; i < b.numMemoryBankPerChiplet; i++ {
		dramName := fmt.Sprintf("%s.DRAM_%d", chiplet.name, i)
		dram := idealmemcontroller.New(
			dramName, b.engine, 512*mem.MB)
		addrConverter := idealmemcontroller.InterleavingConverter{
			InterleavingSize:    1 << b.log2MemoryBankInterleavingSize,
			TotalNumOfElements:  b.numChiplet * b.numMemoryBankPerChiplet,
			CurrentElementIndex: b.numMemoryBankPerChiplet*int(chiplet.ChipletID) + i,
			Offset:              b.memAddrOffset,
			//  + b.memoryPerChiplet*chiplet.ChipletID,
		}
		// fmt.Println("^^^^^", b.numMemoryBankPerChiplet*int(chiplet.ChipletID)+i)
		dram.AddressConverter = addrConverter

		b.drams = append(b.drams, dram)
		b.gpu.MemoryControllers = append(b.gpu.MemoryControllers, dram)
		chiplet.DRAMs = append(chiplet.DRAMs, dram)

		if b.enableVisTracing {
			tracing.CollectTrace(dram, b.visTracer)
		}

		cacheName := fmt.Sprintf("%s.L2_%d", chiplet.name, i)
		l2 := l2Builder.Build(cacheName)
		b.l2Caches = append(b.l2Caches, l2)
		b.gpu.L2Caches = append(b.gpu.L2Caches, l2)
		chiplet.L2Caches = append(chiplet.L2Caches, l2)
		l2.SetLowModuleFinder(&cache.SingleLowModuleFinder{
			LowModule: dram.ToTop,
		})
		if b.enableVisTracing {
			tracing.CollectTrace(l2, b.visTracer)
		}

		if b.useTimeEventAnalysis {
			b.TipEngine.L2Caches = append(b.TipEngine.L2Caches, l2)
		}
	}
}

func (b *HierarchicalSMSideGPUBuilder) buildL2TLB(chiplet *Chiplet) {
	numSets := 256 // 128 // 256 // changed this here
	numWays := 8   // 8 // changed this here
	log2NumSets := int(math.Log2(float64(numSets)))

	tlbIndexBitsStart := int(math.Log2(float64(b.remoteTLBInterleavingSize))) + int(b.log2PageSize) + 1
	tlbIndexBitsEnd := tlbIndexBitsStart + int(math.Log2(float64(b.numChiplet))) - 1

	mask := uint64(0)
	t := uint64(1) << b.log2PageSize
	numBitsSet := 0
	for i := int(b.log2PageSize) + 1; i <= 64; i++ {
		if i < tlbIndexBitsStart || i > tlbIndexBitsEnd {
			mask = mask | t
			numBitsSet++
			if numBitsSet == log2NumSets {
				break
			}
		}
		t = t << 1
	}
	//fmt.Println(mask)
	maxL2PerPartition := 4
	numPartitions := (len(chiplet.L2Caches)-1)/maxL2PerPartition + 1

	for i := 0; i < numPartitions; i++ {
		if numWays%numPartitions != 0 {
			panic("numWays not divisible by numMux")
		}

		builder := tlb.MakeLatTLBBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithNumWays(numWays).
			WithNumSets(numSets / numPartitions).
			WithNumMSHREntry(256 / numPartitions).
			WithNumReqPerCycle(4).
			WithLog2PageSize(b.log2PageSize).
			WithLowModule(chiplet.MMU.ToTopPort()).
			WithIndexingMask(mask).
			WithLatency(10)
		fmt.Println("num TLB sets:", numSets)
		fmt.Println("num TLB ways:", numWays)
		if b.useCoalescingTLBPort {
			builder = builder.UseCoalescingTLBPort()
		}
		l2TLB := builder.Build(fmt.Sprintf("%s.L2TLB[%d]", chiplet.name, i))
		l2TLB.SetLowModuleFinder(&cache.SingleLowModuleFinder{
			LowModule: chiplet.MMU.ToTopPort(),
		})

		b.l2TLBs = append(b.l2TLBs, l2TLB)
		b.gpu.L2TLBs = append(b.gpu.L2TLBs, l2TLB)
		chiplet.L2TLBs = append(chiplet.L2TLBs, l2TLB)

		if b.enableVisTracing {
			tracing.CollectTrace(l2TLB, b.visTracer)
		}
	}
}

func (b *HierarchicalSMSideGPUBuilder) connectL2TLBTOMMU(chiplet *Chiplet) {
	tlbToMMUConn := akita.NewDirectConnection(chiplet.name+".L2TLB-MMU",
		b.engine, b.freq)
	tlbToMMUConn.PlugIn(chiplet.MMU.ToTopPort(), 64)
	for _, l2tlb := range chiplet.L2TLBs {
		tlbToMMUConn.PlugIn(l2tlb.GetBottomPort(), 16)
	}
}

func (b *HierarchicalSMSideGPUBuilder) setupInterchipNetwork() {
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

func (b *HierarchicalSMSideGPUBuilder) InterChipletPorts(c *Chiplet) []akita.Port {
	ports := []akita.Port{
		c.chipRdmaEngine.RequestPort,
		c.chipRdmaEngine.ResponsePort,
	}
	return ports
}
