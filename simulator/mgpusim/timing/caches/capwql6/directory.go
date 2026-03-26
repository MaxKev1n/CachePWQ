package CaPWQCacheL6

import (
	"strconv"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/mem/cache"
	"gitlab.com/akita/mem/profile"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/tracing"
)

type directory struct {
	cache *Cache

	status profile.CachePSVStatus

	numExecutedReqs uint64
}

func (d *directory) Tick(now akita.VTimeInSec) bool {
	d.status = profile.BASE
	d.numExecutedReqs = 0
	d.collectMSHROccupancy(now)
	d.collectWalkMSHROccupancy(now)

	item := d.cache.dirBuf.Peek()
	if item == nil {
		return false
	}

	trans := item.(*transaction)

	if trans.fromWalker {
		return d.processWalkerReq(now, trans)
	}

	if trans.read != nil {
		return d.processRead(now, trans)
	}

	return d.processWrite(now, trans)
}

func (d *directory) processWalkerReq(
	now akita.VTimeInSec,
	trans *transaction,
) bool {
	if trans.write == nil {
		panic("WalkerReq called with nil transaction")
	}

	return d.processWalkerWrite(now, trans)
}

func (d *directory) processWalkerWrite(
	now akita.VTimeInSec,
	trans *transaction,
) bool {
	write := trans.write
	addr := write.Address
	pid := write.PID
	PTEBlockSize := uint64(1 << (d.cache.log2BlockSize + 3))
	PTEBlockID := addr / PTEBlockSize * PTEBlockSize

	mshrEntry := d.cache.mshr.QueryForWalker(
		pid,
		PTEBlockID,
	)
	if mshrEntry != nil {
		offset := (addr >> d.cache.log2BlockSize) & 0x7

		if mshrEntry.OffsetBits[int(offset)] {
			return d.processWalkerWriteMSHRHit(
				now,
				trans,
				mshrEntry,
			)
		}
		return d.processWalkerWritePartialMSHRHit(
			now,
			trans,
			mshrEntry,
		)
	}

	if d.cache.mshr.IsFull() {
		d.cache.notifyWalkerMSHRFull(now)
		return false
	}

	if !d.fetchPTEsFromBottom(now, trans) {
		return false
	}

	d.cache.dirBuf.Pop()

	return true
}

func (d *directory) fetchPTEsFromBottom(
	now akita.VTimeInSec,
	trans *transaction,
) bool {
	addr := trans.Address()
	pid := trans.PID()
	blockSize := uint64(1 << d.cache.log2BlockSize)
	cacheLineID := addr / blockSize * blockSize

	bottomModule := d.cache.lowModuleFinder.Find(cacheLineID)
	readReqInfo := &mem.ReadReqInfo{ReturnAccessInfo: true}
	readToBottom := mem.ReadReqBuilder{}.
		WithSendTime(now).
		WithSrc(d.cache.BottomPort).
		WithDst(bottomModule).
		WithAddress(cacheLineID).
		WithPID(pid).
		WithByteSize(blockSize).
		WithInfo(readReqInfo).
		Build()

	readToBottom.PTW = true

	err := d.cache.BottomPort.Send(readToBottom)
	if err != nil {
		return false
	}

	tracing.AddTaskStep(
		trans.id,
		now,
		d.cache,
		"ptw-read-miss",
	)

	tracing.TraceReqInitiate(readToBottom, now, d.cache, trans.id)
	trans.readToBottom = readToBottom

	PTEBlockSize := uint64(1 << (d.cache.log2BlockSize + 3))
	PTEBlockID := addr / PTEBlockSize * PTEBlockSize
	PTEOffset := (addr >> d.cache.log2BlockSize) & 0x7

	mshrEntry := d.cache.mshr.AddForWalker(pid, PTEBlockID, PTEOffset)
	mshrEntry.Requests = append(mshrEntry.Requests, trans)
	mshrEntry.ReadReq = readToBottom

	return true
}

func (d *directory) processWalkerWriteMSHRHit(
	now akita.VTimeInSec,
	trans *transaction,
	mshrEntry *cache.MSHREntry,
) bool {
	mshrEntry.Requests = append(mshrEntry.Requests, trans)

	d.cache.dirBuf.Pop()

	tracing.AddTaskStep(
		trans.id,
		now,
		d.cache,
		"ptw-read-mshr-hit",
	)

	return true
}

