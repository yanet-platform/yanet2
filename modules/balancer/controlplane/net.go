package balancer

import (
	"unsafe"

	"github.com/yanet-platform/yanet2/common/filterpb"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

// writeNet4Addr writes a 4-byte address to a shared-memory Net4Addr.
func writeNet4Addr(dst *Net4Addr, addr []byte) {
	copy(dst.Bytes[:], addr)
}

// writeNet6Addr writes a 16-byte address to a shared-memory Net6Addr.
func writeNet6Addr(dst *Net6Addr, addr []byte) {
	copy(dst.Bytes[:], addr)
}

// Bytes returns the address bytes for the given IP protocol.
// For IPv6, this reinterprets the C union (V4 [4]byte + 12 byte padding = 16 bytes)
// as a contiguous 16-byte array via unsafe, matching the C union layout.
func (a *NetAddr) Bytes(ipProto int) []byte {
	if ipProto == ipprotoIP {
		return a.V4.Bytes[:]
	}
	return (*[16]byte)(unsafe.Pointer(&a.V4.Bytes[0]))[:]
}

func (a *Net) AddrBytes(ipProto int) []byte {
	if ipProto == ipprotoIP {
		return a.V4.Addr[:]
	}
	// Net is defined as Net4 (8 bytes) + 24 bytes padding = 32 bytes total,
	// which is the same layout as Net6 (addr[16] + mask[16]). We write addr
	// and mask directly into the 32-byte buffer via an unsafe cast instead of
	// going through the Net4 fields, which would only cover the first 8 bytes.
	b := (*[32]byte)(unsafe.Pointer(&a.V4.Addr[0]))
	return b[:16]
}

func (a *Net) MaskBytes(ipProto int) []byte {
	if ipProto == ipprotoIP {
		return a.V4.Mask[:]
	}
	b := (*[32]byte)(unsafe.Pointer(&a.V4.Addr[0]))
	return b[16:]
}

func writeNetAddr(dst *NetAddr, addr []byte) {
	if len(addr) == 4 {
		copy(dst.Bytes(ipprotoIP), addr)
	} else {
		copy(dst.Bytes(ipprotoIPv6), addr)
	}
}

// writeNet writes a filterpb.IPNet (addr + mask) to a shared-memory Net union.
// The Net union is 32 bytes: for IPv4 it uses Net4 (addr[4] + mask[4]),
// for IPv6 it uses Net6 (addr[16] + mask[16]).
// Src must be IPv4 or IPv6.
//
// The address is pre-masked (addr[i] &= mask[i]) before writing to satisfy
// the dataplane invariant documented in real.h: the tunnel code relies on
// addr having zero bits in every position where mask is zero.
func writeNet(dst *Net, src *filterpb.IPNet) {
	addr := src.Addr
	mask := src.Mask
	proto := ipprotoIP
	if len(mask) == 16 {
		proto = ipprotoIPv6
	}
	dstAddr := dst.AddrBytes(proto)
	for i := range len(addr) {
		dstAddr[i] = addr[i] & mask[i]
	}
	copy(dst.MaskBytes(proto), mask)
}

// transportProtoToC converts a protobuf TransportProto to the C constant.
func transportProtoToC(proto balancerpb.TransportProto) uint8 {
	if proto == balancerpb.TransportProto_TCP {
		return ipprotoTCP
	}
	return ipprotoUDP
}
