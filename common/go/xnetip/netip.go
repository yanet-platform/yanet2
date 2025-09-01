package xnetip

import (
	"encoding/binary"
	"net"
	"net/netip"
)

func LastAddr(prefix netip.Prefix) netip.Addr {
	ip := prefix.Addr()
	bits := prefix.Bits()

	if prefix.Addr().Is4() {
		v4b := ip.As4()
		addrBits := binary.BigEndian.Uint32(v4b[:])
		wildcardBits := uint32(1<<(32-bits) - 1)
		broadCastBits := addrBits | wildcardBits

		binary.BigEndian.PutUint32(v4b[:], broadCastBits)
		return netip.AddrFrom4(v4b)
	} else {
		v6b := ip.As16()

		addrBits := binary.BigEndian.Uint64(v6b[:])
		startByte := 0
		if bits >= 64 {
			bits -= 64
			startByte = 8
			addrBits = binary.BigEndian.Uint64(v6b[8:])
		} else {
			// Put uint64_max into last part of the addr
			binary.BigEndian.PutUint64(v6b[8:], ^uint64(0))
		}
		wildcardBits := uint64(1<<(64-bits) - 1)
		broadCastBits := addrBits | wildcardBits
		binary.BigEndian.PutUint64(v6b[startByte:], broadCastBits)
		return netip.AddrFrom16(v6b)
	}
}

func Mask(prefix netip.Prefix) net.IPMask {
	size := net.IPv4len
	if !prefix.Addr().Is4() {
		size = net.IPv6len
	}
	return net.CIDRMask(prefix.Bits(), 8*size)
}
