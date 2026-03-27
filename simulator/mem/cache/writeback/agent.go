package writeback

import (
	"log"

	"github.com/tebeka/atexit"
	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
)

var AgentImpls []*Agent
var StartTime akita.VTimeInSec
var total uint64

type Agent struct {
	*akita.TickingComponent
	MsgsToSend []akita.Msg

	ActiveReqs     map[string]akita.Msg
	MaxOutstanding int
	numReqs        uint64
	count          uint64
}

func NewAgent(
	engine akita.Engine,
	freq akita.Freq,
	srcPorts []akita.Port,
	dstPort akita.Port,
	addr uint64,
	numReqs uint64,
) *Agent {
	a := &Agent{}

	a.TickingComponent = akita.NewTickingComponent(
		"WritebackAgent",
		engine,
		freq,
		a,
	)
	a.numReqs = numReqs
	a.MaxOutstanding = 4096 // 修改 2: 设置最大在途请求数
	a.ActiveReqs = make(map[string]akita.Msg)

	for i := uint64(0); i < a.numReqs; i++ {
		srcPortID := i / 2

		readReqInfo := &mem.ReadReqInfo{ReturnAccessInfo: true}

		msg := mem.ReadReqBuilder{}.
			WithSrc(srcPorts[srcPortID]).
			WithDst(dstPort).
			WithByteSize(64).
			WithAddress(addr).
			WithInfo(readReqInfo).
			Build()
		a.MsgsToSend = append(a.MsgsToSend, msg)
	}

	total += a.numReqs
	AgentImpls = append(AgentImpls, a)

	return a
}

func (a *Agent) Tick(now akita.VTimeInSec) bool {
	madeProgress := false

	numReqs := 0

	// 修改 3: 只要没达到在途上限，就持续注入
	// 模拟 GPU SM 持续发出请求直到管线填满
	for len(a.ActiveReqs) < a.MaxOutstanding && len(a.MsgsToSend) > 0 {
		if a.send(now) {
			madeProgress = true
		} else {
			break // 网络出口已满
		}

		numReqs++

		if numReqs >= 1 {
			break // 每个周期最多发出 16 个请求
		}
	}

	return madeProgress
}

func (a *Agent) send(now akita.VTimeInSec) bool {
	msg := a.MsgsToSend[0]
	msg.Meta().SendTime = now
	err := msg.Meta().Src.Send(msg)
	if err == nil {
		a.MsgsToSend = a.MsgsToSend[1:]
		// 发送成功后再加入 ActiveReqs
		a.ActiveReqs[msg.Meta().ID] = msg
		if StartTime == 0 {
			StartTime = now
		}
		return true
	}

	return false
}

func Recv(msg akita.Msg, now akita.VTimeInSec) {
	rsp := msg.(*mem.DataReadyRsp)

	for i, a := range AgentImpls {
		if req, ok := a.ActiveReqs[rsp.GetRespondTo()]; ok {
			delete(a.ActiveReqs, rsp.GetRespondTo())
			a.count++

			log.Printf("%.12f, %v, %s, %s\n",
				now-req.Meta().SendTime, rsp.Info.(*mem.DataReadyRspInfo).AccessResult, req.Meta().Src.Name(), req.Meta().Dst.Name())
		}

		if a.count == a.numReqs {
			AgentImpls = append(AgentImpls[:i], AgentImpls[i+1:]...)
		}
	}

	if len(AgentImpls) == 0 {
		elapsed := now - StartTime
		log.Printf("All writeback requests are done.\n")
		log.Printf("Elapsed time: %.12f seconds\n", float64(elapsed))
		log.Printf("Bandwidth: %f GB/s\n",
			float64(total)*64/1e9/float64(elapsed))

		atexit.Exit(0)
	}
}
