package tip

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

func (core *GPUCore) Profile(
	pc uint64,
	cycles uint64,
) {
	if _, ok := core.Oracle[pc]; !ok {
		core.Oracle[pc] = 0
	}
	core.Oracle[pc] += cycles
}

type TimeEventAnalysisEngine struct {
	*akita.TickingComponent

	GPUCore          map[string]*GPUCore
	CompletedGPUCore []*GPUCore
}

func NewTimeEventAnalysisEngine(
	engine akita.Engine,
) *TimeEventAnalysisEngine {
	tipEngine := &TimeEventAnalysisEngine{}

	tipEngine.TickingComponent = akita.NewTickingComponent(
		"TimeProportionalEngine",
		engine,
		100*akita.KHz,
		tipEngine,
	)
	tipEngine.GPUCore = make(map[string]*GPUCore)

	atexit.Register(func() {
		for _, cu := range tipEngine.CompletedGPUCore {
			log.Printf("GPU core: %s", cu.name)
			for pc, cycles := range cu.Oracle {
				log.Printf("  PC: 0x%X, Cycles: %v", pc, cycles)
			}
		}
	})

	return tipEngine
}

func (tip *TimeEventAnalysisEngine) Tick(now akita.VTimeInSec) bool {
	tip.EvaluateWfs()

	return len(tip.GPUCore) > 0
}

func (tip *TimeEventAnalysisEngine) EvaluateWfs() {
	for _, cu := range tip.GPUCore {
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
		cycleInterval := 1 * akita.GHz / tip.Freq

		attributeCycle := float64(cycleInterval) / float64(numRunningWfs+numStalledWfs)

		for _, wf := range cu.wavefronts {
			if wf.State == wavefront.WfRunning || wf.State == wavefront.WfAtBarrier {
				if wf.Inst().Opcode == 12 {
					// S_WAITCNT instruction
					count := 0
					if wf.OutstandingScalarMemAccess > wf.Inst().LKGMCNT {
						count += len(wf.OutstandingScalarInst)
					}

					if wf.OutstandingVectorMemAccess > wf.Inst().VMCNT {
						count += len(wf.OutstandingVectorInst)
					}

					if wf.OutstandingScalarMemAccess > wf.Inst().LKGMCNT {
						for pc := range wf.OutstandingScalarInst {
							cu.Profile(pc, uint64(attributeCycle)/uint64(count))
						}
					}

					if wf.OutstandingVectorMemAccess > wf.Inst().VMCNT {
						for pc := range wf.OutstandingVectorInst {
							cu.Profile(pc, uint64(attributeCycle)/uint64(count))
						}
					}
				} else if wf.Inst().Opcode == 1 {
					// S_ENDPGM instruction
					if wf.OutstandingScalarMemAccess > 0 || wf.OutstandingVectorMemAccess > 0 {
						count := len(wf.OutstandingScalarInst) + len(wf.OutstandingVectorInst)

						for pc := range wf.OutstandingScalarInst {
							cu.Profile(pc, uint64(attributeCycle)/uint64(count))
						}

						for pc := range wf.OutstandingVectorInst {
							cu.Profile(pc, uint64(attributeCycle)/uint64(count))
						}
					}
				} else {
					cu.Profile(wf.PC, uint64(attributeCycle))
				}
			}
		}
	}
}

func (tip *TimeEventAnalysisEngine) RegisterWavefront(
	cuName string,
	wf *wavefront.Wavefront,
) {
	if len(tip.GPUCore) == 0 {
		tip.TickNow(tip.Engine.CurrentTime())
	}

	cu, ok := tip.GPUCore[cuName]
	if !ok {
		cu = &GPUCore{
			name:   cuName,
			Oracle: make(map[uint64]uint64),
		}
		tip.GPUCore[cuName] = cu
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

func (tip *TimeEventAnalysisEngine) RemoveWavefront(
	cuName string,
	wf *wavefront.Wavefront,
) {
	core, ok := tip.GPUCore[cuName]
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
				tip.CompletedGPUCore = append(tip.CompletedGPUCore, core)

				delete(tip.GPUCore, cuName)
			}

			return
		}
	}
}

func (tip *TimeEventAnalysisEngine) GenerateNewPSV(
	cuName string,
	wf *wavefront.Wavefront,
) {
	core, ok := tip.GPUCore[cuName]
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
