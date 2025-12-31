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
	t.GenerateMsgs(1024)

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

	booksim := noc.NewHybridBookSimNoC("HybridBookSim-TestNet", engine)

	booksim.MaxNumSMSidePort = 2
	booksim.MaxNumMemSidePort = 1

	booksim.CreateNetwork("/Users/chenzihang/codes/CachePWQ/simulator/noc/networking/booksim/native/config_hierarchicalmemside.icnt")

	routingTableA := multiplexer.NewMapRoutingTable()
	routingTableB := multiplexer.NewMapRoutingTable()

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

	epA := multiplexer.MakeHybridEndPointBuilder().
		WithEngine(engine).
		WithFreq(freq).
		WithFlitByteSize(32).
		WithFlitAssemblingBufferSize(128).
		WithNetworkPortBufferSize(64).
		Build("ToNoCFromA")

	epB := multiplexer.MakeHybridEndPointBuilder().
		WithEngine(engine).
		WithFreq(freq).
		WithFlitByteSize(32).
		WithFlitAssemblingBufferSize(128).
		WithNetworkPortBufferSize(64).
		Build("ToNoCFromB")

	connectorA.SetHighSideHybridEndPoint(epA)
	connectorB.SetHighSideHybridEndPoint(epB)

	nocAPort := booksim.PlugInSMSide(epA.NetworkPort, 32)
	for _, port := range connectorA.RoutingTable.GetAllSrcPorts() {
		booksim.AddRoute(port, epA.NetworkPort)
	}
	epA.PlugIn(nocAPort, 32)

	nocBPort := booksim.PlugInSMSide(epB.NetworkPort, 32)
	for _, port := range connectorB.RoutingTable.GetAllSrcPorts() {
		booksim.AddRoute(port, epB.NetworkPort)
	}
	epB.PlugIn(nocBPort, 32)

	booksim.PlugInMemSide(agents[4].Ports[0], 64)

	test.RegisterAgent(agents[0])
	test.RegisterAgent(agents[1])
	test.RegisterAgent(agents[2])
	test.RegisterAgent(agents[3])
	test.RegisterAgent(agents[4])

}
