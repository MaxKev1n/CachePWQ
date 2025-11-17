package psv

import (
	"log"

	"gitlab.com/akita/akita"
)

type Result int

const (
	SUCCESS Result = iota
	FAIL
	FAILSECONDARY
)

type PSVItem struct {
	Msg    akita.Msg
	SrcMsg akita.Msg
	PSV    *PerfSignatureVec
}

type PerfSignatureVec struct {
	InstAddress uint64

	IFU         []*PSVItem
	SU          []*PSVItem
	VMEM        []*PSVItem
	ROB         []*PSVItem
	AT          []*PSVItem
	L1TLB       []*PSVItem
	L2TLB       []*PSVItem
	PTW         []*PSVItem
	L1Coalescer []*PSVItem
	L1Cache     []*PSVItem
	L2Cache     []*PSVItem
}

func NewPerfSignatureVector() *PerfSignatureVec {
	PSV := &PerfSignatureVec{}

	return PSV
}

func (psv *PerfSignatureVec) AddItem(
	array *[]*PSVItem,
	msg akita.Msg,
	srcMsg akita.Msg,
	vec *PerfSignatureVec,
) {
	for _, i := range *array {
		if i.Msg == msg && i.PSV == vec && i.SrcMsg == srcMsg {
			return
		}
	}
	*array = append(*array, &PSVItem{Msg: msg, PSV: vec, SrcMsg: srcMsg})
}

func (psv *PerfSignatureVec) RemoveItem(
	array *[]*PSVItem,
	msg akita.Msg,
	vec *PerfSignatureVec,
) {
	for i, it := range *array {
		if it.Msg == msg && it.PSV == vec {
			*array = append((*array)[:i], (*array)[i+1:]...)
			return
		}
	}
}

func (psv *PerfSignatureVec) Print() {
	log.Printf("PSV %p\n", psv)
	for _, item := range psv.SU {
		log.Printf("  	SU: %+v", item)
	}
	for _, item := range psv.VMEM {
		log.Printf("  VMEM: %+v", item)
	}
	for _, item := range psv.ROB {
		log.Printf("  ROB: %+v", item)
	}
	for _, item := range psv.AT {
		log.Printf("  AT: %+v", item)
	}
	for _, item := range psv.L1TLB {
		log.Printf("  L1TLB: %+v", item)
	}
	for _, item := range psv.L2TLB {
		log.Printf("  L2TLB: %+v", item)
	}
	for _, item := range psv.PTW {
		log.Printf("  PTW: %+v", item)
	}
	for _, item := range psv.L1Coalescer {
		log.Printf("  L1Coalescer: %+v", item)
	}
	for _, item := range psv.L1Cache {
		log.Printf("  L1Cache: %+v", item)
	}
	for _, item := range psv.L2Cache {
		log.Printf("  L2Cache: %+v", item)
	}
}
