package filterpb

import (
	"net/netip"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
)

// ToDevices converts protobuf Device messages to filter Devices.
func ToDevices(pb []*Device) (filter.Devices, error) {
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
func ToNet4s(pb []*IPNet) (filter.IPNets, error) {
	out := make(filter.IPNets, 0, len(pb))

	for idx := range pb {
		if len(pb[idx].Addr) != 4 {
			continue
		}

		net, err := ToIPNet(pb[idx])
		if err != nil {
			return nil, err
		}

		out = append(out, net)
	}

	return out, nil
}

// ToNet6s converts protobuf IPNet messages to filter IPNets, keeping only IPv6
// entries.
func ToNet6s(pb []*IPNet) (filter.IPNets, error) {
	out := make(filter.IPNets, 0, len(pb))

	for idx := range pb {
		if len(pb[idx].Addr) != 16 {
			continue
		}

		net, err := ToIPNet(pb[idx])
		if err != nil {
			return nil, err
		}

		out = append(out, net)
	}

	return out, nil
}

func ToIPNet(pb *IPNet) (filter.IPNet, error) {
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

	return filter.IPNet{
		Addr: addr,
		Mask: mask,
	}, nil
}

// ToNet4sFromPrefixes converts protobuf IPPrefix messages to filter
// IPNets, keeping only IPv4 entries.
func ToNet4sFromPrefixes(pb []*IPPrefix) (filter.IPNets, error) {
	prefixes := make([]netip.Prefix, 0, len(pb))

	for _, p := range pb {
		if len(p.Addr) != 4 && len(p.Addr) != 16 {
			return nil, status.Error(
				codes.InvalidArgument,
				"invalid network address length")
		}

		if len(p.Addr) != 4 {
			continue
		}

		addr, _ := netip.AddrFromSlice(p.Addr)
		prefixes = append(prefixes, netip.PrefixFrom(addr, int(p.Length)))
	}

	return filter.Net4sFromPrefixes(prefixes)
}

// ToNet6sFromPrefixes converts protobuf IPPrefix messages to filter
// IPNets, keeping only IPv6 entries.
func ToNet6sFromPrefixes(pb []*IPPrefix) (filter.IPNets, error) {
	prefixes := make([]netip.Prefix, 0, len(pb))

	for _, p := range pb {
		if len(p.Addr) != 4 && len(p.Addr) != 16 {
			return nil, status.Error(
				codes.InvalidArgument,
				"invalid network address length")
		}

		if len(p.Addr) != 16 {
			continue
		}

		addr, _ := netip.AddrFromSlice(p.Addr)
		prefixes = append(prefixes, netip.PrefixFrom(addr, int(p.Length)))
	}

	return filter.Net6sFromPrefixes(prefixes)
}

// ToPortRanges converts protobuf PortRange messages to filter PortRanges.
func ToPortRanges(pb []*PortRange) (filter.PortRanges, error) {
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
func ToProtoRanges(pb []*ProtoRange) (filter.ProtoRanges, error) {
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
func ToVlanRanges(pb []*VlanRange) (filter.VlanRanges, error) {
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
