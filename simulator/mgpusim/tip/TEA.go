package tip

import (
	"fmt"
	"log"

	"github.com/tebeka/atexit"
	"gitlab.com/akita/akita"
	"gitlab.com/akita/mgpusim/timing/wavefront"
	"gitlab.com/akita/util/psv"
)

type TEAItems struct {
	InstAddress uint64
	Event       psv.Event
}

type GPUCore struct {
	name string

	wavefronts []*wavefront.Wavefront

	Oracle map[uint64]uint64

	// Components
	VMemUnit    TEAComponent
	VROB        TEAComponent
	VTranslator TEAComponent
	VTLB        TEAComponent
	VCache      TEAComponent

	PICS map[TEAItems]uint64
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

	L2TLB    TEAComponent
	L2Caches []TEAComponent
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
			for item, cycles := range cu.PICS {
				log.Printf("  InstAddr: 0x%X, Event: %v, Cycles: %v",
					item.InstAddress, item.Event, cycles)
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

		attributedEvents := make([]TEAItems, 0)

		for _, wf := range cu.wavefronts {
			if wf.State == wavefront.WfRunning || wf.State == wavefront.WfAtBarrier {
				if wf.Inst().Opcode == 12 {
					// S_WAITCNT instruction
					attributedPSVs := make(map[*psv.PerfSignatureVec]struct{})

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

						for scalarPSV := range wf.OutstandingScalarPSV {
							if _, ok := attributedPSVs[scalarPSV]; !ok {
								attributedPSVs[scalarPSV] = struct{}{}
							}
						}
					}

					if wf.OutstandingVectorMemAccess > wf.Inst().VMCNT {
						for pc := range wf.OutstandingVectorInst {
							cu.Profile(pc, uint64(attributeCycle)/uint64(count))
						}

						for vectorPSV := range wf.OutstandingVectorPSV {
							if _, ok := attributedPSVs[vectorPSV]; !ok {
								attributedPSVs[vectorPSV] = struct{}{}
							}
						}
					}

					for attributedPSV := range attributedPSVs {
						event := tip.Attribute(cu)

						attributedEvents = append(attributedEvents,
							TEAItems{
								InstAddress: attributedPSV.InstAddress,
								Event:       event,
							},
						)

						delete(attributedPSVs, attributedPSV)
					}
					attributedPSVs = nil
				} else if wf.Inst().Opcode == 1 {
					// S_ENDPGM instruction
					if wf.OutstandingScalarMemAccess > 0 || wf.OutstandingVectorMemAccess > 0 {
						count := len(wf.OutstandingScalarInst) + len(wf.OutstandingVectorInst)

						attributedPSVs := make(map[*psv.PerfSignatureVec]struct{})

						for pc := range wf.OutstandingScalarInst {
							cu.Profile(pc, uint64(attributeCycle)/uint64(count))
						}

						for scalarPSV := range wf.OutstandingScalarPSV {
							if _, ok := attributedPSVs[scalarPSV]; !ok {
								attributedPSVs[scalarPSV] = struct{}{}
							}
						}

						for pc := range wf.OutstandingVectorInst {
							cu.Profile(pc, uint64(attributeCycle)/uint64(count))
						}

						for vectorPSV := range wf.OutstandingVectorPSV {
							if _, ok := attributedPSVs[vectorPSV]; !ok {
								attributedPSVs[vectorPSV] = struct{}{}
							}
						}

						for attributedPSV := range attributedPSVs {
							event := tip.Attribute(cu)

							attributedEvents = append(attributedEvents,
								TEAItems{
									InstAddress: attributedPSV.InstAddress,
									Event:       event,
								},
							)

							delete(attributedPSVs, attributedPSV)
						}
						attributedPSVs = nil
					}
				} else {
					cu.Profile(wf.PC, uint64(attributeCycle))

					event := tip.Attribute(cu)

					attributedEvents = append(attributedEvents,
						TEAItems{
							InstAddress: wf.PSV.InstAddress,
							Event:       event,
						},
					)
				}
			}
		}

		attributeCycle = float64(cycleInterval) / float64(len(attributedEvents))

		for _, item := range attributedEvents {
			if _, ok := cu.PICS[item]; !ok {
				cu.PICS[item] = 0
			}
			cu.PICS[item] += uint64(attributeCycle)
		}
	}
}

func (tip *TimeEventAnalysisEngine) RegisterWavefront(
	cuName string,
	wf *wavefront.Wavefront,
) {
	cu, ok := tip.GPUCore[cuName]
	if !ok {
		panic(fmt.Sprintf("GPU core [%s] not found", cuName))
	}

	for _, existingWf := range cu.wavefronts {
		if existingWf.UID == wf.UID {
			panic(fmt.Sprintf("Wavefront %s already registered in CU %s",
				wf.UID, cuName))
		}
	}

	cu.wavefronts = append(cu.wavefronts, wf)

	wf.PSV = psv.NewPerfSignatureVector()
}

func (tip *TimeEventAnalysisEngine) RegisterCU(
	cuName string,
	vmemUnit TEAComponent,
	vrob TEAComponent,
	vtranslator TEAComponent,
	vtlb TEAComponent,
	vcache TEAComponent,
) {
	if len(tip.GPUCore) == 0 {
		tip.TickNow(tip.Engine.CurrentTime())
	}

	_, exists := tip.GPUCore[cuName]
	if exists {
		return
	}

	cu := &GPUCore{
		name:        cuName,
		Oracle:      make(map[uint64]uint64),
		VMemUnit:    vmemUnit,
		VROB:        vrob,
		VTranslator: vtranslator,
		VTLB:        vtlb,
		VCache:      vcache,
		PICS:        make(map[TEAItems]uint64),
	}
	tip.GPUCore[cuName] = cu
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
			psv := psv.NewPerfSignatureVector()

			wf.PSV = psv
			wf.PSV.InstAddress = wf.PC

			return
		}
	}
	panic("Wavefront not found")
}

func (tip *TimeEventAnalysisEngine) Attribute(
	cu *GPUCore,
) psv.Event {
	result, msg := cu.VROB.Attribute(nil)
	if result == psv.SUCCESS {
		return psv.BASE
	}

	result, msg = cu.VTranslator.Attribute(msg)
	switch result {
	case psv.SUCCESS:
		return psv.BASE
	case psv.FAIL:
		result, msg = cu.VTLB.Attribute(msg)

		if result == psv.SUCCESS {
			return psv.BASE
		}

		result, msg = tip.L2TLB.Attribute(msg)
		if result == psv.SUCCESS {
			return psv.L1TLBMISS
		} else {
			return psv.L2TLBMISS
		}
		panic("Unreachable")
	case psv.FAILSECONDARY:
		result, msg = cu.VCache.Attribute(msg)

		if result == psv.SUCCESS {
			return psv.BASE
		}

		for _, l2cache := range tip.L2Caches {
			if msg == nil {
				panic("msg is nil")
			}

			if l2cache.CheckTopPort(msg.Meta().Dst) {
				result, msg = l2cache.Attribute(msg)

				if result == psv.SUCCESS {
					return psv.L1CACHEMISS
				} else {
					return psv.L2CACHEMISS
				}
				panic("Unreachable")
			}
		}
	default:
		panic("Unknown PSV result")
	}
	panic("Don't find the component to attribute")
}
