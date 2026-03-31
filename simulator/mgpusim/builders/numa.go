package builders

import (
	"fmt"
	"log"
	"math"
	"strconv"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/mem/cache"
	"gitlab.com/akita/mem/cache/writeback"
	"gitlab.com/akita/mem/idealmemcontroller"
	"gitlab.com/akita/mem/monitor"
	"gitlab.com/akita/mem/vm/addresstranslator"
	"gitlab.com/akita/mem/vm/lds"
	"gitlab.com/akita/mem/vm/mmu"
	"gitlab.com/akita/mem/vm/mmu/asyncCaPWQ"
	"gitlab.com/akita/mem/vm/mmu/baseline"
	"gitlab.com/akita/mem/vm/mmu/caPWQL1"
	"gitlab.com/akita/mem/vm/mmu/caPWQL2"
	"gitlab.com/akita/mem/vm/mmu/caPWQL3"
	"gitlab.com/akita/mem/vm/mmu/caPWQL4"
	"gitlab.com/akita/mem/vm/mmu/caPWQL5"
	"gitlab.com/akita/mem/vm/mmu/infinite"
	"gitlab.com/akita/mem/vm/mmu/mpw"
	"gitlab.com/akita/mem/vm/tlb"
	"gitlab.com/akita/mgpusim"
	CaPWQCacheL4 "gitlab.com/akita/mgpusim/timing/caches/capwql4"
	CaPWQCacheL6 "gitlab.com/akita/mgpusim/timing/caches/capwql6"
	"gitlab.com/akita/mgpusim/timing/caches/l1v"
	"gitlab.com/akita/mgpusim/yamlconfig"
	noc "gitlab.com/akita/noc/networking/booksim"
	"gitlab.com/akita/noc/networking/chipnetwork"
	"gitlab.com/akita/noc/networking/multiplexer"
	"gitlab.com/akita/util/tracing"
)

type NUMAGPUBuilder struct {
	*CommonBuilder

	// specific componenets
	useTLBMonitor   bool
	useCaPWQMonitor bool
	useCacheTEA     bool
	ptwTracer       tracing.Tracer
	caPWQTracer     tracing.Tracer
}

func (b *NUMAGPUBuilder) WithTLBMonitor() {
	b.useTLBMonitor = true
}

func (b *NUMAGPUBuilder) WithCaPWQMonitor() {
	b.useCaPWQMonitor = true
}

func (b *NUMAGPUBuilder) WithCacheTEA() {
	b.useCacheTEA = true
}

func (b *NUMAGPUBuilder) WithPTWTracer(
	tracer tracing.Tracer,
) {
	b.ptwTracer = tracer
}

func (b *NUMAGPUBuilder) WithCaPWQTracer(
	tracer tracing.Tracer,
) {
	b.caPWQTracer = tracer
}

func MakeNUMAGPUBuilder() NUMAGPUBuilder {
	// TODO: should this be using new? is the object being allocated on the stack?
	cbp := CommonBuilder{}
	b := NUMAGPUBuilder{CommonBuilder: &cbp}
	b.SetDefaultCommonBuilderParams()
	return b
}

func (b NUMAGPUBuilder) Build(name string, id uint64) *mgpusim.GPU {
	b.createGPU(name, id)

	b.buildCP()

	chipRdmaAddressTable := b.createChipRDMAAddrTable()
	rdmaResponsePorts := make([]akita.Port, b.numChiplet)

	chipletName := fmt.Sprintf("%s.chiplet_%02d", b.gpuName, 0)
	chiplet := NewChiplet(chipletName, uint64(0))

	b.BuildSAs(chiplet)
	b.buildMemBanks(chiplet)
	b.buildL2TLB(chiplet)
	b.buildL3TLB(chiplet)
	b.buildMMU(chiplet)

	b.configChipRDMAEngine(chiplet, chipRdmaAddressTable, rdmaResponsePorts)

	b.createGlobalNoC(chiplet)

	b.establishL1ToL2RoutingPath(chiplet)
	b.establishL1TLBToL2TLBRoutingPath(chiplet)
	b.establishL2TLBToL3TLBRoutingPath(chiplet)
	b.establishMMUToL2RoutingPath(chiplet)

	b.connectL2ToDRAM(chiplet)

	b.establishTPC(chiplet)
	b.establishGPC(chiplet)
	b.establishL2Partition(chiplet)

	b.connectGlobalNoC(chiplet)

	b.chiplets = append(b.chiplets, chiplet)

	b.buildPageMigrationController()
	b.setupDMA()

	b.connectCP()
	b.setupInterchipNetwork()

	chiplet.GlobalNoC.Establish()

	b.establishTLBMonitor(chiplet)
	b.establishCaPWQMonitor(chiplet)
	b.establishCacheTEA(chiplet)

	return b.gpu
}

