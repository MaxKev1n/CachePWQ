package tip

import (
	"fmt"
	"log"

	"github.com/tebeka/atexit"
	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/mgpusim/timing/wavefront"
	"gitlab.com/akita/util/psv"
)

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
				perfVector := wf.PSV
				log.Printf("PSV for WF %s %p at PC 0x%X:", wf.UID, perfVector, wf.PC)

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
						for scalarPSV := range wf.OutstandingScalarPSV {
							log.Printf("	scalarPSV for %p at PC 0x%X:", scalarPSV, scalarPSV.InstAddress)
						}
					}

					if wf.OutstandingVectorMemAccess > wf.Inst().VMCNT {
						for pc := range wf.OutstandingVectorInst {
							cu.Profile(pc, uint64(attributeCycle)/uint64(count))
						}
						for vectorPSV := range wf.OutstandingVectorPSV {
							log.Printf("	vectorPSV for %p at PC 0x%X:", vectorPSV, vectorPSV.InstAddress)

							log.Printf("Attribute to VROB for vector PSV at PC 0x%X", vectorPSV.InstAddress)
							result, msg := cu.VROB.Attribute(nil)
							if result == psv.SUCCESS {
								log.Printf("Final: Attribute to VROB\n")
								continue
							}

							log.Printf("ROB Head PSV Item: %p\n", msg.(mem.AccessReq).GetPSV())
							msg.(mem.AccessReq).GetPSV().Print()

							log.Printf("Attribute to VTranslator for vector PSV at PC 0x%X", vectorPSV.InstAddress)
							result, msg = cu.VTranslator.Attribute(msg)
							switch result {
							case psv.SUCCESS:
								log.Printf("Final: Attribute to VAT\n")
								continue
							case psv.FAIL:
								log.Printf("Attribute to VTLB for vector PSV at PC 0x%X", vectorPSV.InstAddress)
								result, msg = cu.VTLB.Attribute(msg)

								if result == psv.SUCCESS {
									log.Printf("Final: Attribute to L1VTLB\n")
									continue
								}

								log.Printf("Attribute to L2TLB for vector PSV at PC 0x%X", vectorPSV.InstAddress)
								result, msg = tip.L2TLB.Attribute(msg)
								if result == psv.SUCCESS {
									log.Printf("Final: Attribute to L1VTLB Miss\n")
								} else {
									log.Printf("Final: Attribute to L2 TLB Miss\n")
								}
							case psv.FAILSECONDARY:
								log.Printf("Attribute to VCache for vector PSV at PC 0x%X", vectorPSV.InstAddress)
								result, msg = cu.VCache.Attribute(msg)

								if result == psv.SUCCESS {
									log.Printf("Final: Attribute to L1VCache\n")
									continue
								}

								log.Printf("Attribute to L2 Cache for vector PSV at PC 0x%X", vectorPSV.InstAddress)
								for _, l2cache := range tip.L2Caches {
									if msg == nil {
										panic("msg is nil")
									}

									if l2cache.CheckTopPort(msg.Meta().Dst) {
										result, msg = l2cache.Attribute(msg)

										if result == psv.SUCCESS {
											log.Printf("Final: Attribute to L1VCache Miss\n")
										} else {
											log.Printf("Final: Attribute to L2 Cache Miss\n")
										}

										break
									}
								}
							default:
								panic("Unknown PSV result")
							}
						}
					}
				} else if wf.Inst().Opcode == 1 {
					// S_ENDPGM instruction
					if wf.OutstandingScalarMemAccess > 0 || wf.OutstandingVectorMemAccess > 0 {
						count := len(wf.OutstandingScalarInst) + len(wf.OutstandingVectorInst)

						for pc := range wf.OutstandingScalarInst {
							cu.Profile(pc, uint64(attributeCycle)/uint64(count))
						}

						for scalarPSV := range wf.OutstandingScalarPSV {
							log.Printf("	scalarPSV for %p at PC 0x%X:", scalarPSV, scalarPSV.InstAddress)
						}

						for pc := range wf.OutstandingVectorInst {
							cu.Profile(pc, uint64(attributeCycle)/uint64(count))
						}

						for vectorPSV := range wf.OutstandingVectorPSV {
							log.Printf("	vectorPSV for %p at PC 0x%X:", vectorPSV, vectorPSV.InstAddress)

							log.Printf("Attribute to VROB for vector PSV at PC 0x%X", vectorPSV.InstAddress)
							result, msg := cu.VROB.Attribute(nil)
							if result == psv.SUCCESS {
								log.Printf("Final: Attribute to VROB\n")
								continue
							}

							log.Printf("ROB Head PSV Item: %p\n", msg.(mem.AccessReq).GetPSV())
							msg.(mem.AccessReq).GetPSV().Print()

							log.Printf("Attribute to VTranslator for vector PSV at PC 0x%X", vectorPSV.InstAddress)
							result, msg = cu.VTranslator.Attribute(msg)
							switch result {
							case psv.SUCCESS:
								log.Printf("Final: Attribute to VAT\n")
								continue
							case psv.FAIL:
								log.Printf("Attribute to VTLB for vector PSV at PC 0x%X", vectorPSV.InstAddress)
								result, msg = cu.VTLB.Attribute(msg)

								if result == psv.SUCCESS {
									log.Printf("Final: Attribute to L1VTLB\n")
									continue
								}

								log.Printf("Attribute to L2TLB for vector PSV at PC 0x%X", vectorPSV.InstAddress)
								result, msg = tip.L2TLB.Attribute(msg)
								if result == psv.SUCCESS {
									log.Printf("Final: Attribute to L1VTLB Miss\n")
								} else {
									log.Printf("Final: Attribute to L2 TLB Miss\n")
								}
							case psv.FAILSECONDARY:
								log.Printf("Attribute to VCache for vector PSV at PC 0x%X", vectorPSV.InstAddress)
								result, msg = cu.VCache.Attribute(msg)

								if result == psv.SUCCESS {
									log.Printf("Final: Attribute to L1VCache\n")
									continue
								}

								log.Printf("Attribute to L2 Cache for vector PSV at PC 0x%X", vectorPSV.InstAddress)
								for _, l2cache := range tip.L2Caches {
									if msg == nil {
										panic("msg is nil")
									}

									if l2cache.CheckTopPort(msg.Meta().Dst) {
										result, msg = l2cache.Attribute(msg)

										if result == psv.SUCCESS {
											log.Printf("Final: Attribute to L1VCache Miss\n")
										} else {
											log.Printf("Final: Attribute to L2 Cache Miss\n")
										}

										break
									}
								}
							default:
								panic("Unknown PSV result")
							}
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

			return
		}
	}
	panic("Wavefront not found")
}