func (d *directory) processWalkerWritePartialMSHRHit(
	now akita.VTimeInSec,
	trans *transaction,
	mshrEntry *cache.MSHREntry,
) bool {
	addr := trans.Address()
	pid := trans.PID()
	blockSize := uint64(1 << d.cache.log2BlockSize)
	cacheLineID := addr / blockSize * blockSize

	bottomModule := d.cache.lowModuleFinder.Find(cacheLineID)
	readReqInfo := &mem.ReadReqInfo{ReturnAccessInfo: true}
	readToBottom := mem.ReadReqBuilder{}.
		WithSendTime(now).
		WithSrc(d.cache.BottomPort).
		WithDst(bottomModule).
		WithAddress(cacheLineID).
		WithPID(pid).
		WithByteSize(blockSize).
		WithInfo(readReqInfo).
		Build()

	readToBottom.PTW = true

	err := d.cache.BottomPort.Send(readToBottom)
	if err != nil {
		return false
	}

	tracing.AddTaskStep(
		trans.id,
		now,
		d.cache,
		"ptw-read-mshr-partial-hit",
	)

	tracing.TraceReqInitiate(readToBottom, now, d.cache, trans.id)
	trans.readToBottom = readToBottom

	PTEOffset := (addr >> d.cache.log2BlockSize) & 0x7

	mshrEntry.Requests = append(mshrEntry.Requests, trans)
	mshrEntry.OffsetBits[int(PTEOffset)] = true

	d.cache.dirBuf.Pop()

	return true
}

func (d *directory) collectMSHROccupancy(now akita.VTimeInSec) {
	m := d.cache.mshr
	uniqEntries := len(m.AllEntries())
	totalEntries := 0
	for _, me := range m.AllEntries() {
		totalEntries += len(me.Requests)
	}

	tracing.StartTask("", "", now, d.cache,
		"MSHRlen", strconv.Itoa(totalEntries), nil)
	tracing.StartTask("", "", now, d.cache,
		"MSHRuniq", strconv.Itoa(uniqEntries), nil)
	if uniqEntries > 0 {
		tracing.StartTask("", "", now, d.cache,
			"MSHRlen_g0", strconv.Itoa(totalEntries), nil)
		tracing.StartTask("", "", now, d.cache,
			"MSHRuniq_g0", strconv.Itoa(uniqEntries), nil)
	}
}

func (d *directory) collectWalkMSHROccupancy(now akita.VTimeInSec) {
	m := d.cache.mshr
	uniqEntries := 0
	totalEntries := 0
	for _, me := range m.AllEntries() {
		if me.PTW {
			uniqEntries++
			totalEntries += len(me.Requests)
		}
	}

	tracing.StartTask("", "", now, d.cache,
		"WalkMSHRlen", strconv.Itoa(totalEntries), nil)
	tracing.StartTask("", "", now, d.cache,
		"WalkMSHRuniq", strconv.Itoa(uniqEntries), nil)
	if uniqEntries > 0 {
		tracing.StartTask("", "", now, d.cache,
			"WalkMSHRlen_g0", strconv.Itoa(totalEntries), nil)
		tracing.StartTask("", "", now, d.cache,
			"WalkMSHRuniq_g0", strconv.Itoa(uniqEntries), nil)
	}
}

func (d *directory) processRead(now akita.VTimeInSec, trans *transaction) bool {
	read := trans.read
	addr := read.Address
	pid := read.PID
	blockSize := uint64(1 << d.cache.log2BlockSize)
	cacheLineID := addr / blockSize * blockSize

	mshrEntry := d.cache.mshr.Query(pid, cacheLineID)
	if mshrEntry != nil {
		return d.processMSHRHit(now, trans, mshrEntry)
	}

	block := d.cache.directory.Lookup(pid, cacheLineID)
	if block != nil && block.IsValid {
		return d.processReadHit(now, trans, block)
	}

	return d.processReadMiss(now, trans)
}

func (d *directory) processMSHRHit(
	now akita.VTimeInSec,
	trans *transaction,
	mshrEntry *cache.MSHREntry,
) bool {
	mshrEntry.Requests = append(mshrEntry.Requests, trans)

	d.cache.dirBuf.Pop()
	d.numExecutedReqs++

	if trans.read != nil {
		tracing.AddTaskStep(trans.id, now, d.cache, "read-mshr-hit")

		what := "l1_read_hits"
		if d.cache.isInstCache {
			what = "l1i_hits"
		}
		tracing.AddTaskStep("PowerStat", now, d.cache, what)
	} else {
		tracing.AddTaskStep(trans.id, now, d.cache, "write-mshr-hit")
		tracing.AddTaskStep("PowerStat", now, d.cache, "l1_write_hits")
	}

	return true
}

