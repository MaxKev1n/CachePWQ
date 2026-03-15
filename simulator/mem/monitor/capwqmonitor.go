package monitor

import (
	"gitlab.com/akita/akita"
	"gitlab.com/akita/util/tracing"
)

type CaPWQMonitor struct {
	*akita.TickingComponent

	L1VCaches []MonitorComponent
	Walkers   []MonitorComponent

	running     bool
	initialized bool

	numEpoches        uint64
	numL1VCacheLength uint64
	numWalkerLength   uint64
}

func NewCaPWQMonitor(
	name string,
	engine akita.Engine,
	freq akita.Freq,
) *CaPWQMonitor {
	c := &CaPWQMonitor{}

	c.TickingComponent = akita.NewTickingComponent(
		name, engine, freq, c)

	return c
}

func (m *CaPWQMonitor) Tick(now akita.VTimeInSec) bool {
	if !m.running {
		return false
	}

	for _, l1v := range m.L1VCaches {
		m.CollectL1VCacheComponentStats(l1v)
	}

	for _, walker := range m.Walkers {
		m.CollectWalkerComponentStats(walker)
	}

	l1VCacheUtilization := float64(m.numL1VCacheLength) / float64(len(m.L1VCaches))
	walkerUtilization := float64(m.numWalkerLength) / float64(len(m.Walkers))

	tracing.StartTask(
		"",
		"",
		now,
		m,
		"CaPWQMonitor",
		"",
		StatItem{
			NumEpoches:          m.numEpoches,
			L1VCacheUtilization: l1VCacheUtilization,
			WalkerUtilization:   walkerUtilization,
		},
	)

	m.numEpoches++
	m.numL1VCacheLength = 0
	m.numWalkerLength = 0

	return true
}

func (m *CaPWQMonitor) RegisterL1VCache(l1v MonitorComponent) {
	l1v.InitMonitorStats()
	m.L1VCaches = append(m.L1VCaches, l1v)
}

func (m *CaPWQMonitor) RegisterPageWalker(walker MonitorComponent) {
	walker.InitMonitorStats()
	m.Walkers = append(m.Walkers, walker)
}

func (m *CaPWQMonitor) Start(now akita.VTimeInSec) {
	if !m.initialized {
		for _, l1v := range m.L1VCaches {
			l1v.ClearMonitorStats()
		}

		for _, walker := range m.Walkers {
			walker.ClearMonitorStats()
		}

		m.initialized = true
		m.numEpoches = 0
		m.numL1VCacheLength = 0
		m.numWalkerLength = 0
	}

	m.running = true

	m.TickLater(now)
}

func (m *CaPWQMonitor) Stop() {
	for _, l1v := range m.L1VCaches {
		l1v.ClearMonitorStats()
	}

	for _, walker := range m.Walkers {
		walker.ClearMonitorStats()
	}

	m.running = false
	m.numL1VCacheLength = 0
	m.numWalkerLength = 0
}

func (m *CaPWQMonitor) CollectL1VCacheComponentStats(
	component MonitorComponent,
) {
	if m.numEpoches == 0 {
		component.ClearMonitorStats()

		return
	}

	m.numL1VCacheLength += component.
		GetMonitorStats().(*CaPWQMonitorStats).Length

	component.ClearMonitorStats()
}

func (m *CaPWQMonitor) CollectWalkerComponentStats(
	component MonitorComponent,
) {
	if m.numEpoches == 0 {
		component.ClearMonitorStats()

		return
	}

	m.numWalkerLength += component.
		GetMonitorStats().(*CaPWQMonitorStats).Length

	component.ClearMonitorStats()
}

type CaPWQMonitorStats struct {
	Name string

	Length uint64
}

func (stat *CaPWQMonitorStats) Clear() {
	stat.Length = 0
}

type StatItem struct {
	NumEpoches          uint64
	L1VCacheUtilization float64
	WalkerUtilization   float64
}
