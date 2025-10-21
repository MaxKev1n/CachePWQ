package mmu

import (
	"encoding/binary"
	"strings"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/mem/cache"
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/util/tracing"
)

type MMU interface {
	tracing.NamedHookable

	GetNumActiveWalkers() int
	ToTopPort() akita.Port
	TranslationPortPort() akita.Port
	SetLowModuleFinder(lmf cache.LowModuleFinder)
	CommandProcessorPort() akita.Port
	SetCommandProcessorPort(akita.Port)
	ControlPortPort() akita.Port
}

type transactionState int

const (
	newTransaction transactionState = iota
	sentToPageWalkCache
	pageWalkCacheDone
	sentToMem
	memDone
	transactionFinished
)

type transaction struct {
	akita.MsgMeta

	req  *device.TranslationReq
	page device.Page
	//cycleLeft int
	//migration *device.PageMigrationReqToDriver
	level             int
	msgID             string
	state             transactionState
	PPN               uint64
	vAddr             uint64
	remoteMemAccesses int
}

func (r *transaction) Meta() *akita.MsgMeta {
	return &r.MsgMeta
}

func div(x, y float64) float64 {
	if y == 0 {
		return 0
	}
	return x / y
}

func getAccessResultString(accessResult mem.AccessResult) (str string) {
	switch accessResult {
	case mem.ReadHit:
		str = "read-hit"
		// fmt.Println("read hit")
	case mem.ReadMiss:
		str = "read-miss"
		// fmt.Println("read miss")
	case mem.ReadMSHRHit:
		str = "read-mshr-hit"
		// fmt.Println("read mshr hit")
	default:
		panic("unknown access type")
	}
	return
}

func getChipletNum(component string) (chipletNum string) {
	chipletNum = strings.Split(component, "_")[1][1:2]
	return
}

func uint64ToBytes(data uint64) []byte {
	bytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(bytes, data)
	return bytes
}

func unique(intSlice []uint64) []uint64 {
	keys := make(map[int]bool)
	list := []uint64{}
	for _, entry := range intSlice {
		if _, value := keys[int(entry)]; !value {
			keys[int(entry)] = true
			list = append(list, entry)
		}
	}
	return list
}
