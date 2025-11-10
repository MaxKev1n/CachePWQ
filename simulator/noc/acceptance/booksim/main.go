// booksim_noc_acceptance.go
package main

import (
	"flag"
	"fmt"
	"math/rand"

	"github.com/tebeka/atexit"
	"gitlab.com/akita/akita"
	"gitlab.com/akita/noc/acceptance"
	noc "gitlab.com/akita/noc/networking/booksim"
)

func main() {
	flag.Parse()
	rand.Seed(1)

	engine := akita.NewSerialEngine()
	t := acceptance.NewTest()

	createNetwork(engine, t)

	engine.Run()

	t.MustHaveReceivedAllMsgs()
	t.ReportBandwidthAchieved(engine.CurrentTime())
	t.ReportAverageLatency()
	atexit.Exit(0)
}

// -----------------------------------------------------------------------------
// 创建网络拓扑
// -----------------------------------------------------------------------------
func createNetwork(engine akita.Engine, test *acceptance.Test) {
	freq := 1 * akita.GHz
	numAgents := 193
	var agents []*acceptance.Agent

	// 1️⃣ 创建 agent
	for i := 0; i < numAgents; i++ {
		agent := acceptance.NewAgent(
			engine, freq, fmt.Sprintf("Agent%d", i), 1, test)
		agent.TickLater(0)
		agents = append(agents, agent)
	}

	// 2️⃣ 创建 BookSimNoC
	booksim := noc.NewBookSimNoC("BookSim-TestNet", engine)

	booksim.MaxNumSMSidePort = 192
	booksim.MaxNumMemSidePort = 1

	booksim.CreateNetwork("/Users/chenzihang/codes/CachePWQ/simulator/noc/networking/booksim/native/config_monolithic_4GB_tlb.icnt")

	// 3️⃣ 接入 agent 端口
	for i := 0; i < 192; i++ {
		booksim.PlugInSMSide(agents[i].Ports[0], 32)
	}
	for i := 192; i < numAgents; i++ {
		booksim.PlugInMemSide(agents[i].Ports[0], 32)
	}

	// 4️⃣ 注册 agent 到 test 框架
	for i := 0; i < numAgents; i++ {
		test.RegisterAgent(agents[i])
	}

	test.GenerateMsgs(4096)

	//for i := 0; i < numAgents; i++ {
	//	for _, msg := range agents[i].MsgsToSend {
	//		msg.Meta().Dst = booksim.NocPorts()[i]
	//	}
	//}
}
