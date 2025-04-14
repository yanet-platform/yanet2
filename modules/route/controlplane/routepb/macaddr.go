package routepb

import (
	"encoding/binary"
)

// TODO: docs.
func NewMACAddressEUI48(addr [6]byte) *MACAddress {
	buf := [8]byte{}
	copy(buf[:], addr[:])

	return &MACAddress{
		Addr: binary.BigEndian.Uint64(buf[:]),
	}
}
