// Package filterpbconv translates protobuf filter messages into cgo-bound
// filter values.
//
// Keeping the translation in this bridge package lets message-only consumers
// avoid the C toolchain.
package filterpbconv

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/xnetip"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
)

// ToDevices converts protobuf Device messages to filter Devices.
func ToDevices(pb []*filterpb.Device) (filter.Devices, error) {
	out := make(filter.Devices, len(pb))
	for idx := range pb {
		out[idx] = filter.Device{
			Name: pb[idx].Name,
		}
	}

	return out, nil
}

// ToNet4sFromNetworks converts family-typed IPv4 network messages to
// contiguous IPv4 filter networks, enforcing the compiler's mask class.
func ToNet4sFromNetworks(pb []*commonpb.IPv4Network) ([]xnetip.Contiguous[xnetip.Network4], error) {
	out := make([]xnetip.Contiguous[xnetip.Network4], 0, len(pb))

	for idx := range pb {
		net, err := pb[idx].ToNetwork4()
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid IPv4 network at index %d: %v", idx, err)
		}

		typed, ok := xnetip.ContiguousFrom(net)
		if !ok {
			return nil, status.Errorf(codes.InvalidArgument, "network mask must be contiguous at index %d", idx)
		}

		out = append(out, typed)
	}

	return out, nil
}

// ToNet6sFromNetworks converts family-typed IPv6 network messages to
// bi-contiguous IPv6 filter networks, enforcing the compiler's mask class.
func ToNet6sFromNetworks(pb []*commonpb.IPv6Network) ([]xnetip.BiContiguous, error) {
	out := make([]xnetip.BiContiguous, 0, len(pb))

	for idx := range pb {
		net, err := pb[idx].ToNetwork6()
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid IPv6 network at index %d: %v", idx, err)
		}

		typed, ok := xnetip.BiContiguousFrom6(net)
		if !ok {
			return nil, status.Errorf(codes.InvalidArgument, "network mask must be bi-contiguous at index %d", idx)
		}

		out = append(out, typed)
	}

	return out, nil
}

// ToPortRanges converts protobuf PortRange messages to filter PortRanges.
func ToPortRanges(pb []*filterpb.PortRange) (filter.PortRanges, error) {
	out := make(filter.PortRanges, len(pb))

	for idx := range pb {
		if pb[idx].From > 65535 {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"Port 'from' value %d exceeds maximum 65535",
				pb[idx].From,
			)
		}
		if pb[idx].To > 65535 {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"Port 'to' value %d exceeds maximum 65535",
				pb[idx].To,
			)
		}
		if pb[idx].From > pb[idx].To {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"Port 'from' value %d is greater than 'to' value %d",
				pb[idx].From,
				pb[idx].To,
			)
		}

		out[idx] = filter.PortRange{
			From: uint16(pb[idx].From),
			To:   uint16(pb[idx].To),
		}
	}

	return out, nil
}

// ToProtoRanges converts protobuf ProtoRange messages to filter
// ProtoRanges.
func ToProtoRanges(pb []*filterpb.ProtoRange) (filter.ProtoRanges, error) {
	out := make(filter.ProtoRanges, len(pb))

	for idx := range pb {
		if pb[idx].From > 65535 {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"Protocol 'from' value %d exceeds maximum 65535",
				pb[idx].From,
			)
		}
		if pb[idx].To > 65535 {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"Protocol 'to' value %d exceeds maximum 65535",
				pb[idx].To,
			)
		}
		if pb[idx].From > pb[idx].To {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"Protocol 'from' value %d is greater than 'to' value %d",
				pb[idx].From,
				pb[idx].To,
			)
		}

		out[idx] = filter.ProtoRange{
			From: uint16(pb[idx].From),
			To:   uint16(pb[idx].To),
		}
	}

	return out, nil
}

// The IP protocol numbers whose transport header carries the subtype
// byte the structured protocol entries constrain.
const (
	ipProtoICMP   = 1
	ipProtoTCP    = 6
	ipProtoICMPv6 = 58
)

// ToProtocolRanges converts structured protobuf Protocol entries to
// filter ProtoRanges, the packed protocol and subtype encoding the
// classifiers consume.
//
// An entry without specific conditions expands to the whole protocol
// block. A rule matches a packet when either form it carries matches,
// so callers append the result to the ranges decoded from the raw
// field.
func ToProtocolRanges(pb []*filterpb.Protocol) (filter.ProtoRanges, error) {
	out := make(filter.ProtoRanges, 0, len(pb))

	for idx, entry := range pb {
		number := entry.GetNumber()
		if number > 255 {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"Protocol %d number %d exceeds maximum 255",
				idx,
				number,
			)
		}

		specific := false
		if tcp := entry.GetTcp(); tcp != nil {
			ranges, err := toTCPFlagRanges(idx, number, tcp)
			if err != nil {
				return nil, err
			}
			out = append(out, ranges...)
			specific = true
		}

		icmpTypes, err := toIcmpTypeRanges(idx, number, ipProtoICMP, entry.GetIcmpTypes())
		if err != nil {
			return nil, err
		}
		out = append(out, icmpTypes...)

		icmp6Types, err := toIcmpTypeRanges(idx, number, ipProtoICMPv6, entry.GetIcmp6Types())
		if err != nil {
			return nil, err
		}
		out = append(out, icmp6Types...)

		if !specific && len(icmpTypes) == 0 && len(icmp6Types) == 0 {
			out = append(out, filter.NewProtoRange(uint8(number), filter.AnySubtype()))
		}
	}

	return out, nil
}