func (d *directory) processReadHit(
	now akita.VTimeInSec,
	trans *transaction,
	block *cache.Block,
) bool {
	if block.IsLocked {
		return false
	}

	bankBuf := d.getBankBuf(block)
	if !bankBuf.CanPush() {
		return false
	}

	trans.block = block
	trans.bankAction = bankActionReadHit
	block.ReadCount++
	d.cache.directory.Visit(block)
	bankBuf.Push(trans)

	d.cache.dirBuf.Pop()
	d.numExecutedReqs++
	tracing.AddTaskStep(trans.id, now, d.cache, "read-hit")

	what := "l1_read_hits"
	if d.cache.isInstCache {
		what = "l1i_hits"
	}
	tracing.AddTaskStep("PowerStat", now, d.cache, what)

	return true
}

func (d *directory) processReadMiss(
	now akita.VTimeInSec,
	trans *transaction,
) bool {
	d.status = profile.MISS

	read := trans.read
	addr := read.Address
	blockSize := uint64(1 << d.cache.log2BlockSize)
	cacheLineID := addr / blockSize * blockSize

	victim := d.cache.directory.FindVictim(cacheLineID)
	if victim.IsLocked || victim.ReadCount > 0 {
		return false
	}

	if d.cache.mshr.IsFull() {
		d.cache.notifyWalkerMSHRFull(now)
		return false
	}

	if !d.fetchFromBottom(now, trans, victim) {
		return false
	}

	d.cache.dirBuf.Pop()
	d.numExecutedReqs++
	tracing.AddTaskStep(trans.id, now, d.cache, "read-miss")

	what := "l1_read_misses"
	if d.cache.isInstCache {
		what = "l1i_misses"
	}
	tracing.AddTaskStep("PowerStat", now, d.cache, what)

	d.status = profile.BASE

	return true
}

func (d *directory) processWrite(
	now akita.VTimeInSec,
	trans *transaction,
) bool {
	write := trans.write
	addr := write.Address
	pid := write.PID
	blockSize := uint64(1 << d.cache.log2BlockSize)
	cacheLineID := addr / blockSize * blockSize

	mshrEntry := d.cache.mshr.Query(pid, cacheLineID)
	if mshrEntry != nil {
		ok := d.writeBottom(now, trans)
		if ok {
			return d.processMSHRHit(now, trans, mshrEntry)
		}
		return false
	}

	block := d.cache.directory.Lookup(pid, cacheLineID)
	if block != nil && block.IsValid {
		ok := d.processWriteHit(now, trans, block)
		if ok {
			tracing.AddTaskStep(trans.id, now, d.cache, "write-hit")
			tracing.AddTaskStep("PowerStat", now, d.cache, "l1_write_hits")
		}

		return ok
	}

	if d.isPartialWrite(write) {
		return d.partialWriteMiss(now, trans)
	}

	ok := d.fullLineWriteMiss(now, trans)
	if ok {
		tracing.AddTaskStep(trans.id, now, d.cache, "write-miss")
		tracing.AddTaskStep("PowerStat", now, d.cache, "l1_write_misses")
	}

	return ok
}

func (d *directory) isPartialWrite(write *mem.WriteReq) bool {
	if len(write.Data) < (1 << d.cache.log2BlockSize) {
		return true
	}

	if write.DirtyMask != nil {
		for _, byteDirty := range write.DirtyMask {
			if !byteDirty {
				return true
			}
		}
	}

	return false
}

func (d *directory) partialWriteMiss(
	now akita.VTimeInSec,
	trans *transaction,
) bool {
	d.status = profile.MISS

	write := trans.write
	addr := write.Address
	blockSize := uint64(1 << d.cache.log2BlockSize)
	cacheLineID := addr / blockSize * blockSize
	trans.fetchAndWrite = true

	if d.cache.mshr.IsFull() {
		d.cache.notifyWalkerMSHRFull(now)
		return false
	}

	victim := d.cache.directory.FindVictim(cacheLineID)
	if victim.ReadCount > 0 || victim.IsLocked {
		return false
	}

	sentThisCycle := false
	if trans.writeToBottom == nil {
		ok := d.writeBottom(now, trans)
		if !ok {
			return false
		}
		sentThisCycle = true
	}

	ok := d.fetchFromBottom(now, trans, victim)
	if !ok {
		if sentThisCycle {
			return true
		}
		return false
	}

	d.cache.dirBuf.Pop()
	d.numExecutedReqs++
	tracing.AddTaskStep(trans.id, now, d.cache, "write-miss")
	tracing.AddTaskStep("PowerStat", now, d.cache, "l1_write_misses")

	d.status = profile.BASE

	return true
}

