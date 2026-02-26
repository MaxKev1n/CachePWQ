package mmu

import (
	"encoding/binary"
	"fmt"
	"regexp"
	"strconv"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem/cache"
	"gitlab.com/akita/util/tracing"
)

type MMU interface {
	tracing.NamedHookable

	GetNumActiveWalkers() int
	ToTopPort() akita.Port
	TranslationPortPort() akita.Port
	ToCachePort() akita.Port
	ToPageWalkCachePort() akita.Port
	SetLowModuleFinder(lmf cache.LowModuleFinder)
	CanAccept() bool
}

type Transaction interface {
	TaskID() string
	Meta() *akita.MsgMeta
}

var l2Re = regexp.MustCompile(`L2_(\d+)`)

func GetL2SliceNum(s string) (int, error) {
	m := l2Re.FindStringSubmatch(s)
	if len(m) < 2 {
		return 0, fmt.Errorf("not found")
	}
	return strconv.Atoi(m[1])
}

func Uint64ToBytes(data uint64) []byte {
	bytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(bytes, data)
	return bytes
}
