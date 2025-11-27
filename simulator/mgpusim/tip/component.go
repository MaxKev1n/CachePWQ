package tip

import (
	"gitlab.com/akita/akita"
	"gitlab.com/akita/util/psv"
)

type TEAComponent interface {
	GetName() string
	Attribute(msg akita.Msg) (psv.Result, akita.Msg)
	GetStalledPSV() *psv.PerfSignatureVec
	CheckTopPort(port akita.Port) bool
	CheckBottomPort(port akita.Port) bool
}

type SchedulerComponent interface {
	GetName() string
	Attribute(unit int) psv.Result
}