// toTCPFlagRanges expands one TCP flag mask match into the packed
// ranges of the flag bytes it accepts.
//
// The flag bytes a mask admits form up to 128 single-byte runs, so a
// narrow mask yields many ranges; the classifier compiles them into
// the same line either way.
func toTCPFlagRanges(
	idx int,
	number uint32,
	tcp *filterpb.TcpFlags,
) (filter.ProtoRanges, error) {
	if number != ipProtoTCP {
		return nil, status.Errorf(
			codes.InvalidArgument,
			"Protocol %d: TCP flags require protocol number %d, got %d",
			idx,
			ipProtoTCP,
			number,
		)
	}

	flags := tcp.GetFlags()
	mask := tcp.GetMask()
	if flags > 0xff || mask > 0xff {
		return nil, status.Errorf(
			codes.InvalidArgument,
			"Protocol %d: TCP flags 0x%x and mask 0x%x exceed one byte",
			idx,
			flags,
			mask,
		)
	}
	if flags&^mask != 0 {
		return nil, status.Errorf(
			codes.InvalidArgument,
			"Protocol %d: TCP flags 0x%x carry bits outside mask 0x%x",
			idx,
			flags,
			mask,
		)
	}

	var out filter.ProtoRanges
	runStart := -1
	for value := 0; value <= 256; value++ {
		if value < 256 && uint8(value)&uint8(mask) == uint8(flags) {
			if runStart < 0 {
				runStart = value
			}
			continue
		}
		if runStart >= 0 {
			out = append(out, filter.NewProtoRange(
				ipProtoTCP,
				filter.RangeSubtype(uint8(runStart), uint8(value-1)),
			))
			runStart = -1
		}
	}

	return out, nil
}

// toIcmpTypeRanges expands the ICMP or ICMPv6 type ranges of one
// entry into their packed form; an empty list leaves the protocol
// unconstrained.
func toIcmpTypeRanges(
	idx int,
	number uint32,
	expectedProto uint32,
	pb []*filterpb.IcmpTypeRange,
) (filter.ProtoRanges, error) {
	out := make(filter.ProtoRanges, 0, len(pb))

	for _, typeRange := range pb {
		if number != expectedProto {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"Protocol %d: ICMP types require protocol number %d, got %d",
				idx,
				expectedProto,
				number,
			)
		}
		if typeRange.GetFrom() > 255 || typeRange.GetTo() > 255 {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"Protocol %d: ICMP type value %d exceeds maximum 255",
				idx,
				max(typeRange.GetFrom(), typeRange.GetTo()),
			)
		}
		if typeRange.GetFrom() > typeRange.GetTo() {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"Protocol %d: ICMP type 'from' value %d is greater than 'to' value %d",
				idx,
				typeRange.GetFrom(),
				typeRange.GetTo(),
			)
		}

		out = append(out, filter.NewProtoRange(
			uint8(number),
			filter.RangeSubtype(
				uint8(typeRange.GetFrom()),
				uint8(typeRange.GetTo()),
			),
		))
	}

	return out, nil
}

// ToVlanRanges converts protobuf VlanRange messages to filter VlanRanges.
func ToVlanRanges(pb []*filterpb.VlanRange) (filter.VlanRanges, error) {
	out := make(filter.VlanRanges, len(pb))

	for idx := range pb {
		if pb[idx].From > 4095 {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"VLAN 'from' value %d exceeds maximum 4095",
				pb[idx].From,
			)
		}
		if pb[idx].To > 4095 {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"VLAN 'to' value %d exceeds maximum 4095",
				pb[idx].To,
			)
		}

		out[idx] = filter.VlanRange{
			From: uint16(pb[idx].From),
			To:   uint16(pb[idx].To),
		}
	}

	return out, nil
}

// ToFragment converts protobuf Fragment message to filter Fragment.
func ToFragment(pb *filterpb.Fragment) (filter.Fragment, error) {
	if pb == nil {
		return filter.FragmentAny, nil
	}
	switch pb.Kind {
	case filterpb.FragmentKind_Any:
		return filter.FragmentAny, nil
	case filterpb.FragmentKind_None:
		return filter.FragmentNone, nil
	case filterpb.FragmentKind_Frag:
		return filter.FragmentFrag, nil
	}

	return filter.FragmentAny, status.Errorf(
		codes.InvalidArgument,
		"Unknown Fragment Kind code %d",
		pb.Kind,
	)
}
