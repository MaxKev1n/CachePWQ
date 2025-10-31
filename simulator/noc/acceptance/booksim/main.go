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
	numAgents := 418
	var agents []*acceptance.Agent

	// 1️⃣ 创建 agent
	for i := 0; i < numAgents; i++ {
		agent := acceptance.NewAgent(
			engine, freq, fmt.Sprintf("Agent%d", i), 1, test)
		agent.TickLater(0)
		agents = append(agents, agent)
	}

	// 2️⃣ 创建 BookSimNoC
	cfgPath := "" // 如果没有配置文件，可传 "" 使用默认 mesh
	booksim := noc.NewBookSimNoC("BookSim-TestNet", cfgPath, engine, numAgents)

	booksim.MaxNumSMSidePort = 385
	booksim.MaxNumMemSidePort = 33
	// 3️⃣ 接入 agent 端口
	for i := 0; i < 385; i++ {
		booksim.PlugInSMSide(agents[i].Ports[0], 32)
	}
	for i := 385; i < numAgents; i++ {
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
