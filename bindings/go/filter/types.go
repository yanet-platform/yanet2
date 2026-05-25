package filter

import (
	"net"
	"net/netip"
)

type Device struct {
	Name string
}

type Devices []Device

type IPNet struct {
	Addr netip.Addr
	Mask netip.Addr
}

// UnspecifiedIPv4 is the IPv4 wildcard network (0.0.0.0/0).
var UnspecifiedIPv4 = IPNet{
	Addr: netip.IPv4Unspecified(),
	Mask: netip.IPv4Unspecified(),
}

// UnspecifiedIPv6 is the IPv6 wildcard network (::/0).
var UnspecifiedIPv6 = IPNet{
	Addr: netip.IPv6Unspecified(),
	Mask: netip.IPv6Unspecified(),
}

// MustParseIPNet parses a CIDR prefix string into an IPNet.
//
// The IPv4/IPv6 family is inferred from the parsed address. Panics on
// malformed input; intended for static test data and other contexts
// where a parse failure is a programmer error.
func MustParseIPNet(s string) IPNet {
	p := netip.MustParsePrefix(s)
	if p.Addr().Is4() {
		return IPNet{
			Addr: p.Addr(),
			Mask: makePrefix4(p.Bits()),
		}
	}

	return IPNet{
		Addr: p.Addr(),
		Mask: makePrefix6(p.Bits()),
	}
}

type IPNets []IPNet

type PortRange struct {
	From uint16
	To   uint16
}

type PortRanges []PortRange

type ProtoRange struct {
	From uint16
	To   uint16
}

type ProtoRanges []ProtoRange

type VlanRange struct {
	From uint16
	To   uint16
}

type VlanRanges []VlanRange

// Net4sFromPrefixes converts standard library prefixes to IPNets,
// keeping only IPv4 entries.
func Net4sFromPrefixes(prefixes []netip.Prefix) (IPNets, error) {
	out := make([]IPNet, 0, len(prefixes))

	for _, prefix := range prefixes {
		if !prefix.Addr().Is4() {
			continue
		}

		out = append(out, IPNet{
			Addr: prefix.Addr(),
			Mask: makePrefix4(prefix.Bits()),
		})
	}

	return out, nil
}

// Net6sFromPrefixes converts standard library prefixes to IPNets,
// keeping only IPv6 entries.
func Net6sFromPrefixes(prefixes []netip.Prefix) (IPNets, error) {
	out := make([]IPNet, 0, len(prefixes))

	for _, prefix := range prefixes {
		if !prefix.Addr().Is6() {
			continue
		}

		out = append(out, IPNet{
			Addr: prefix.Addr(),
			Mask: makePrefix6(prefix.Bits()),
		})
	}

	return out, nil
}

func makePrefix4(bits int) netip.Addr {
	mask := net.CIDRMask(bits, 32)
	return netip.AddrFrom4([4]byte(mask))
}

func makePrefix6(bits int) netip.Addr {
	mask := net.CIDRMask(bits, 128)
	return netip.AddrFrom16([16]byte(mask))
}

// Subtype is a closed range of protocol subtype bytes used by NewProtoRange.
type Subtype struct {
	From uint8
	To   uint8
}

// AnySubtype returns a Subtype covering every value (0x00..0xFF).
func AnySubtype() Subtype {
	return RangeSubtype(0, 0xFF)
}

// ExactSubtype returns a Subtype matching exactly value.
func ExactSubtype(value uint8) Subtype {
	return RangeSubtype(value, value)
}

// RangeSubtype returns a Subtype covering [from, to] inclusive.
func RangeSubtype(from, to uint8) Subtype {
	return Subtype{From: from, To: to}
}

// NewProtoRange returns a ProtoRange for proto with the given subtype range.
func NewProtoRange(proto uint8, subtype Subtype) ProtoRange {
	return ProtoRange{
		From: NewProto(proto, subtype.From),
		To:   NewProto(proto, subtype.To),
	}
}

// NewProto returns the 16-bit proto encoding used by filter.ProtoRange.
//
// The encoding packs the L4 protocol number in the high byte and a
// protocol-specific subtype in the low byte: TCP flags, ICMP type, etc.
//
// A proto range over a single protocol with any subtype is
// (NewProto(proto, 0), NewProto(proto, 0xFF)).
func NewProto(proto uint8, subtype uint8) uint16 {
	return uint16(proto)<<8 | uint16(subtype)
}
