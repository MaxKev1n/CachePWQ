package main

import (
	"flag"
	"fmt"
	"math/rand"

	"github.com/tebeka/atexit"
	"gitlab.com/akita/akita"
	"gitlab.com/akita/noc/acceptance"
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
	for i := 0; i < 3; i++ {
		agent := acceptance.NewAgent(
			engine, freq, fmt.Sprintf("Agent%d", i), 1, test)
		agent.TickLater(0)
		agents = append(agents, agent)
	}

	builder := multiplexer.MakeMultiplexerBuilder().
		WithEngine(engine).
		WithFreq(freq).
		WithNumReqPerCycle(1).
		WithSwitchLatency(2).
		WithBufferSizeInNumFlit(16)

	connector := builder.Build("Multiplexer")

	for i := 0; i < 2; i++ {
		ep := multiplexer.MakeEndPointBuilder().
			WithEngine(engine).
			WithFreq(freq).
			WithDevicePort(agents[i].Ports[0]).
			WithFlitByteSize(32).
			Build(fmt.Sprintf("EndPoint%d", i))

		connector.AddLowSidePort(ep)
	}

	ep := multiplexer.MakeEndPointBuilder().
		WithEngine(engine).
		WithFreq(freq).
		WithDevicePort(agents[2].Ports[0]).
		WithFlitByteSize(32).
		Build(fmt.Sprintf("EndPoint%d", 2))

	connector.SetHighSidePort(ep)

	test.RegisterAgent(agents[0])
	test.RegisterAgent(agents[1])
	test.RegisterAgent(agents[2])

}
