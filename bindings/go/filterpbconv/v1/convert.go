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
