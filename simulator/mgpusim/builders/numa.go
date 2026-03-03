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
	"gitlab.com/akita/mem/vm/mmu/caPWQ"
	"gitlab.com/akita/mem/vm/mmu/mpw"
	"gitlab.com/akita/mem/vm/tlb"
	"gitlab.com/akita/mgpusim"
	"gitlab.com/akita/mgpusim/timing/caches/l1cache"
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
	useTLBMonitor bool
	useCacheTEA   bool
}

func (b *NUMAGPUBuilder) WithTLBMonitor() {
	b.useTLBMonitor = true
}

func (b *NUMAGPUBuilder) WithCacheTEA() {
	b.useCacheTEA = true
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

	for i, l1vtlb := range chiplet.L1VTLBs {
		ep := multiplexer.MakeEndPointBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithDevicePorts([]akita.Port{l1vtlb.GetBottomPort()}).
			WithNumReqPerCycle(1).
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
				mmu.TranslationPortPort(),
				mmu.ToPageWalkCachePort(),
				mmu.ToTopPort(),
			}).
			WithFlitByteSize(32).
			WithNumReqPerCycle(4).
			WithNetworkPortBufferSize(4).
			Build(fmt.Sprintf("%s.MMU[%d]", chiplet.name, i))

		mux := chiplet.gpcMux[i]

		localPort := mux.AddLowSidePort(ep)
		mux.AddRoute(mmu.TranslationPortPort(), localPort)
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

		chiplet.GlobalNoC.AddRoute(chiplet.L2TLBs[i].GetTopPort(), ep.NetworkPort)
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
			WithNumMSHREntry(64).
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

	builder := tlb.MakeLastLevelTLBBuilder().
		WithEngine(b.engine).
		WithFreq(b.freq).
		WithNumWays(numWays).
		WithNumSets(numSets).
		WithNumMSHREntry(512).
		WithNumReqPerCycle(8).
		WithLog2PageSize(b.log2PageSize).
		WithPageWalkCacheSize(2048).
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
		case "MPWMMU":
			b.buildMPWMMU(chiplet)
		case "CaPWQMMU":
			b.buildCaPWQMMU(chiplet)
		case "AsyncCaPWQMMU":
			b.buildAsyncCaPWQMMU(chiplet)
		default:
			log.Panicf("Unsupported MMU type: %s\n", mmuType)
		}
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
			Build(fmt.Sprintf("%s.BaselineMMU_%02d", chiplet.name, i))

		pageWalkCachePort := chiplet.L3TLBs[0].(*tlb.LastLevelTLB).PWCWritePort

		component.(*baseline.MMUImpl).PageWalkCache = pageWalkCachePort

		chiplet.MMUs = append(chiplet.MMUs, component)
		b.gpu.MMUs = append(b.gpu.MMUs, component)

		chiplet.L3TLBs[0].(*tlb.LastLevelTLB).MMUs = append(
			chiplet.L3TLBs[0].(*tlb.LastLevelTLB).MMUs, component,
		)
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
			Build(fmt.Sprintf("%s.MPWMMU_%02d", chiplet.name, i))

		pageWalkCachePort := chiplet.L3TLBs[0].(*tlb.LastLevelTLB).PWCWritePort

		component.(*mpw.MPWMMU).PageWalkCache = pageWalkCachePort

		chiplet.MMUs = append(chiplet.MMUs, component)
		b.gpu.MMUs = append(b.gpu.MMUs, component)

		chiplet.L3TLBs[0].(*tlb.LastLevelTLB).MMUs = append(
			chiplet.L3TLBs[0].(*tlb.LastLevelTLB).MMUs, component,
		)
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
			Build(fmt.Sprintf("%s.IdealMMU_%02d", chiplet.name, i))

		chiplet.MMUs = append(chiplet.MMUs, component)
		b.gpu.MMUs = append(b.gpu.MMUs, component)

		chiplet.L3TLBs[0].(*tlb.LastLevelTLB).MMUs = append(
			chiplet.L3TLBs[0].(*tlb.LastLevelTLB).MMUs, component,
		)
	}
}

