package multiplexer

import (
	"gitlab.com/akita/akita"
)

type RoutingTable interface {
	AddRoute(srcPort akita.Port, dstPort akita.Port)
	Find(dstPort akita.Port) (akita.Port, bool)
	GetAllSrcPorts() []akita.Port
}

type MapRoutingTable struct {
	table map[akita.Port]akita.Port
}

func NewMapRoutingTable() *MapRoutingTable {
	return &MapRoutingTable{
		table: make(map[akita.Port]akita.Port),
	}
}

func (r *MapRoutingTable) AddRoute(srcPort akita.Port, dstPort akita.Port) {
	if _, exists := r.table[srcPort]; exists {
		panic("Route already exists")
	}

	r.table[srcPort] = dstPort
}

func (r *MapRoutingTable) Find(dstPort akita.Port) (akita.Port, bool) {
	if srcPort, exists := r.table[dstPort]; exists {
		return srcPort, true
	}

	return nil, false
}

func (r *MapRoutingTable) GetAllSrcPorts() []akita.Port {
	var ports []akita.Port
	for srcPort := range r.table {
		ports = append(ports, srcPort)
	}
	return ports
}