func (d *directory) fullLineWriteMiss(
	now akita.VTimeInSec,
	trans *transaction,
) bool {
	d.status = profile.MISS

	write := trans.write
	addr := write.Address
	blockSize := uint64(1 << d.cache.log2BlockSize)
	cacheLineID := addr / blockSize * blockSize
	block := d.cache.directory.FindVictim(cacheLineID)

	if !d.processWriteHit(now, trans, block) {
		return false
	}

	d.status = profile.BASE

	return true
}

func (d *directory) writeBottom(now akita.VTimeInSec, trans *transaction) bool {
	write := trans.write
	addr := write.Address

	writeToBottom := mem.WriteReqBuilder{}.
		WithSendTime(now).
		WithSrc(d.cache.BottomPort).
		WithDst(d.cache.lowModuleFinder.Find(addr)).
		WithAddress(addr).
		WithPID(write.PID).
		WithData(write.Data).
		WithDirtyMask(write.DirtyMask).
		Build()

	err := d.cache.BottomPort.Send(writeToBottom)
	if err != nil {
		return false
	}

	trans.writeToBottom = writeToBottom

	tracing.TraceReqInitiate(writeToBottom, now, d.cache, trans.id)

	return true
}

func (d *directory) processWriteHit(
	now akita.VTimeInSec,
	trans *transaction,
	block *cache.Block,
) bool {
	if block.IsLocked || block.ReadCount > 0 {
		return false
	}

	bankBuf := d.getBankBuf(block)
	if !bankBuf.CanPush() {
		return false
	}

	if trans.writeToBottom == nil {
		ok := d.writeBottom(now, trans)
		if !ok {
			return false
		}
	}

	write := trans.write
	addr := write.Address
	blockSize := uint64(1 << d.cache.log2BlockSize)
	cacheLineID := addr / blockSize * blockSize
	block.IsLocked = true
	block.IsValid = true
	block.Tag = cacheLineID
	d.cache.directory.Visit(block)

	trans.bankAction = bankActionWrite
	trans.block = block
	bankBuf.Push(trans)

	d.cache.dirBuf.Pop()
	d.numExecutedReqs++

	return true
}

func (d *directory) fetchFromBottom(
	now akita.VTimeInSec,
	trans *transaction,
	victim *cache.Block,
) bool {
	addr := trans.Address()
	pid := trans.PID()
	blockSize := uint64(1 << d.cache.log2BlockSize)
	cacheLineID := addr / blockSize * blockSize

	bottomModule := d.cache.lowModuleFinder.Find(cacheLineID)
	readToBottom := mem.ReadReqBuilder{}.
		WithSendTime(now).
		WithSrc(d.cache.BottomPort).
		WithDst(bottomModule).
		WithAddress(cacheLineID).
		WithPID(pid).
		WithByteSize(blockSize).
		Build()
	err := d.cache.BottomPort.Send(readToBottom)
	if err != nil {
		return false
	}

	tracing.TraceReqInitiate(readToBottom, now, d.cache, trans.id)
	trans.readToBottom = readToBottom
	trans.block = victim

	mshrEntry := d.cache.mshr.Add(pid, cacheLineID)
	mshrEntry.Requests = append(mshrEntry.Requests, trans)
	mshrEntry.ReadReq = readToBottom
	mshrEntry.Block = victim

	victim.Tag = cacheLineID
	victim.PID = pid
	victim.IsValid = true
	victim.IsLocked = true
	d.cache.directory.Visit(victim)

	return true
}

func (d *directory) getBankBuf(block *cache.Block) util.Buffer {
	numWaysPerSet := d.cache.directory.WayAssociativity()
	blockID := block.SetID*numWaysPerSet + block.WayID
	bankID := blockID % len(d.cache.bankBufs)
	return d.cache.bankBufs[bankID]
}