func (b *NUMAGPUBuilder) buildCaPWQMMU(chiplet *Chiplet) {
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
		component := caPWQ.MakeCaPWQMMUBuilder().
			WithEngine(b.engine).
			WithFreq(1 * akita.GHz).
			WithLog2PageSize(b.log2PageSize).
			WithPageTable(b.pageTable).
			WithMaxNumReqInFlight(maxNumReqInFlight / numGPCs).
			Build(fmt.Sprintf("%s.CaPWQMMU_%02d", chiplet.name, i))

		pageWalkCachePort := chiplet.L3TLBs[0].(*tlb.LastLevelTLB).PWCWritePort

		component.(*caPWQ.CaPWQMMU).PageWalkCache = pageWalkCachePort
		component.(*caPWQ.CaPWQMMU).L3TLB = chiplet.L3TLBs[0].GetBottomPort()

		chiplet.MMUs = append(chiplet.MMUs, component)
		b.gpu.MMUs = append(b.gpu.MMUs, component)

		chiplet.L3TLBs[0].(*tlb.LastLevelTLB).MMUs = append(
			chiplet.L3TLBs[0].(*tlb.LastLevelTLB).MMUs, component,
		)
	}

	b.establishMMUToL1RoutingPath(chiplet)
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
			Build(fmt.Sprintf("%s.AsyncCaPWQMMU_%02d", chiplet.name, i))

		pageWalkCachePort := chiplet.L3TLBs[0].(*tlb.LastLevelTLB).PWCWritePort

		component.(*asyncCaPWQ.AsyncCaPWQMMU).PageWalkCache = pageWalkCachePort
		component.(*asyncCaPWQ.AsyncCaPWQMMU).L3TLB = chiplet.L3TLBs[0].GetBottomPort()

		chiplet.MMUs = append(chiplet.MMUs, component)
		b.gpu.MMUs = append(b.gpu.MMUs, component)

		chiplet.L3TLBs[0].(*tlb.LastLevelTLB).MMUs = append(
			chiplet.L3TLBs[0].(*tlb.LastLevelTLB).MMUs, component,
		)
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
	numCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/numCUsPerGPC + 1

	if len(chiplet.MMUs) != numGPCs {
		panic("number of MMUs should be the same as number of GPCs")
	}

	for i := 0; i < numGPCs; i++ {
		lowModuleFinder := cache.NewXORLowModuleFinder(
			len(chiplet.L1VCaches)/numCUsPerGPC,
			4,
			int(math.Log2(float64(len(chiplet.L1VCaches)/numCUsPerGPC))),
			int(b.log2PageSize))

		switch mmu := chiplet.MMUs[i].(type) {
		case *caPWQ.CaPWQMMU:
			mmu.CacheLowModuleFinder = lowModuleFinder
		case *asyncCaPWQ.AsyncCaPWQMMU:
			mmu.CacheLowModuleFinder = lowModuleFinder
		default:
			panic("MMU is not CaPWQMMU")
		}

		mmuSwitch := multiplexer.SwitchBuilder{}.
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithArbiter(multiplexer.NewRRArbiter()).
			WithRoutingTable(multiplexer.NewMapRoutingTable()).
			WithNumReqPerCycle(32).
			WithBufferSizeInNumFlit(32).
			Build(fmt.Sprintf("%s.MMUSwitch", chiplet.name))

		for j := i * numCUsPerGPC; j < (i+1)*numCUsPerGPC; j++ {
			idealCache := l1cache.NewIdealCaPWQCache(
				fmt.Sprintf("%s.L1IdealCaPWQCache_%02d", chiplet.name, j),
				b.engine,
				b.freq,
				2,
				28,
			)

			ep := multiplexer.MakeEndPointBuilder().
				WithEngine(b.engine).
				WithFreq(b.freq).
				WithDevicePorts([]akita.Port{idealCache.GetMMUSidePort()}).
				WithNumReqPerCycle(1).
				WithFlitByteSize(64).
				Build(fmt.Sprintf("%s.L1IdealCaPWQCache_%02d", chiplet.name, j))

			switchPort := mmuSwitch.ConnectEndPointToSwitch(ep, 5, b.freq)
			rt := mmuSwitch.GetRoutingTable()
			rt.AddRoute(idealCache.GetMMUSidePort(), switchPort)

			lowModuleFinder.LowModules = append(lowModuleFinder.LowModules,
				idealCache.GetMMUSidePort())

			b.gpu.L1CaPWQCache = append(b.gpu.L1CaPWQCache, idealCache)
		}

		ep := multiplexer.MakeEndPointBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithDevicePorts([]akita.Port{chiplet.MMUs[i].ToCachePort()}).
			WithFlitByteSize(64).
			WithNumReqPerCycle(8).
			WithNetworkPortBufferSize(8).
			Build(fmt.Sprintf("%s.MMU_%02d", chiplet.name, i))

		switchPort := mmuSwitch.ConnectEndPointToSwitch(ep, 5, b.freq)
		rt := mmuSwitch.GetRoutingTable()
		rt.AddRoute(chiplet.MMUs[i].ToCachePort(), switchPort)
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
		tlbMonitor.RegisterL3TLB(l3tlb.(monitor.TLBMonitorComponent))
	}

	b.gpu.TLBMonitors = append(b.gpu.TLBMonitors, tlbMonitor)
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
