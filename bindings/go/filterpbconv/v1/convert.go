// Package filterpbconv translates protobuf filter messages into cgo-bound
// filter values.
//
// Keeping the translation in this bridge package lets message-only consumers
// avoid the C toolchain.
package filterpbconv

import (
	"net/netip"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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

// ToNet4s converts protobuf IPNet messages to filter IPNets, keeping only IPv4
// entries.
func ToNet4s(pb []*filterpb.IPNet) (filter.IPNets, error) {
	out := make(filter.IPNets, 0, len(pb))

	for idx := range pb {
		net, err := ToIPNet(pb[idx])
		if err != nil {
			return nil, err
		}
		if !net.Addr.Is4() {
			continue
		}

		out = append(out, net)
	}

	return out, nil
}

// ToNet6s converts protobuf IPNet messages to filter IPNets, keeping only IPv6
// entries.
func ToNet6s(pb []*filterpb.IPNet) (filter.IPNets, error) {
	out := make(filter.IPNets, 0, len(pb))

	for idx := range pb {
		net, err := ToIPNet(pb[idx])
		if err != nil {
			return nil, err
		}
		if !net.Addr.Is6() {
			continue
		}

		out = append(out, net)
	}

	return out, nil
}

// ToIPNet validates the address family and mask shape before returning a
// filter network value.
func ToIPNet(pb *filterpb.IPNet) (filter.IPNet, error) {
	addr, ok := netip.AddrFromSlice(pb.Addr)
	if !ok {
		return filter.IPNet{}, status.Error(
			codes.InvalidArgument,
			"invalid network address",
		)
	}
	mask, ok := netip.AddrFromSlice(pb.Mask)
	if !ok {
		return filter.IPNet{}, status.Error(
			codes.InvalidArgument,
			"invalid network mask",
		)
	}

	if addr.Is4() != mask.Is4() {
		return filter.IPNet{}, status.Error(
			codes.InvalidArgument,
			"network address and mask must be the same IP family",
		)
	}

	net := filter.IPNet{
		Addr: addr,
		Mask: mask,
	}
	if !net.MaskIsValid() {
		if mask.Is4() {
			return filter.IPNet{}, status.Error(
				codes.InvalidArgument,
				"network mask must be contiguous",
			)
		}
		return filter.IPNet{}, status.Error(
			codes.InvalidArgument,
			"network mask must be bi-contiguous",
		)
	}

	return net, nil
}

// ToNet4sFromContiguous converts family-typed contiguous IPv4 network
// messages to filter IPNets.
func ToNet4sFromContiguous(pb []*commonpb.ContiguousIPv4Network) (filter.IPNets, error) {
	prefixes, err := commonpb.PrefixesFromNetworks(pb)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid IPv4 network: %v", err)
	}

	return filter.Net4sFromPrefixes(prefixes)
}

// ToNet6sFromBiContiguous converts family-typed bi-contiguous IPv6 network
// messages to filter IPNets.
func ToNet6sFromBiContiguous(pb []*commonpb.BiContiguousIPv6Network) (filter.IPNets, error) {
	out := make(filter.IPNets, 0, len(pb))

	for idx := range pb {
		net, err := pb[idx].ToBiContiguous()
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid IPv6 network at index %d: %v", idx, err)
		}

		out = append(out, filter.IPNet{
			Addr: net.Network().Addr(),
			Mask: net.Network().Mask(),
		})
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
