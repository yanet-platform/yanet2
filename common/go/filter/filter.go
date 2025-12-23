package filter

import (
	"net/netip"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/filterpb"
)

type Device struct {
	Name string
}

type Devices []Device

type VlanRange struct {
	From uint16
	To   uint16
}

type VlanRanges []VlanRange

type IPNet4 struct {
	Addr netip.Addr
	Mask netip.Addr
}

type IPNet4s []IPNet4

type IPNet6 struct {
	Addr netip.Addr
	Mask netip.Addr
}

type IPNet6s []IPNet6

type ProtoRange struct {
	From uint16
	To   uint16
}

type ProtoRanges []ProtoRange

type PortRange struct {
	From uint16
	To   uint16
}

type PortRanges []PortRange

func MakeDevices(devices []*filterpb.Device) ([]Device, error) {
	result := make([]Device, len(devices))

	for idx := range devices {
		result[idx] = Device{
			Name: devices[idx].Name,
		}
	}

	return result, nil
}

func MakeVlanRanges(vlanRanges []*filterpb.VlanRange) ([]VlanRange, error) {
	result := make([]VlanRange, len(vlanRanges))

	for idx := range vlanRanges {
		if vlanRanges[idx].From > 4095 {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"VLAN 'from' value %d exceeds maximum 4095",
				vlanRanges[idx].From,
			)
		}
		if vlanRanges[idx].To > 4095 {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"VLAN 'to' value %d exceeds maximum 4095",
				vlanRanges[idx].To,
			)
		}
		result[idx] = VlanRange{
			From: uint16(vlanRanges[idx].From),
			To:   uint16(vlanRanges[idx].To),
		}
	}

	return result, nil
}

func MakeIPNet4s(ipNets []*filterpb.IPNet) ([]IPNet4, error) {
	result := make([]IPNet4, 0, len(ipNets))

	for idx := range ipNets {
		if (len(ipNets[idx].Addr) != 4 && len(ipNets[idx].Addr) != 16) ||
			len(ipNets[idx].Addr) != len(ipNets[idx].Mask) {
			return nil, status.Error(
				codes.InvalidArgument,
				"invalid network address length")
		}

		if len(ipNets[idx].Addr) != 4 {
			continue
		}
		addr, _ := netip.AddrFromSlice(ipNets[idx].Addr)
		mask, _ := netip.AddrFromSlice(ipNets[idx].Mask)
		result = append(result, IPNet4{
			Addr: addr,
			Mask: mask,
		})
	}

	return result, nil
}

func MakeIPNet6s(ipNets []*filterpb.IPNet) ([]IPNet6, error) {
	result := make([]IPNet6, 0, len(ipNets))

	for idx := range ipNets {
		if (len(ipNets[idx].Addr) != 4 && len(ipNets[idx].Addr) != 16) ||
			len(ipNets[idx].Addr) != len(ipNets[idx].Mask) {
			return nil, status.Error(
				codes.InvalidArgument,
				"invalid network address length")
		}

		if len(ipNets[idx].Addr) != 16 {
			continue
		}
		addr, _ := netip.AddrFromSlice(ipNets[idx].Addr)
		mask, _ := netip.AddrFromSlice(ipNets[idx].Mask)
		result = append(result, IPNet6{
			Addr: addr,
			Mask: mask,
		})
	}

	return result, nil
}

func MakeProtoRanges(protoRanges []*filterpb.ProtoRange) ([]ProtoRange, error) {
	result := make([]ProtoRange, len(protoRanges))

	for idx := range protoRanges {
		// Protocol range is stored as uint16 in the code
		if protoRanges[idx].From > 65535 {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"Protocol 'from' value %d exceeds maximum 65535",
				protoRanges[idx].From,
			)
		}
		if protoRanges[idx].To > 65535 {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"Protocol 'to' value %d exceeds maximum 65535",
				protoRanges[idx].To,
			)
		}
		if protoRanges[idx].From > protoRanges[idx].To {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"Protocol 'from' value %d is greater than 'to' value %d",
				protoRanges[idx].From,
				protoRanges[idx].To,
			)
		}
		result[idx] = ProtoRange{
			From: uint16(protoRanges[idx].From),
			To:   uint16(protoRanges[idx].To),
		}
	}

	return result, nil
}

func MakePortRanges(portRanges []*filterpb.PortRange) ([]PortRange, error) {
	result := make([]PortRange, len(portRanges))

	for idx := range portRanges {
		// Port is 16 bits, so valid range is 0-65535
		if portRanges[idx].From > 65535 {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"Port 'from' value %d exceeds maximum 65535",
				portRanges[idx].From,
			)
		}
		if portRanges[idx].To > 65535 {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"Port 'to' value %d exceeds maximum 65535",
				portRanges[idx].To,
			)
		}
		if portRanges[idx].From > portRanges[idx].To {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"Port 'from' value %d is greater than 'to' value %d",
				portRanges[idx].From,
				portRanges[idx].To,
			)
		}

		result[idx] = PortRange{
			From: uint16(portRanges[idx].From),
			To:   uint16(portRanges[idx].To),
		}
	}

	return result, nil
}