func (b *NUMAGPUBuilder) connectCP() {
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

// BuildSAs builds shader arrays.
func (b *NUMAGPUBuilder) BuildSAs(chiplet *Chiplet) {
	saBuilder := makeShaderArrayBuilder()
	saBuilder.withEngine(b.engine)
	saBuilder.withFreq(b.freq)
	saBuilder.withGPUID(b.gpu.GPUID)
	saBuilder.withLog2CachelineSize(b.log2CacheLineSize)
	saBuilder.withLog2PageSize(b.log2PageSize)
	saBuilder.withNumCU(b.numCUPerShaderArray)
	saBuilder.withPageTable(b.pageTable)

	switch yamlconfig.OverrideConfig["MMU.type"] {
	case "CaPWQMMUL1":
		saBuilder.withConfig("CaPWQL1")
	case "CaPWQMMUL2":
		saBuilder.withConfig("CaPWQL2")
	case "CaPWQMMUL3":
		saBuilder.withConfig("CaPWQL3")
	case "CaPWQMMUL4":
		saBuilder.withConfig("CaPWQL4")
	case "CaPWQMMUL5":
		saBuilder.withConfig("CaPWQL5")
	case "CaPWQMMUL6":
		saBuilder.withConfig("CaPWQL6")
	default:
	}

	if b.enableVisTracing {
		saBuilder.withVisTracer(b.visTracer)
	}

	if b.enableTLBTracing {
		saBuilder.withTLBTracer(b.tlbTracer)
	}

	maxCUsPerGPC := 16
	if b.numShaderArrayPerChiplet%maxCUsPerGPC != 0 {
		panic("numShaderArrayPerChiplet should be divisible by maxCUsPerGPC")
	}

	maxSAsPerGPC := maxCUsPerGPC / b.numCUPerShaderArray
	for i := 0; i < b.numShaderArrayPerChiplet; i++ {
		gpcID := i / maxSAsPerGPC
		innerSAID := i % maxSAsPerGPC

		saName := fmt.Sprintf(
			"%s.GPC_%02d.SA_%02d",
			chiplet.name,
			gpcID,
			innerSAID,
		)
		sa := saBuilder.Build(saName, i)
		b.collectSAComponents(sa, chiplet)
	}
}

func (b *NUMAGPUBuilder) createGlobalNoC(chiplet *Chiplet) {
	chiplet.GlobalNoC = noc.NewHybridBookSimNoC(
		fmt.Sprintf("HybridGlobalNoC[%d]", chiplet.ChipletID),
		b.engine,
	)

	b.gpu.NoCs = append(b.gpu.NoCs, chiplet.GlobalNoC)

	maxCUsPerGPC := 16     // same as NVIDIA A100
	maxL2PerPartition := 8 // same as NVIDIA V100

	chiplet.GlobalNoC.MaxNumSMSidePort = ((len(chiplet.CUs) - 1) / maxCUsPerGPC) + 1
	chiplet.GlobalNoC.MaxNumSMSideNode = chiplet.GlobalNoC.MaxNumSMSidePort * 4

	chiplet.GlobalNoC.MaxNumMemSidePort = ((len(chiplet.L2Caches) - 1) / maxL2PerPartition) + 1
	chiplet.GlobalNoC.MaxNumMemSideNode = chiplet.GlobalNoC.MaxNumMemSidePort * 8

	// Monolithic Page Walk Cache
	chiplet.GlobalNoC.MaxNumMemSidePort++
	chiplet.GlobalNoC.MaxNumMemSideNode++

	// Monolithic L3 TLB
	chiplet.GlobalNoC.MaxNumMemSidePort += 2
	chiplet.GlobalNoC.MaxNumMemSideNode += 2

	log.Printf("%s has %d SM side components and %d Mem side components\n",
		chiplet.GlobalNoC.Name(), chiplet.GlobalNoC.MaxNumSMSidePort, chiplet.GlobalNoC.MaxNumMemSidePort)

	chiplet.GlobalNoC.CreateNetworkWithLib(
		b.booksimDir+"libintersim.dylib", b.booksimGlobal,
	)
}

func (b *NUMAGPUBuilder) establishL1ToL2RoutingPath(chiplet *Chiplet) {
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

	for _, comp := range chiplet.MMUs {
		switch walker := comp.(type) {
		case *baseline.MMUImpl:
			walker.DramLowModuleFinder = lowModuleFinder
		default:
			panic("MMU type not supported in establishL1ToL2RoutingPath")
		}
	}
}

func (b *NUMAGPUBuilder) establishL1TLBToL2TLBRoutingPath(chiplet *Chiplet) {
	numCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/numCUsPerGPC + 1

	for i := 0; i < numGPCs; i++ {
		singeLowModuleFinder := new(cache.SingleLowModuleFinder)
		singeLowModuleFinder.LowModule = chiplet.L2TLBs[i].GetTopPort()

		for j := i * numCUsPerGPC; j < (i+1)*numCUsPerGPC; j++ {
			chiplet.L1VTLBs[j].SetLowModuleFinder(singeLowModuleFinder)
		}

		numSAPerGPC := b.numShaderArrayPerChiplet / numGPCs
		if b.numShaderArrayPerChiplet%numGPCs != 0 {
			panic("numShaderArrayPerChiplet not divisible by numGPCs")
		}

		for j := i * numSAPerGPC; j < (i+1)*numSAPerGPC; j++ {
			chiplet.L1STLBs[j].SetLowModuleFinder(singeLowModuleFinder)
			chiplet.L1ITLBs[j].SetLowModuleFinder(singeLowModuleFinder)
		}
	}
}

func (b *NUMAGPUBuilder) establishTPC(chiplet *Chiplet) {
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
			WithNumReqPerCycle(4).
			WithSwitchLatency(2).
			WithBufferSizeInNumFlit(16).
			WithRoutingTable(routingTable).
			Build(fmt.Sprintf("%s.GPC[%d].TPCMux[%d]", chiplet.name, gpcID, localID))

		chiplet.tpcMux = append(chiplet.tpcMux, mux)
	}

	for i, l1v := range chiplet.L1VCaches {
		devicePorts := []akita.Port{l1v.GetBottomPort()}

		//if l1v.GetWalkerPort() != nil {
		//	devicePorts = append(devicePorts, l1v.GetWalkerPort())
		//}

		ep := multiplexer.MakeEndPointBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithDevicePorts(devicePorts).
			WithNumReqPerCycle(2).
			WithNetworkPortBufferSize(2).
			WithFlitByteSize(32).
			Build(fmt.Sprintf("%s.L1VCache[%d]", chiplet.name, i))

		tpcID := i / 2
		mux := chiplet.tpcMux[tpcID]

		localPort := mux.AddLowSidePort(ep)
		for _, port := range devicePorts {
			mux.AddRoute(port, localPort)
		}
	}

	for i, l1vtlb := range chiplet.L1VTLBs {
		ep := multiplexer.MakeEndPointBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithDevicePorts([]akita.Port{l1vtlb.GetBottomPort()}).
			WithNumReqPerCycle(2).
			WithNetworkPortBufferSize(2).
			WithFlitByteSize(32).
			Build(fmt.Sprintf("%s.L1VTLB[%d]", chiplet.name, i))

		tpcID := i / 2
		mux := chiplet.tpcMux[tpcID]

		localPort := mux.AddLowSidePort(ep)
		mux.AddRoute(l1vtlb.GetBottomPort(), localPort)
	}
}

