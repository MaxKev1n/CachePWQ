package CaPWQCacheL3

import (
	"log"
	"reflect"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/util/tracing"
)

type walkerStage struct {
	cache *Cache
}

func (c *walkerStage) Reset() {}

func (c *walkerStage) Tick(now akita.VTimeInSec) bool {
	item := c.cache.WalkerPort.Peek()
	if item == nil {
		return false
	}

	return c.processReqFromWalker(now, item.(mem.AccessReq))
}

func (c *walkerStage) processReqFromWalker(
	now akita.VTimeInSec,
	req mem.AccessReq,
) bool {
	if !c.cache.dirBuf.CanPush() {
		return false
	}

	trans := c.createTransaction(req)
	c.cache.transactions = append(c.cache.transactions, trans)

	c.cache.dirBuf.Push(trans)

	c.cache.WalkerPort.Retrieve(now)

	tracing.TraceReqReceive(req, now, c.cache)

	return true
}

func (c *walkerStage) createTransaction(req mem.AccessReq) *transaction {
	switch req := req.(type) {
	case *mem.ReadReq:
		t := &transaction{
			read:       req,
			fromWalker: true,
		}
		return t
	case *mem.WriteReq:
		t := &transaction{
			write:      req,
			fromWalker: true,
		}
		return t
	default:
		log.Panicf("cannot process request of type %s\n", reflect.TypeOf(req))
		return nil
	}
}
