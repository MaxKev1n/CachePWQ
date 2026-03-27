package vm

import (
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/util/ca"
)

type CaPWQBlock struct {
	PID     ca.PID
	Address uint64
	Level   int

	// For CAM, do not occupy space.
	PPNWithOffset uint64
	PPN           uint64

	// For debugging.
	MsgID string
	Req   *device.TranslationReq
}
