package multiplexer

import "gitlab.com/akita/akita"

type RoutingTable interface {
	Find(dstPort akita.Port) (akita.Port, bool)
}