func (b *NUMAGPUBuilder) establishGPC(chiplet *Chiplet) {
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

	for i, l1itlb := range chiplet.L1ITLBs {
		ep := multiplexer.MakeEndPointBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithDevicePorts([]akita.Port{l1itlb.GetBottomPort()}).
			WithFlitByteSize(32).
			WithNumReqPerCycle(1).
			WithNetworkPortBufferSize(1).
			Build(fmt.Sprintf("%s.L1ITLB[%d]", chiplet.name, i))

		gpcID := i / numSAPerGPC
		mux := chiplet.gpcMux[gpcID]

		localPort := mux.AddLowSidePort(ep)
		mux.AddRoute(l1itlb.GetBottomPort(), localPort)
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

		gpcID := i / numSAPerGPC
		mux := chiplet.gpcMux[gpcID]

		localPort := mux.AddLowSidePort(ep)
		mux.AddRoute(l1stlb.GetBottomPort(), localPort)
	}

	for i, l2tlb := range chiplet.L2TLBs {
		ep := multiplexer.MakeEndPointBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithDevicePorts([]akita.Port{l2tlb.GetBottomPort()}).
			WithFlitByteSize(32).
			WithNumReqPerCycle(4).
			WithNetworkPortBufferSize(4).
			Build(fmt.Sprintf("%s.L2TLB[%d]", chiplet.name, i))

		mux := chiplet.gpcMux[i]

		localPort := mux.AddLowSidePort(ep)
		mux.AddRoute(l2tlb.GetBottomPort(), localPort)
	}

	for i, mmu := range chiplet.MMUs {
		ep := multiplexer.MakeEndPointBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithDevicePorts([]akita.Port{
				mmu.ToTranslationPort(),
				mmu.ToPageWalkCachePort(),
				mmu.ToTopPort(),
			}).
			WithFlitByteSize(32).
			WithNumReqPerCycle(4).
			WithNetworkPortBufferSize(4).
			Build(fmt.Sprintf("%s.MMU[%d]", chiplet.name, i))

		mux := chiplet.gpcMux[i]

		localPort := mux.AddLowSidePort(ep)
		mux.AddRoute(mmu.ToTranslationPort(), localPort)
		mux.AddRoute(mmu.ToPageWalkCachePort(), localPort)
		mux.AddRoute(mmu.ToTopPort(), localPort)
	}
}

