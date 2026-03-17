package xnetip

import (
	"encoding/binary"
	"math/bits"
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

// RangeToCIDR converts an address range to a CIDR prefix if possible.
//
// Returns false (and a zero prefix) if:
//   - from and to belong to different address families;
//   - the range does not correspond to a single prefix (e.g., from > to,
//     or host bits are not properly aligned).
func RangeToCIDR(from, to netip.Addr) (netip.Prefix, bool) {
	if from.Is4() != to.Is4() {
		return netip.Prefix{}, false
	}

	var prefixLen int
	if from.Is4() {
		prefixLen = commonPrefixLen4(from, to)
	} else {
		prefixLen = commonPrefixLen6(from, to)
	}

	prefix, err := from.Prefix(prefixLen)
	if err != nil {
		return netip.Prefix{}, false
	}
	if prefix.Addr() != from {
		return netip.Prefix{}, false
	}
	if LastAddr(prefix) != to {
		return netip.Prefix{}, false
	}

	return prefix, true
}

func commonPrefixLen4(a, b netip.Addr) int {
	a4 := a.As4()
	b4 := b.As4()
	xor := binary.BigEndian.Uint32(a4[:]) ^ binary.BigEndian.Uint32(b4[:])
	return bits.LeadingZeros32(xor)
}

func commonPrefixLen6(a, b netip.Addr) int {
	a16 := a.As16()
	b16 := b.As16()
	xor := newUint128From16(a16).Xor(newUint128From16(b16))
	return xor.LeadingZeros()
}

func Mask(prefix netip.Prefix) net.IPMask {
	size := net.IPv4len
	if !prefix.Addr().Is4() {
		size = net.IPv6len
	}
	return net.CIDRMask(prefix.Bits(), 8*size)
}
