package device

import (
	"gitlab.com/akita/mem"
)

// A deviceDemandPagingMemoryState implements DeviceMemoryState as a interleaved allocator
type deviceDemandPagingMemoryState struct {
	bankSize        uint64
	log2PageSize    uint64
	initialAddress  uint64
	storageSize     uint64
	availablePAddrs []uint64
}

func (dims *deviceDemandPagingMemoryState) setInitialAddress(addr uint64) {
	dims.initialAddress = addr
	pageSize := uint64(1 << dims.log2PageSize)
	endAddr := dims.initialAddress + dims.storageSize
	for addr := dims.initialAddress; addr < endAddr; addr += pageSize {
		dims.availablePAddrs = append(dims.availablePAddrs, addr)
	}
}

func newdeviceDemandPagingMemoryState(log2pagesize uint64) DeviceMemoryState {
	return &deviceDemandPagingMemoryState{
		log2PageSize: log2pagesize,
		bankSize:     256 * mem.MB,
	}
}

func (dims *deviceDemandPagingMemoryState) getInitialAddress() uint64 {
	return dims.initialAddress
}

func (dims *deviceDemandPagingMemoryState) setStorageSize(size uint64) {
	dims.storageSize = size
}

func (dims *deviceDemandPagingMemoryState) getStorageSize() uint64 {
	return dims.storageSize
}

func (dims *deviceDemandPagingMemoryState) addSinglePAddr(addr uint64) {
	dims.availablePAddrs = append(dims.availablePAddrs, addr)
}

func (dims *deviceDemandPagingMemoryState) popNextAvailablePAddrs() uint64 {
	nextPAddr := dims.availablePAddrs[0]
	dims.availablePAddrs = dims.availablePAddrs[1:]
	return nextPAddr
}

func (dims *deviceDemandPagingMemoryState) noAvailablePAddrs() bool {
	return len(dims.availablePAddrs) == 0
}

func (dims *deviceDemandPagingMemoryState) allocateMultiplePages(
	numPages int,
) (pAddrs []uint64) {
	for i := 0; i < numPages; i++ {
		pAddr := dims.popNextAvailablePAddrs()
		pAddrs = append(pAddrs, pAddr)
	}
	return pAddrs
}

func (dims *deviceDemandPagingMemoryState) allocatePageTablePage(vAddr, pAddr uint64) uint64 {
	return dims.allocateMultiplePages(1)[0]
}

func (dims *deviceDemandPagingMemoryState) allocatePageOnChiplet(chiplet int) uint64 {
	panic("not implemented")
	return 0
}
