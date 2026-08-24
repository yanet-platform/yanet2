package filter

import (
	"github.com/yanet-platform/xnetip"
)

type Device struct {
	Name string
}

type Devices []Device

// UnspecifiedIPv4 is the IPv4 wildcard network (0.0.0.0/0).
var UnspecifiedIPv4 = xnetip.MustParseContiguous4("0.0.0.0/0")

// UnspecifiedIPv6 is the IPv6 wildcard network (::/0).
var UnspecifiedIPv6 = xnetip.MustParseBiContiguous("::/0")

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

type Fragment uint32

// Represent Fragmented attribute
const (
	FragmentAny Fragment = iota
	FragmentNone
	FragmentFrag
)
