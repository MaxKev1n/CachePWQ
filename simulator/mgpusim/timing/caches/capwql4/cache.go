package CaPWQCacheL4

import (
	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/mem/cache"
	"gitlab.com/akita/mem/profile"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/tracing"
)

// A Cache is a customized L1 cache the for R9nano GPUs.
type Cache struct {
	*akita.TickingComponent

	TopPort     akita.Port
	BottomPort  akita.Port
	ControlPort akita.Port
	WalkerPort  akita.Port
	PageWalker  akita.Port

	numReqPerCycle   int
	log2BlockSize    uint64
	storage          *mem.Storage
	directory        cache.Directory
	mshr             cache.MSHR
	bankLatency      int
	wayAssociativity int
	lowModuleFinder  cache.LowModuleFinder

	dirBuf   util.Buffer
	bankBufs []util.Buffer

	coalesceStage    *coalescer
	walkerStage      *walkerStage
	directoryStage   *directory
	bankStages       []*bankStage
	parseBottomStage *bottomParser
	respondStage     *respondStage
	controlStage     *controlStage

	transactions             []*transaction
	postCoalesceTransactions []*transaction

	isPaused bool

	enableAttribute bool
	provider        profile.CachePSVComponent

	directoryStatus []profile.CachePSVStatus

	isInstCache bool

	mshrFull bool
}

// SetLowModuleFinder sets the finder that tells which remote port can serve
// the data on a certain address.
func (c *Cache) SetLowModuleFinder(lmf cache.LowModuleFinder) {
	c.lowModuleFinder = lmf
}

// Tick update the state of the cache
func (c *Cache) Tick(now akita.VTimeInSec) bool {
	madeProgress := false

	if !c.isPaused {
		madeProgress = c.runPipeline(now) || madeProgress
	}

	madeProgress = c.controlStage.Tick(now) || madeProgress

	if c.enableAttribute {
		c.Attribute(now)

		return true
	}

	return madeProgress
}

func (c *Cache) EnableCacheTEA() {
	c.enableAttribute = true
}

func (c *Cache) runPipeline(now akita.VTimeInSec) bool {
	madeProgress := false
	madeProgress = c.tickRespondStage(now) || madeProgress
	madeProgress = c.tickParseBottomStage(now) || madeProgress
	madeProgress = c.tickBankStage(now) || madeProgress
	madeProgress = c.tickDirectoryStage(now) || madeProgress
	madeProgress = c.tickCoalesceState(now) || madeProgress
	madeProgress = c.tickWalkerStage(now) || madeProgress
	return madeProgress
}

func (c *Cache) tickRespondStage(now akita.VTimeInSec) bool {
	madeProgress := false
	for i := 0; i < c.numReqPerCycle; i++ {
		madeProgress = c.respondStage.Tick(now) || madeProgress
	}
	return madeProgress
}

func (c *Cache) tickParseBottomStage(now akita.VTimeInSec) bool {
	madeProgress := false

	for i := 0; i < c.numReqPerCycle; i++ {
		madeProgress = c.parseBottomStage.Tick(now) || madeProgress
	}

	return madeProgress
}

func (c *Cache) tickBankStage(now akita.VTimeInSec) bool {
	madeProgress := false
	for _, bs := range c.bankStages {
		madeProgress = bs.Tick(now) || madeProgress
	}
	return madeProgress
}

func (c *Cache) tickDirectoryStage(now akita.VTimeInSec) bool {
	madeProgress := false
	for i := 0; i < c.numReqPerCycle; i++ {
		madeProgress = c.directoryStage.Tick(now) || madeProgress

		c.directoryStatus[i] = c.directoryStage.status
	}
	return madeProgress
}

func (c *Cache) tickWalkerStage(now akita.VTimeInSec) bool {
	madeProgress := false
	for i := 0; i < c.numReqPerCycle; i++ {
		madeProgress = c.walkerStage.Tick(now) || madeProgress
	}
	return madeProgress
}

func (c *Cache) tickCoalesceState(now akita.VTimeInSec) bool {
	madeProgress := false
	for i := 0; i < c.numReqPerCycle; i++ {
		madeProgress = c.coalesceStage.Tick(now) || madeProgress
	}
	return madeProgress
}

func (c *Cache) notifyWalkerMSHRNotFull(
	now akita.VTimeInSec,
) bool {
	if !c.mshrFull {
		return true
	}

	msg := mem.ControlMsgBuilder{}.
		WithSendTime(now).
		WithSrc(c.WalkerPort).
		WithDst(c.PageWalker).
		Build()
	err := c.WalkerPort.Send(msg)
	if err != nil {
		return false
	}

	c.mshrFull = false

	return true
}

func (c *Cache) GetTopPort() akita.Port {
	return c.TopPort
}

func (c *Cache) GetBottomPort() akita.Port {
	return c.BottomPort
}

func (c *Cache) GetControlPort() akita.Port {
	return c.ControlPort
}

func (c *Cache) GetName() string {
	return c.Name()
}

func (c *Cache) CheckTopPort(port akita.Port) bool {
	return port == c.TopPort
}

func (c *Cache) CheckBottomPort(port akita.Port) bool {
	return port == c.BottomPort
}

func (c *Cache) Attribute(now akita.VTimeInSec) {
	if c.directoryStage.numExecutedReqs == uint64(c.numReqPerCycle) {
		tracing.StartTask(
			"",
			"",
			now,
			c,
			"cache_utilization",
			"base",
			1.0,
		)

		return
	}

	if c.directoryStage.numExecutedReqs > 0 {
		tracing.StartTask(
			"",
			"",
			now,
			c,
			"cache_utilization",
			"base",
			float64(c.directoryStage.numExecutedReqs)/float64(c.numReqPerCycle),
		)
	}

	remainingReqs := int(c.numReqPerCycle) - int(c.directoryStage.numExecutedReqs)
	for i := remainingReqs - 1; i >= 0; i-- {
		status := profile.BASE

		if len(c.coalesceStage.toCoalesce) == 0 && c.TopPort.Peek() == nil {
			status = c.provider.Attribute()
		} else if !c.dirBuf.CanPush() {
			status = c.directoryStatus[i]
		}

		tracing.StartTask(
			"",
			"",
			now,
			c,
			"cache_utilization",
			profile.CachePSVStatusNames[status],
			1.0/float64(c.numReqPerCycle),
		)
	}
}

func (c *Cache) SetProvider(provider profile.CachePSVComponent) {
	c.provider = provider
}

func (c *Cache) GetWalkerPort() akita.Port {
	return c.WalkerPort
}
