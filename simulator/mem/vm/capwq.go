package vm

import (
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/util/ca"
)

type CaPWQBlock struct {
	Req   *device.TranslationReq // easy for user to get PID and VPN
	VAddr uint64                 // easy for calculating PPN with offset

	PID           ca.PID
	VPN           uint64
	PPNWithOffset uint64
	Level         int
	MsgID         string
}