func (b *NUMAGPUBuilder) establishL2Partition(chiplet *Chiplet) {
	maxL2PerPartition := 8
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

func (b *NUMAGPUBuilder) connectGlobalNoC(chiplet *Chiplet) {
	if len(chiplet.gpcMux)%2 != 0 {
		panic("number of GPC mux should be even")
	}

	for i, mux := range chiplet.gpcMux {
		ep := multiplexer.MakeHybridEndPointBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithFlitByteSize(32).
			WithFlitAssemblingBufferSize(128).
			WithNumReqPerCycle(64).
			WithNetworkPortBufferSize(64).
			WithDevicePorts([]akita.Port{
				chiplet.L2TLBs[i].GetTopPort(),
				//chiplet.MMUs[i].ToCachePort(),
			}).
			Build(fmt.Sprintf("%s.GPCHighSideEndPoint[%d]", chiplet.name, i))

		mux.SetHighSideHybridEndPoint(ep)

		subNetworkID := 0
		if i >= len(chiplet.gpcMux)/2 {
			subNetworkID = 1
		}

		nocPort := chiplet.GlobalNoC.PlugInNUMASMSideMultiPort(
			ep.NetworkPort,
			64,
			4,
			subNetworkID,
		)
		for _, port := range mux.RoutingTable.GetAllSrcPorts() {
			chiplet.GlobalNoC.AddRoute(port, ep.NetworkPort)
		}
		ep.PlugInNoCPort(nocPort, 64)
	}

	if len(chiplet.l2Mux)%2 != 0 {
		panic("number of L2 mux should be even")
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

		subNetworkID := 0
		if i >= len(chiplet.l2Mux)/2 {
			subNetworkID = 1
		}

		nocPort := chiplet.GlobalNoC.PlugInNUMAMemSideMultiPort(
			ep.NetworkPort,
			64,
			8,
			subNetworkID,
		)
		for _, port := range mux.RoutingTable.GetAllSrcPorts() {
			chiplet.GlobalNoC.AddRoute(port, ep.NetworkPort)
		}
		ep.PlugInNoCPort(nocPort, 64)
	}

	chiplet.GlobalNoC.PlugInMemSideMultiPort(chiplet.L3TLBs[0].GetTopPort(), 64, 1)
	chiplet.GlobalNoC.PlugInMemSideMultiPort(chiplet.L3TLBs[0].GetBottomPort(), 64, 1)
	chiplet.GlobalNoC.PlugInMemSideMultiPort(
		chiplet.L3TLBs[0].(*tlb.LastLevelTLB).PWCWritePort,
		64,
		1,
	)
}

func (b *NUMAGPUBuilder) establishL2TLBToL3TLBRoutingPath(chiplet *Chiplet) {
	numCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/numCUsPerGPC + 1

	singleLowModuleFinder := new(cache.SingleLowModuleFinder)
	singleLowModuleFinder.LowModule = chiplet.L3TLBs[0].GetTopPort()

	for i := 0; i < numGPCs; i++ {
		chiplet.L2TLBs[i].SetLowModuleFinder(singleLowModuleFinder)
	}

	chiplet.L3TLBs[0].SetTLBFinder(singleLowModuleFinder)
}

func (b *NUMAGPUBuilder) establishMMUToL2RoutingPath(chiplet *Chiplet) {
	for _, mmu := range chiplet.MMUs {
		mmu.SetLowModuleFinder(chiplet.lowModuleFinderForL1)
	}
}

func (b *NUMAGPUBuilder) buildMemBanks(chiplet *Chiplet) {
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

	maxSlicesPerPartition := 8
	if b.numMemoryBankPerChiplet%maxSlicesPerPartition != 0 {
		panic("numMemoryBankPerChiplet should be divisible by maxSlicesPerPartition")
	}

	for i := 0; i < b.numMemoryBankPerChiplet; i++ {
		partitionID := i / maxSlicesPerPartition
		innerID := i % maxSlicesPerPartition

		dramName := fmt.Sprintf(
			"%s.MP_%02d.DRAM_%d",
			chiplet.name,
			partitionID,
			innerID,
		)
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

		cacheName := fmt.Sprintf(
			"%s.MP_%02d.L2_%02d",
			chiplet.name,
			partitionID,
			innerID,
		)
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
	}
}

func (b *NUMAGPUBuilder) buildL2TLB(chiplet *Chiplet) {
	numSets := 64
	numWays := 8
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

	numCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/numCUsPerGPC + 1

	for i := 0; i < numGPCs; i++ {
		builder := tlb.MakeLatTLBBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithNumWays(numWays).
			WithNumSets(numSets).
			WithNumMSHREntry(128).
			WithNumReqPerCycle(4).
			WithLog2PageSize(b.log2PageSize).
			WithIndexingMask(mask).
			WithLatency(40)

		if b.useCoalescingTLBPort {
			builder = builder.UseCoalescingTLBPort()
		}
		l2TLB := builder.Build(fmt.Sprintf("%s.GPC_%02d_L2TLB", chiplet.name, i))

		b.l2TLBs = append(b.l2TLBs, l2TLB)
		b.gpu.L2TLBs = append(b.gpu.L2TLBs, l2TLB)
		chiplet.L2TLBs = append(chiplet.L2TLBs, l2TLB)

		if b.enableVisTracing {
			tracing.CollectTrace(l2TLB, b.visTracer)
		}
	}
}

func (b *NUMAGPUBuilder) buildL3TLB(chiplet *Chiplet) {
	numSets := 256
	numWays := 8
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

	dispatchPolicy := yamlconfig.OverrideConfig["L3TLB.dispatcher"]

	builder := tlb.MakeLastLevelTLBBuilder().
		WithEngine(b.engine).
		WithFreq(b.freq).
		WithNumWays(numWays).
		WithNumSets(numSets).
		WithNumMSHREntry(512).
		WithNumReqPerCycle(8).
		WithLog2PageSize(b.log2PageSize).
		WithPageWalkCacheSize(2048).
		WithDispatchPolicy(dispatchPolicy).
		WithLatency(80)

	if b.useCoalescingTLBPort {
		builder = builder.UseCoalescingTLBPort()
	}
	l3TLB := builder.Build(fmt.Sprintf("%s.L3TLB", chiplet.name))

	b.l3TLBs = append(b.l3TLBs, l3TLB)
	b.gpu.L3TLBs = append(b.gpu.L3TLBs, l3TLB)
	chiplet.L3TLBs = append(chiplet.L3TLBs, l3TLB)

	if b.enableVisTracing {
		tracing.CollectTrace(l3TLB, b.visTracer)
	}
}

func (b *NUMAGPUBuilder) buildMMU(chiplet *Chiplet) {
	if yamlconfig.OverrideConfig == nil {
		b.buildDefaultMMU(chiplet)
	} else {
		mmuType := yamlconfig.OverrideConfig["MMU.type"]

		switch mmuType {
		case "BaselineMMU":
			b.buildDefaultMMU(chiplet)
		case "IdealMMU":
			b.buildIdealMMU(chiplet)
		case "InfiniteMMU":
			b.buildInfiniteMMU(chiplet)
		case "MPWMMU":
			b.buildMPWMMU(chiplet)
		case "CaPWQMMUL1":
			b.buildCaPWQMMUL1(chiplet)
		case "CaPWQMMUL2":
			b.buildCaPWQMMUL2(chiplet)
		case "CaPWQMMUL3":
			b.buildCaPWQMMUL3(chiplet)
		case "CaPWQMMUL4":
			b.buildCaPWQMMUL4(chiplet)
		case "CaPWQMMUL5":
			b.buildCaPWQMMUL5(chiplet)
		case "CaPWQMMUL6":
			b.buildCaPWQMMUL6(chiplet)
		case "AsyncCaPWQMMU":
			b.buildAsyncCaPWQMMU(chiplet)
		default:
			log.Panicf("Unsupported MMU type: %s\n", mmuType)
		}
	}

	for _, mmu := range chiplet.MMUs {
		if b.ptwTracer != nil {
			tracing.CollectTrace(mmu, b.ptwTracer)
		}

		b.l3TLBs[0].(*tlb.LastLevelTLB).RegisterMMU(
			mmu.ToTopPort(),
		)
	}
}

func (b *NUMAGPUBuilder) buildDefaultMMU(chiplet *Chiplet) {
	maxNumReqInFlight := 16

	if numWalkers, ok := yamlconfig.OverrideConfig["MMU.numPageWalkers"]; ok {
		numWalkersInt, err := strconv.Atoi(numWalkers)
		if err != nil {
			log.Panicf("Invalid number of walkers %v\n", numWalkersInt)
		}

		maxNumReqInFlight = numWalkersInt
	}

	maxCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/maxCUsPerGPC + 1

	if maxNumReqInFlight%numGPCs != 0 {
		panic("numPageWalkers should be divisible by numGPCs")
	}

	for i := 0; i < numGPCs; i++ {
		component := baseline.MakeMMUBuilder().
			WithEngine(b.engine).
			WithFreq(1 * akita.GHz).
			WithLog2PageSize(b.log2PageSize).
			WithPageTable(b.pageTable).
			WithMaxNumReqInFlight(maxNumReqInFlight / numGPCs).
			Build(fmt.Sprintf("%s.GPC_%02d.BaselineMMU", chiplet.name, i))

		pageWalkCachePort := chiplet.L3TLBs[0].(*tlb.LastLevelTLB).PWCWritePort

		component.(*baseline.MMUImpl).PageWalkCache = pageWalkCachePort

		chiplet.MMUs = append(chiplet.MMUs, component)
		b.gpu.MMUs = append(b.gpu.MMUs, component)

		for _, dram := range chiplet.DRAMs {
			component.(*baseline.MMUImpl).Drams = append(
				component.(*baseline.MMUImpl).Drams, dram,
			)
		}

		for _, l2 := range chiplet.L2Caches {
			component.(*baseline.MMUImpl).L2Caches = append(
				component.(*baseline.MMUImpl).L2Caches, l2,
			)
		}

		component.(*baseline.MMUImpl).CPU = b.cpuStorage
	}
}

func (b *NUMAGPUBuilder) buildInfiniteMMU(chiplet *Chiplet) {
	maxCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/maxCUsPerGPC + 1

	for i := 0; i < numGPCs; i++ {
		component := infinite.MakeMMUBuilder().
			WithEngine(b.engine).
			WithFreq(1 * akita.GHz).
			WithLog2PageSize(b.log2PageSize).
			WithPageTable(b.pageTable).
			Build(fmt.Sprintf("%s.GPC_%02d.InfiniteMMU", chiplet.name, i))

		pageWalkCachePort := chiplet.L3TLBs[0].(*tlb.LastLevelTLB).PWCWritePort

		component.(*infinite.MMUImpl).PageWalkCache = pageWalkCachePort

		chiplet.MMUs = append(chiplet.MMUs, component)
		b.gpu.MMUs = append(b.gpu.MMUs, component)
	}
}

func (b *NUMAGPUBuilder) buildMPWMMU(chiplet *Chiplet) {
	maxNumReqInFlight := 16

	if numWalkers, ok := yamlconfig.OverrideConfig["MMU.numPageWalkers"]; ok {
		numWalkersInt, err := strconv.Atoi(numWalkers)
		if err != nil {
			log.Panicf("Invalid number of walkers %v\n", numWalkersInt)
		}

		maxNumReqInFlight = numWalkersInt
	}

	maxCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/maxCUsPerGPC + 1

	if maxNumReqInFlight%numGPCs != 0 {
		panic("numPageWalkers should be divisible by numGPCs")
	}

	for i := 0; i < numGPCs; i++ {
		component := mpw.MakeMPWMMUBuilder().
			WithEngine(b.engine).
			WithFreq(1 * akita.GHz).
			WithLog2PageSize(b.log2PageSize).
			WithPageTable(b.pageTable).
			WithMaxNumReqInFlight(maxNumReqInFlight / numGPCs).
			Build(fmt.Sprintf("%s.GPC_%02d.mpwMMU", chiplet.name, i))

		pageWalkCachePort := chiplet.L3TLBs[0].(*tlb.LastLevelTLB).PWCWritePort

		component.(*mpw.MPWMMU).PageWalkCache = pageWalkCachePort

		chiplet.MMUs = append(chiplet.MMUs, component)
		b.gpu.MMUs = append(b.gpu.MMUs, component)
	}
}

func (b *NUMAGPUBuilder) buildIdealMMU(chiplet *Chiplet) {
	pageWalkLatency := 200
	maxActiveWalkers := 0

	if latency, ok := yamlconfig.OverrideConfig["MMU.walkLatency"]; ok {
		latencyInt, err := strconv.Atoi(latency)
		if err != nil {
			log.Panicf("Invalid walk latency: %v\n", latency)
		}

		pageWalkLatency = latencyInt
	}

	if numWalkers, ok := yamlconfig.OverrideConfig["MMU.numPageWalkers"]; ok {
		numWalkersInt, err := strconv.Atoi(numWalkers)
		if err != nil {
			log.Panicf("Invalid number of walkers %v\n", numWalkersInt)
		}

		maxActiveWalkers = numWalkersInt
	}

	maxCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/maxCUsPerGPC + 1

	if maxActiveWalkers%numGPCs != 0 {
		panic("numPageWalkers should be divisible by numGPCs")
	}

	for i := 0; i < numGPCs; i++ {
		component := mmu.MakeIdealMMUBuilder().
			WithEngine(b.engine).
			WithFreq(1 * akita.GHz).
			WithLog2PageSize(b.log2PageSize).
			WithPageTable(b.pageTable).
			WithLatency(pageWalkLatency).
			WithMaxActiveTransactions(uint64(maxActiveWalkers / numGPCs)).
			Build(fmt.Sprintf("%s.GPC_%02d.IdealMMU", chiplet.name, i))

		chiplet.MMUs = append(chiplet.MMUs, component)
		b.gpu.MMUs = append(b.gpu.MMUs, component)
	}
}

func (b *NUMAGPUBuilder) buildCaPWQMMUL1(chiplet *Chiplet) {
	maxNumReqInFlight := 16

	if numWalkers, ok := yamlconfig.OverrideConfig["MMU.numPageWalkers"]; ok {
		numWalkersInt, err := strconv.Atoi(numWalkers)
		if err != nil {
			log.Panicf("Invalid number of walkers %v\n", numWalkersInt)
		}

		maxNumReqInFlight = numWalkersInt
	}

	maxCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/maxCUsPerGPC + 1

	if maxNumReqInFlight%numGPCs != 0 {
		panic("numPageWalkers should be divisible by numGPCs")
	}

	for i := 0; i < numGPCs; i++ {
		component := caPWQL1.MakeCaPWQMMUBuilder().
			WithEngine(b.engine).
			WithFreq(1 * akita.GHz).
			WithLog2PageSize(b.log2PageSize).
			WithPageTable(b.pageTable).
			WithMaxNumReqInFlight(maxNumReqInFlight / numGPCs).
			Build(fmt.Sprintf("%s.GPC_%02d.CaPWQMMUL1", chiplet.name, i))

		pageWalkCachePort := chiplet.L3TLBs[0].(*tlb.LastLevelTLB).PWCWritePort

		component.(*caPWQL1.CaPWQMMU).PageWalkCache = pageWalkCachePort
		component.(*caPWQL1.CaPWQMMU).L3TLB = chiplet.L3TLBs[0].GetBottomPort()

		chiplet.MMUs = append(chiplet.MMUs, component)
		b.gpu.MMUs = append(b.gpu.MMUs, component)
	}

	b.establishMMUToL1RoutingPath(chiplet)
}

func (b *NUMAGPUBuilder) buildCaPWQMMUL2(chiplet *Chiplet) {
	maxNumReqInFlight := 16

	if numWalkers, ok := yamlconfig.OverrideConfig["MMU.numPageWalkers"]; ok {
		numWalkersInt, err := strconv.Atoi(numWalkers)
		if err != nil {
			log.Panicf("Invalid number of walkers %v\n", numWalkersInt)
		}

		maxNumReqInFlight = numWalkersInt
	}

	maxCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/maxCUsPerGPC + 1

	if maxNumReqInFlight%numGPCs != 0 {
		panic("numPageWalkers should be divisible by numGPCs")
	}

	for i := 0; i < numGPCs; i++ {
		component := caPWQL2.MakeCaPWQMMUBuilder().
			WithEngine(b.engine).
			WithFreq(1 * akita.GHz).
			WithLog2PageSize(b.log2PageSize).
			WithPageTable(b.pageTable).
			WithMaxNumReqInFlight(maxNumReqInFlight / numGPCs).
			Build(fmt.Sprintf("%s.GPC_%02d.CaPWQMMUL2", chiplet.name, i))

		pageWalkCachePort := chiplet.L3TLBs[0].(*tlb.LastLevelTLB).PWCWritePort

		component.(*caPWQL2.CaPWQMMU).PageWalkCache = pageWalkCachePort
		component.(*caPWQL2.CaPWQMMU).L3TLB = chiplet.L3TLBs[0].GetBottomPort()

		chiplet.MMUs = append(chiplet.MMUs, component)
		b.gpu.MMUs = append(b.gpu.MMUs, component)
	}

	b.establishMMUToL1RoutingPath(chiplet)
}

func (b *NUMAGPUBuilder) buildCaPWQMMUL3(chiplet *Chiplet) {
	maxNumReqInFlight := 16

	if numWalkers, ok := yamlconfig.OverrideConfig["MMU.numPageWalkers"]; ok {
		numWalkersInt, err := strconv.Atoi(numWalkers)
		if err != nil {
			log.Panicf("Invalid number of walkers %v\n", numWalkersInt)
		}

		maxNumReqInFlight = numWalkersInt
	}

	maxCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/maxCUsPerGPC + 1

	if maxNumReqInFlight%numGPCs != 0 {
		panic("numPageWalkers should be divisible by numGPCs")
	}

	for i := 0; i < numGPCs; i++ {
		component := caPWQL3.MakeCaPWQMMUBuilder().
			WithEngine(b.engine).
			WithFreq(1 * akita.GHz).
			WithLog2PageSize(b.log2PageSize).
			WithPageTable(b.pageTable).
			WithMaxNumReqInFlight(maxNumReqInFlight / numGPCs).
			Build(fmt.Sprintf("%s.GPC_%02d.CaPWQMMUL3", chiplet.name, i))

		pageWalkCachePort := chiplet.L3TLBs[0].(*tlb.LastLevelTLB).PWCWritePort

		component.(*caPWQL3.CaPWQMMU).PageWalkCache = pageWalkCachePort
		component.(*caPWQL3.CaPWQMMU).L3TLB = chiplet.L3TLBs[0].GetBottomPort()

		chiplet.MMUs = append(chiplet.MMUs, component)
		b.gpu.MMUs = append(b.gpu.MMUs, component)
	}

	b.establishMMUToL1RoutingPath(chiplet)
}

func (b *NUMAGPUBuilder) buildCaPWQMMUL4(chiplet *Chiplet) {
	maxNumReqInFlight := 16

	if numWalkers, ok := yamlconfig.OverrideConfig["MMU.numPageWalkers"]; ok {
		numWalkersInt, err := strconv.Atoi(numWalkers)
		if err != nil {
			log.Panicf("Invalid number of walkers %v\n", numWalkersInt)
		}

		maxNumReqInFlight = numWalkersInt
	}

	maxCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/maxCUsPerGPC + 1

	if maxNumReqInFlight%numGPCs != 0 {
		panic("numPageWalkers should be divisible by numGPCs")
	}

	for i := 0; i < numGPCs; i++ {
		component := caPWQL4.MakeCaPWQMMUBuilder().
			WithEngine(b.engine).
			WithFreq(1 * akita.GHz).
			WithLog2PageSize(b.log2PageSize).
			WithPageTable(b.pageTable).
			WithMaxNumReqInFlight(maxNumReqInFlight / numGPCs).
			Build(fmt.Sprintf("%s.GPC_%02d.CaPWQMMUL4", chiplet.name, i))

		pageWalkCachePort := chiplet.L3TLBs[0].(*tlb.LastLevelTLB).PWCWritePort

		component.(*caPWQL4.CaPWQMMU).PageWalkCache = pageWalkCachePort
		component.(*caPWQL4.CaPWQMMU).L3TLB = chiplet.L3TLBs[0].GetBottomPort()

		chiplet.MMUs = append(chiplet.MMUs, component)
		b.gpu.MMUs = append(b.gpu.MMUs, component)
	}

	b.establishMMUToL1RoutingPath(chiplet)
}

func (b *NUMAGPUBuilder) buildCaPWQMMUL5(chiplet *Chiplet) {
	maxNumReqInFlight := 16

	if numWalkers, ok := yamlconfig.OverrideConfig["MMU.numPageWalkers"]; ok {
		numWalkersInt, err := strconv.Atoi(numWalkers)
		if err != nil {
			log.Panicf("Invalid number of walkers %v\n", numWalkersInt)
		}

		maxNumReqInFlight = numWalkersInt
	}

	maxCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/maxCUsPerGPC + 1

	if maxNumReqInFlight%numGPCs != 0 {
		panic("numPageWalkers should be divisible by numGPCs")
	}

	for i := 0; i < numGPCs; i++ {
		component := caPWQL5.MakeCaPWQMMUBuilder().
			WithEngine(b.engine).
			WithFreq(1 * akita.GHz).
			WithLog2PageSize(b.log2PageSize).
			WithPageTable(b.pageTable).
			WithMaxNumReqInFlight(maxNumReqInFlight / numGPCs).
			Build(fmt.Sprintf("%s.GPC_%02d.CaPWQMMUL5", chiplet.name, i))

		pageWalkCachePort := chiplet.L3TLBs[0].(*tlb.LastLevelTLB).PWCWritePort

		component.(*caPWQL5.CaPWQMMU).PageWalkCache = pageWalkCachePort
		component.(*caPWQL5.CaPWQMMU).L3TLB = chiplet.L3TLBs[0].GetBottomPort()

		chiplet.MMUs = append(chiplet.MMUs, component)
		b.gpu.MMUs = append(b.gpu.MMUs, component)
	}

	b.establishMMUToL1RoutingPath(chiplet)
}

func (b *NUMAGPUBuilder) buildCaPWQMMUL6(chiplet *Chiplet) {
	maxNumReqInFlight := 16

	if numWalkers, ok := yamlconfig.OverrideConfig["MMU.numPageWalkers"]; ok {
		numWalkersInt, err := strconv.Atoi(numWalkers)
		if err != nil {
			log.Panicf("Invalid number of walkers %v\n", numWalkersInt)
		}

		maxNumReqInFlight = numWalkersInt
	}

	maxCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/maxCUsPerGPC + 1

	if maxNumReqInFlight%numGPCs != 0 {
		panic("numPageWalkers should be divisible by numGPCs")
	}

	for i := 0; i < numGPCs; i++ {
		// Use the same MMU as CaPWQMMUL5, but with different CaPWQCache.
		component := caPWQL5.MakeCaPWQMMUBuilder().
			WithEngine(b.engine).
			WithFreq(1 * akita.GHz).
			WithLog2PageSize(b.log2PageSize).
			WithPageTable(b.pageTable).
			WithMaxNumReqInFlight(maxNumReqInFlight / numGPCs).
			Build(fmt.Sprintf("%s.GPC_%02d.CaPWQMMUL6", chiplet.name, i))

		pageWalkCachePort := chiplet.L3TLBs[0].(*tlb.LastLevelTLB).PWCWritePort

		component.(*caPWQL5.CaPWQMMU).PageWalkCache = pageWalkCachePort
		component.(*caPWQL5.CaPWQMMU).L3TLB = chiplet.L3TLBs[0].GetBottomPort()

		chiplet.MMUs = append(chiplet.MMUs, component)
		b.gpu.MMUs = append(b.gpu.MMUs, component)
	}

	b.establishMMUToCaPWQL6L1RoutingPath(chiplet)
}

func (b *NUMAGPUBuilder) buildAsyncCaPWQMMU(chiplet *Chiplet) {
	maxNumReqInFlight := 16

	if numWalkers, ok := yamlconfig.OverrideConfig["MMU.numPageWalkers"]; ok {
		numWalkersInt, err := strconv.Atoi(numWalkers)
		if err != nil {
			log.Panicf("Invalid number of walkers %v\n", numWalkersInt)
		}

		maxNumReqInFlight = numWalkersInt
	}

	maxCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/maxCUsPerGPC + 1

	if maxNumReqInFlight%numGPCs != 0 {
		panic("numPageWalkers should be divisible by numGPCs")
	}

	for i := 0; i < numGPCs; i++ {
		component := asyncCaPWQ.MakeAsyncCaPWQMMUBuilder().
			WithEngine(b.engine).
			WithFreq(1 * akita.GHz).
			WithLog2PageSize(b.log2PageSize).
			WithPageTable(b.pageTable).
			WithMaxNumReqInFlight(maxNumReqInFlight / numGPCs).
			Build(fmt.Sprintf("%s.GPC_%02d.AsyncCaPWQMMU", chiplet.name, i))

		pageWalkCachePort := chiplet.L3TLBs[0].(*tlb.LastLevelTLB).PWCWritePort

		component.(*asyncCaPWQ.AsyncCaPWQMMU).PageWalkCache = pageWalkCachePort
		component.(*asyncCaPWQ.AsyncCaPWQMMU).L3TLB = chiplet.L3TLBs[0].GetBottomPort()

		chiplet.MMUs = append(chiplet.MMUs, component)
		b.gpu.MMUs = append(b.gpu.MMUs, component)
	}

	b.establishMMUToL1RoutingPath(chiplet)
	b.establishMMUToLDSRoutingPath(chiplet)
}

func (b *NUMAGPUBuilder) establishMMUToLDSRoutingPath(chiplet *Chiplet) {
	numCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/numCUsPerGPC + 1

	if len(chiplet.MMUs) != numGPCs {
		panic("number of MMUs should be the same as number of GPCs")
	}

	for i := 0; i < numGPCs; i++ {
		mmuSwitch := multiplexer.SwitchBuilder{}.
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithArbiter(multiplexer.NewRRArbiter()).
			WithRoutingTable(multiplexer.NewMapRoutingTable()).
			WithNumReqPerCycle(32).
			WithBufferSizeInNumFlit(32).
			Build(fmt.Sprintf("%s.MMUSwitch_%02d", chiplet.name, i))

		idealLDS := lds.NewIdealCaPWQLDS(
			fmt.Sprintf("%s.L1IdealCaPWQLDS_%02d", chiplet.name, i),
			b.engine,
			b.freq,
			4,
			28,
		)

		epLDS := multiplexer.MakeEndPointBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithDevicePorts([]akita.Port{idealLDS.GetMMUSidePort()}).
			WithNumReqPerCycle(4).
			WithFlitByteSize(64).
			Build(fmt.Sprintf("%s.L1IdealCaPWQLDS_%02d", chiplet.name, i))

		switchPortLDS := mmuSwitch.ConnectEndPointToSwitch(epLDS, 5, b.freq)
		rt := mmuSwitch.GetRoutingTable()
		rt.AddRoute(idealLDS.GetMMUSidePort(), switchPortLDS)

		b.gpu.L1CaPWQLDS = append(b.gpu.L1CaPWQLDS, idealLDS)

		epMMU := multiplexer.MakeEndPointBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithDevicePorts([]akita.Port{chiplet.MMUs[i].(*asyncCaPWQ.AsyncCaPWQMMU).ToLDS}).
			WithFlitByteSize(64).
			WithNumReqPerCycle(16).
			WithNetworkPortBufferSize(16).
			Build(fmt.Sprintf("%s.MMU_%02d", chiplet.name, i))

		switchPortMMU := mmuSwitch.ConnectEndPointToSwitch(epMMU, 5, b.freq)
		rt.AddRoute(chiplet.MMUs[i].(*asyncCaPWQ.AsyncCaPWQMMU).ToLDS, switchPortMMU)

		chiplet.MMUs[i].(*asyncCaPWQ.AsyncCaPWQMMU).LDS = idealLDS.GetMMUSidePort()
	}
}

func (b *NUMAGPUBuilder) establishMMUToL1RoutingPath(chiplet *Chiplet) {
	numVCachesPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/numVCachesPerGPC + 1

	if len(chiplet.MMUs) != numGPCs {
		panic("number of MMUs should be the same as number of GPCs")
	}

	for i := 0; i < numGPCs; i++ {
		vlowModuleFinder := cache.NewXORLowModuleFinder(
			numVCachesPerGPC,
			4,
			int(math.Log2(float64(numVCachesPerGPC))),
			int(b.log2CacheLineSize))

		switch mmu := chiplet.MMUs[i].(type) {
		case *caPWQL5.CaPWQMMU:
			mmu.VCacheLowModuleFinder = vlowModuleFinder
		default:
			panic("MMU is not CaPWQMMU")
		}

		conn := akita.NewDirectConnection(
			fmt.Sprintf("%s.MMU_%02d_To_L1Conn", chiplet.name, i),
			b.engine, 1*akita.GHz,
		)

		conn.PlugIn(chiplet.MMUs[i].ToCachePort(), 16)

		for j := i * numVCachesPerGPC; j < (i+1)*numVCachesPerGPC; j++ {
			vlowModuleFinder.LowModules = append(
				vlowModuleFinder.LowModules,
				chiplet.L1VCaches[j].GetWalkerPort(),
			)
			conn.PlugIn(chiplet.L1VCaches[j].GetWalkerPort(), 16)

			chiplet.L1VCaches[j].(*CaPWQCacheL4.Cache).PageWalker =
				chiplet.MMUs[i].ToCachePort()
		}
	}
}

func (b *NUMAGPUBuilder) establishMMUToCaPWQL6L1RoutingPath(chiplet *Chiplet) {
	numVCachesPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/numVCachesPerGPC + 1

	if len(chiplet.MMUs) != numGPCs {
		panic("number of MMUs should be the same as number of GPCs")
	}

	for i := 0; i < numGPCs; i++ {
		vlowModuleFinder := cache.NewXORLowModuleFinder(
			numVCachesPerGPC,
			4,
			int(math.Log2(float64(numVCachesPerGPC))),
			int(b.log2CacheLineSize)+6)

		switch mmu := chiplet.MMUs[i].(type) {
		case *caPWQL5.CaPWQMMU:
			mmu.VCacheLowModuleFinder = vlowModuleFinder
		default:
			panic("MMU is not CaPWQMMU")
		}

		conn := akita.NewDirectConnection(
			fmt.Sprintf("%s.MMU_%02d_To_L1Conn", chiplet.name, i),
			b.engine, 1*akita.GHz,
		)

		conn.PlugIn(chiplet.MMUs[i].ToCachePort(), 16)

		for j := i * numVCachesPerGPC; j < (i+1)*numVCachesPerGPC; j++ {
			vlowModuleFinder.LowModules = append(
				vlowModuleFinder.LowModules,
				chiplet.L1VCaches[j].GetWalkerPort(),
			)
			conn.PlugIn(chiplet.L1VCaches[j].GetWalkerPort(), 16)

			chiplet.L1VCaches[j].(*CaPWQCacheL6.Cache).PageWalker =
				chiplet.MMUs[i].ToCachePort()
		}
	}
}

func (b *NUMAGPUBuilder) setupInterchipNetwork() {
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

func (b *NUMAGPUBuilder) InterChipletPorts(c *Chiplet) []akita.Port {
	ports := []akita.Port{
		c.chipRdmaEngine.RequestPort,
		c.chipRdmaEngine.ResponsePort,
	}
	return ports
}

func (b *NUMAGPUBuilder) establishTLBMonitor(c *Chiplet) {
	if !b.useTLBMonitor {
		return
	}

	tlbMonitor := monitor.NewTLBMonitor(
		fmt.Sprintf("%s.TLBMonitor", b.gpuName),
		b.engine,
		1*akita.MHz,
	)

	for _, l3tlb := range c.L3TLBs {
		tlbMonitor.RegisterL3TLB(l3tlb.(monitor.MonitorComponent))
	}

	b.gpu.TLBMonitors = append(b.gpu.TLBMonitors, tlbMonitor)
}

func (b *NUMAGPUBuilder) establishCaPWQMonitor(c *Chiplet) {
	if !b.useCaPWQMonitor {
		return
	}

	caPWQMonitor := monitor.NewCaPWQMonitor(
		fmt.Sprintf("%s.CaPWQMonitor", b.gpuName),
		b.engine,
		1*akita.MHz,
	)

	for _, l1v := range c.L1VCaches {
		caPWQMonitor.RegisterL1VCache(l1v.(monitor.MonitorComponent))
	}

	for _, mmu := range c.MMUs {
		switch walker := mmu.(type) {
		case *baseline.MMUImpl:
			caPWQMonitor.RegisterPageWalker(walker)
		case *caPWQL5.CaPWQMMU:
			caPWQMonitor.RegisterPageWalker(walker)
		}
	}

	b.gpu.CaPWQMonitor = append(b.gpu.CaPWQMonitor, caPWQMonitor)

	tracing.CollectTrace(caPWQMonitor, b.caPWQTracer)
}

func (b *NUMAGPUBuilder) establishCacheTEA(c *Chiplet) {
	if !b.useCacheTEA {
		return
	}

	if len(c.L1VAddrTranslator) != len(c.L1VCaches) {
		log.Panicf("number of L1VAddrTranslator should be the same as number of L1VCaches")
	}

	for i := 0; i < len(c.L1VCaches); i++ {
		l1VCache := c.L1VCaches[i]
		l1VAT := c.L1VAddrTranslator[i]

		l1VCache.(*l1v.Cache).SetProvider(l1VAT.(*addresstranslator.DefaultAddressTranslator))
		l1VCache.(*l1v.Cache).EnableCacheTEA()
	}
}
