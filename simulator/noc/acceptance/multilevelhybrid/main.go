package main

import (
	"flag"
	"fmt"
	"math/rand"

	"github.com/tebeka/atexit"
	"gitlab.com/akita/akita"
	"gitlab.com/akita/noc/acceptance"
	noc "gitlab.com/akita/noc/networking/booksim"
	"gitlab.com/akita/noc/networking/multiplexer"
)

func main() {
	flag.Parse()
	rand.Seed(1)

	engine := akita.NewSerialEngine()
	t := acceptance.NewTest()

	createNetwork(engine, t)
	t.GenerateMsgs(4096)

	engine.Run()

	t.MustHaveReceivedAllMsgs()
	t.ReportBandwidthAchieved(engine.CurrentTime())
	atexit.Exit(0)
}

func createNetwork(engine akita.Engine, test *acceptance.Test) {
	freq := 1.0 * akita.GHz
	var agents []*acceptance.Agent
	for i := 0; i < 5; i++ {
		agent := acceptance.NewAgent(
			engine, freq, fmt.Sprintf("Agent%d", i), 1, test)
		agent.TickLater(0)
		agents = append(agents, agent)
	}

	booksim := noc.NewHybridBookSimNoC("MultiLevelHybridBookSim-TestNet", engine)

	booksim.MaxNumSMSidePort = 1
	booksim.MaxNumMemSidePort = 1

	booksim.CreateNetwork("/Users/chenzihang/codes/CachePWQ/simulator/noc/networking/booksim/native/config_hierarchicalmemside.icnt")

	routingTableA := multiplexer.NewMapRoutingTable()
	routingTableB := multiplexer.NewMapRoutingTable()
	routingTableC := multiplexer.NewMapRoutingTable()

	builder := multiplexer.MakeMultiplexerBuilder().
		WithEngine(engine).
		WithFreq(freq).
		WithNumReqPerCycle(1).
		WithSwitchLatency(2).
		WithRoutingTable(routingTableA).
		WithBufferSizeInNumFlit(16)

	connectorA := builder.Build("MultiplexerA")

	builder = multiplexer.MakeMultiplexerBuilder().
		WithEngine(engine).
		WithFreq(freq).
		WithNumReqPerCycle(1).
		WithSwitchLatency(2).
		WithRoutingTable(routingTableB).
		WithBufferSizeInNumFlit(16)

	connectorB := builder.Build("MultiplexerB")

	builder = multiplexer.MakeMultiplexerBuilder().
		WithEngine(engine).
		WithFreq(freq).
		WithNumReqPerCycle(4).
		WithSwitchLatency(15).
		WithRoutingTable(routingTableC).
		WithBufferSizeInNumFlit(64)

	connectorC := builder.Build("MultiplexerC")

	for i := 0; i < 2; i++ {
		ep := multiplexer.MakeEndPointBuilder().
			WithEngine(engine).
			WithFreq(freq).
			WithDevicePorts(agents[i].Ports).
			WithFlitByteSize(32).
			Build(fmt.Sprintf("EndPoint%d", i))

		local := connectorA.AddLowSidePort(ep)

		for _, port := range ep.DevicePorts {
			connectorA.AddRoute(port, local)
		}
	}

	for i := 2; i < 4; i++ {
		ep := multiplexer.MakeEndPointBuilder().
			WithEngine(engine).
			WithFreq(freq).
			WithDevicePorts(agents[i].Ports).
			WithFlitByteSize(32).
			Build(fmt.Sprintf("EndPoint%d", i))

		local := connectorB.AddLowSidePort(ep)

		for _, port := range ep.DevicePorts {
			connectorB.AddRoute(port, local)
		}
	}

	// Connect multiplexers
	multiplexer.ConnectMultiplexers(
		engine,
		connectorA,
		connectorC,
		freq,
	)

	multiplexer.ConnectMultiplexers(
		engine,
		connectorB,
		connectorC,
		freq,
	)

	ep := multiplexer.MakeHybridEndPointBuilder().
		WithEngine(engine).
		WithFreq(freq).
		WithFlitByteSize(32).
		WithFlitAssemblingBufferSize(128).
		WithNetworkPortBufferSize(64).
		Build("HybridEndPoint")

	connectorC.SetHighSideHybridEndPoint(ep)

	nocPort := booksim.PlugInSMSide(ep.NetworkPort, 64)
	for _, port := range connectorC.RoutingTable.GetAllSrcPorts() {
		booksim.AddRoute(port, ep.NetworkPort)
	}
	ep.PlugIn(nocPort, 64)

	booksim.PlugInMemSide(agents[4].Ports[0], 64)

	test.RegisterAgent(agents[0])
	test.RegisterAgent(agents[1])
	test.RegisterAgent(agents[2])
	test.RegisterAgent(agents[3])
	test.RegisterAgent(agents[4])

}
