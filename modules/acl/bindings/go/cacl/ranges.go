package cacl

import "github.com/yanet-platform/yanet2/bindings/go/filter"

// The IP protocol numbers of the transport specific attributes.
const (
	ipProtoICMPv4 uint8 = 1
	ipProtoTCP    uint8 = 6
	ipProtoUDP    uint8 = 17
	ipProtoICMPv6 uint8 = 58
)

// LineRange is one derived domain interval of a line attribute,
// inclusive bounds.
type LineRange struct {
	From uint32
	To   uint32
}

// ProtoSplit is the byte split of the authored 16 bit protocol
// ranges of one rule: the encoding packs the IP protocol number in
// the high byte and the transport specific byte in the low one, and
// every line attribute consumes one side of it.
type ProtoSplit struct {
	// IpProto holds the high byte span of every authored range: the
	// declared protocol interval the shared core discriminates.
	IpProto []LineRange
	// TCPFlags holds the low byte sub-span of every TCP covering
	// range, clamped to the whole byte at the edges outside TCP: the
	// only protocol the flags leaf discriminates.
	TCPFlags []LineRange
	// ICMPTypes holds the low byte sub-spans of the ICMP and the
	// ICMPv6 covering ranges: the two protocols share the type byte
	// semantics, so one leaf discriminates both.
	ICMPTypes []LineRange
	// WholeL4 reports that every authored range leaves the low byte
	// whole: such a rule matches any transport specific byte, the
	// fragments without one included.
	WholeL4 bool
}

// subspan returns the low byte sub-span of one range for the
// protocol, clamped to the whole byte where the range extends beyond
// the protocol; ok reports whether the range covers the protocol at
// all.
func subspan(r filter.ProtoRange, proto uint8) (LineRange, bool) {
	lo := uint32(r.From >> 8)
	hi := uint32(r.To >> 8)
	if uint32(proto) < lo || uint32(proto) > hi {
		return LineRange{}, false
	}

	from := uint32(0)
	if lo == uint32(proto) {
		from = uint32(r.From & 0xff)
	}
	to := uint32(0xff)
	if hi == uint32(proto) {
		to = uint32(r.To & 0xff)
	}
	return LineRange{From: from, To: to}, true
}

// coversZero reports whether some sub-span for the protocol covers
// the zero subtype: the packets of a protocol without a transport
// specific byte resolve at the zero subtype, so only such a rule
// matches them.
func coversZero(ranges filter.ProtoRanges, proto uint8) bool {
	for _, r := range ranges {
		if interval, ok := subspan(r, proto); ok && interval.From == 0 {
			return true
		}
	}
	return false
}

// intersects reports whether some range covers the protocol.
func intersects(ranges filter.ProtoRanges, proto uint8) bool {
	for _, r := range ranges {
		lo := uint32(r.From >> 8)
		hi := uint32(r.To >> 8)
		if lo <= uint32(proto) && uint32(proto) <= hi {
			return true
		}
	}
	return false
}

// SplitProtoRanges splits the authored protocol ranges of one rule
// into the derived line intervals.
func SplitProtoRanges(ranges filter.ProtoRanges) ProtoSplit {
	split := ProtoSplit{WholeL4: true}
	for _, r := range ranges {
		split.IpProto = append(split.IpProto, LineRange{
			From: uint32(r.From >> 8),
			To:   uint32(r.To >> 8),
		})

		if interval, ok := subspan(r, ipProtoTCP); ok {
			split.TCPFlags = append(split.TCPFlags, interval)
		}
		if interval, ok := subspan(r, ipProtoICMPv4); ok {
			split.ICMPTypes = append(split.ICMPTypes, interval)
		}
		if interval, ok := subspan(r, ipProtoICMPv6); ok {
			split.ICMPTypes = append(split.ICMPTypes, interval)
		}

		if r.From&0xff != 0 || r.To&0xff != 0xff {
			split.WholeL4 = false
		}
	}
	return split
}

// hasFullPorts reports whether the port range set leaves the side
// unconstrained: an empty set, or a single whole range.
func hasFullPorts(ranges filter.PortRanges) bool {
	return len(ranges) == 0 ||
		(len(ranges) > 0 && ranges[0].From == 0 && ranges[0].To == 65535)
}

// PathFlags is the family path membership of one ACL rule: the paths
// the rule's packets resolve through.
type PathFlags struct {
	// Plain takes the rules matching the packets without a transport
	// header: the whole transport byte rules and the fragment
	// constrained ones.
	Plain bool
	// TCP takes the rules that can match an offset zero TCP packet.
	TCP bool
	// UDP takes the rules that can match an offset zero UDP packet.
	UDP bool
	// ICMP takes the rules that can match an ICMP or an ICMPv6
	// packet.
	ICMP bool
}

// RulePathMembership decides the family paths one rule's packets
// resolve through, out of the authored protocol ranges, port ranges
// and fragment constraint.
//
// The plain path takes the rules matching the packets without a
// transport header: the rules whose every range leaves the transport
// byte whole match any byte, absent included, and the fragment
// constrained rules resolve through the fragment suffix. The protocol
// paths take every remaining rule that can match a packet of their
// protocol: the tcp and the icmp paths discriminate through their
// byte leaves, any covering span resolves; the udp path has no leaf,
// a UDP packet resolves at the zero subtype, so only a rule whose UDP
// sub-span covers the zero subtype can match one. The icmp path takes
// only whole port rules: an ICMP packet carries no ports, a port
// constrained ICMP coverage never matched a packet in the former port
// filter either.
func RulePathMembership(
	protos filter.ProtoRanges,
	srcPorts, dstPorts filter.PortRanges,
	fragment filter.Fragment,
) PathFlags {
	split := SplitProtoRanges(protos)
	fullPorts := hasFullPorts(srcPorts) && hasFullPorts(dstPorts)
	fragOnly := fragment == filter.FragmentFrag
	plainOwned := split.WholeL4 && fullPorts

	return PathFlags{
		Plain: split.WholeL4 && (fragOnly || fullPorts),
		TCP:   !fragOnly && intersects(protos, ipProtoTCP) && !plainOwned,
		UDP: !fragOnly && intersects(protos, ipProtoUDP) &&
			coversZero(protos, ipProtoUDP) && !plainOwned,
		ICMP: !fragOnly && fullPorts && !split.WholeL4 &&
			(intersects(protos, ipProtoICMPv4) ||
				intersects(protos, ipProtoICMPv6)),
	}
}
