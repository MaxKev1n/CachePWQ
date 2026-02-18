package tlb

import (
	"gitlab.com/akita/akita"
	"gitlab.com/akita/util/tracing"
)

type TLBMonitor struct {
	*akita.TickingComponent

	L2TLBs []*LatTLB

	running     bool
	initialized bool

	numEpoches uint64
}

func NewTLBMonitor(
	name string,
	engine akita.Engine,
	freq akita.Freq,
) *TLBMonitor {
	c := &TLBMonitor{}

	c.TickingComponent = akita.NewTickingComponent(
		name, engine, freq, c)

	return c
}

func (m *TLBMonitor) Tick(now akita.VTimeInSec) bool {
	if !m.running {
		return false
	}

	for _, tlb := range m.L2TLBs {
		m.CollectL2TLBStats(now, tlb)
	}

	m.numEpoches++

	return true
}

func (m *TLBMonitor) RegisterL2TLB(tlb *LatTLB) {
	m.L2TLBs = append(m.L2TLBs, tlb)
}

func (m *TLBMonitor) Start(now akita.VTimeInSec) {
	if !m.initialized {
		for _, tlb := range m.L2TLBs {
			tlb.monitorStats.Clear()
		}

		m.initialized = true
	}

	m.running = true

	m.TickLater(now)
}

func (m *TLBMonitor) Stop() {
	for _, tlb := range m.L2TLBs {
		tlb.monitorStats.Clear()
	}

	m.running = false
}

func (m *TLBMonitor) CollectL2TLBStats(
	now akita.VTimeInSec,
	tlb *LatTLB,
) {
	if m.numEpoches == 0 {
		tlb.monitorStats.Clear()

		return
	}

	tracing.StartTask(
		"",
		"",
		now,
		m,
		"TLBMonitor",
		"",
		tlb.monitorStats,
	)

	tlb.monitorStats.Clear()
}

type MonitorStats struct {
	name string

	Hits     uint64
	MSHRHits uint64
	Misses   uint64
}

func (stat *MonitorStats) Clear() {
	stat.Hits = 0
	stat.MSHRHits = 0
	stat.Misses = 0
}
