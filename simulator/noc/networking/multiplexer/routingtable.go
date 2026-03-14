package multiplexer

import (
	"gitlab.com/akita/akita"
)

type RoutingTable interface {
	AddRoute(srcPort akita.Port, dstPort akita.Port)
	Find(dstPort akita.Port) akita.Port
	GetAllSrcPorts() []akita.Port
	SetDefaultPort(port akita.Port)
}

type MapRoutingTable struct {
	table map[akita.Port]akita.Port

	defaultPort akita.Port
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

func (r *MapRoutingTable) Find(dstPort akita.Port) akita.Port {
	if srcPort, exists := r.table[dstPort]; exists {
		return srcPort
	}
	panic("Route does not exist")
}

func (r *MapRoutingTable) GetAllSrcPorts() []akita.Port {
	var ports []akita.Port
	for srcPort := range r.table {
		ports = append(ports, srcPort)
	}
	return ports
}

func (r *MapRoutingTable) SetDefaultPort(port akita.Port) {
	if r.defaultPort != nil {
		panic("Default port already set")
	}

	r.defaultPort = port
}
