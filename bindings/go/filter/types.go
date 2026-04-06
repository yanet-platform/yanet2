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
