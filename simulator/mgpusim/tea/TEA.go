package tea

import (
	"fmt"
	"log"

	"github.com/tebeka/atexit"
	"gitlab.com/akita/akita"
	"gitlab.com/akita/mgpusim/timing/wavefront"
)

type GPUCore struct {
	name string

	wavefronts []*wavefront.Wavefront

	Oracle map[uint64]uint64
}

type TimeEventAnalysisEngine struct {
	*akita.TickingComponent

	GPUCore          map[string]*GPUCore
	CompletedGPUCore []*GPUCore
}

func NewTimeEventAnalysisEngine(
	engine akita.Engine,
) *TimeEventAnalysisEngine {
	teaEngine := &TimeEventAnalysisEngine{}

	teaEngine.TickingComponent = akita.NewTickingComponent(
		"TimeEventAnalysisEngine",
		engine,
		1*akita.MHz,
		teaEngine,
	)
	teaEngine.GPUCore = make(map[string]*GPUCore)

	atexit.Register(func() {
		for _, cu := range teaEngine.CompletedGPUCore {
			log.Printf("GPU core: %s", cu.name)
			for pc, cycles := range cu.Oracle {
				log.Printf("  PC: 0x%X, Cycles: %v", pc, cycles)
			}
		}
	})

	return teaEngine
}

func (tea *TimeEventAnalysisEngine) Tick(now akita.VTimeInSec) bool {
	tea.EvaluateWfs()

	return len(tea.GPUCore) > 0
}

func (tea *TimeEventAnalysisEngine) EvaluateWfs() {
	for _, cu := range tea.GPUCore {
		numReadyWfs := 0
		numRunningWfs := 0
		numStalledWfs := 0
		numOthers := 0

		for _, wf := range cu.wavefronts {
			switch wf.State {
			case wavefront.WfReady:
				numReadyWfs++
			case wavefront.WfRunning:
				numRunningWfs++
			case wavefront.WfAtBarrier:
				numStalledWfs++
			default:
				numOthers++
			}
		}

		// Number of cycles between two calls to EvaluateWfs
		cycleInterval := 1 * akita.GHz / tea.Freq

		attributeCycle := float64(cycleInterval) / float64(numRunningWfs+numStalledWfs)

		for _, wf := range cu.wavefronts {
			if wf.State == wavefront.WfRunning || wf.State == wavefront.WfAtBarrier {
				if _, ok := cu.Oracle[wf.PC]; !ok {
					cu.Oracle[wf.PC] = 0
				}
				cu.Oracle[wf.PC] += uint64(attributeCycle)
			}
		}
	}
}

func (tea *TimeEventAnalysisEngine) RegisterWavefront(
	cuName string,
	wf *wavefront.Wavefront,
) {
	if len(tea.GPUCore) == 0 {
		tea.TickNow(tea.Engine.CurrentTime())
	}

	cu, ok := tea.GPUCore[cuName]
	if !ok {
		cu = &GPUCore{
			name:   cuName,
			Oracle: make(map[uint64]uint64),
		}
		tea.GPUCore[cuName] = cu
	}

	for _, existingWf := range cu.wavefronts {
		if existingWf.UID == wf.UID {
			panic(fmt.Sprintf("Wavefront %s already registered in CU %s",
				wf.UID, cuName))
		}
	}

	cu.wavefronts = append(cu.wavefronts, wf)

	wf.PSV = wavefront.NewPerfSignatureVector(0)
}

func (tea *TimeEventAnalysisEngine) RemoveWavefront(
	cuName string,
	wf *wavefront.Wavefront,
) {
	core, ok := tea.GPUCore[cuName]
	if !ok {
		panic("CU not found")
	}

	for i, existingWf := range core.wavefronts {
		if existingWf == wf {
			core.wavefronts = append(
				core.wavefronts[:i],
				core.wavefronts[i+1:]...,
			)

			if len(core.wavefronts) == 0 {
				tea.CompletedGPUCore = append(tea.CompletedGPUCore, core)

				delete(tea.GPUCore, cuName)
			}

			return
		}
	}
}

func (tea *TimeEventAnalysisEngine) GenerateNewPSV(
	cuName string,
	wf *wavefront.Wavefront,
) {
	core, ok := tea.GPUCore[cuName]
	if !ok {
		panic("CU not found")
	}

	for _, existingWf := range core.wavefronts {
		if existingWf == wf {
			psv := wavefront.NewPerfSignatureVector(wf.PC)

			wf.PSV = psv

			return
		}
	}
	panic("Wavefront not found")
}
