package ffi

import (
	"fmt"
	"net"
	"net/netip"

	"github.com/yanet-platform/yanet2/common/go/xnetip"
)

const (
	AddressFamilyIPv4 = 4
	AddressFamilyIPv6 = 6
)

// FIBNexthop represents a single ECMP nexthop in the FIB.
type FIBNexthop struct {
	DstMAC net.HardwareAddr
	SrcMAC net.HardwareAddr
	Device string
}

// FIBEntry represents a single FIB prefix with its nexthops.
type FIBEntry struct {
	AddressFamily uint8
	PrefixFrom    netip.Addr
	PrefixTo      netip.Addr
	Nexthops      []FIBNexthop
}

// AddRoute adds a hardware route with MAC addresses and egress device.
func (m *ModuleConfig) AddRoute(srcAddr net.HardwareAddr, dstAddr net.HardwareAddr, device string) (int, error) {
	if len(srcAddr) != 6 {
		return -1, fmt.Errorf("unsupported source MAC address: must be EUI-48")
	}
	if len(dstAddr) != 6 {
		return -1, fmt.Errorf("unsupported destination MAC address: must be EUI-48")
	}
	if device == "" {
		return -1, fmt.Errorf("device name is required")
	}

	return m.addRoute([6]byte(dstAddr), [6]byte(srcAddr), device)
}

// AddRouteList adds a list of route indices as an ECMP group.
func (m *ModuleConfig) AddRouteList(routeIndices []uint32) (int, error) {
	if len(routeIndices) == 0 {
		return -1, fmt.Errorf("routeIndices must not be empty")
	}

	return m.addRouteList(routeIndices)
}

// AddPrefix adds a prefix to the LPM table, pointing at the given route list.
func (m *ModuleConfig) AddPrefix(prefix netip.Prefix, routeListIdx uint32) error {
	addrStart := prefix.Addr()
	addrEnd := xnetip.LastAddr(prefix)

	if addrStart.Is4() {
		return m.addPrefixV4(addrStart.As4(), addrEnd.As4(), routeListIdx)
	}
	if addrStart.Is6() {
		return m.addPrefixV6(addrStart.As16(), addrEnd.As16(), routeListIdx)
	}

	return fmt.Errorf("unsupported prefix: must be either IPv4 or IPv6")
}

// DumpFIB reads the Forwarding Information Base from shared memory using a
// zero-copy iterator.
func (m *ModuleConfig) DumpFIB() ([]FIBEntry, error) {
	iter, err := newFIBIter(m)
	if err != nil {
		return nil, fmt.Errorf("failed to create FIB iterator: %w", err)
	}
	defer iter.destroy()

	var entries []FIBEntry

	for iter.next() {
		af := iter.addressFamily()

		from := iter.prefixFrom()
		to := iter.prefixTo()

		var prefixFrom, prefixTo netip.Addr
		switch af {
		case AddressFamilyIPv4:
			prefixFrom = netip.AddrFrom4(*(*[4]byte)(from))
			prefixTo = netip.AddrFrom4(*(*[4]byte)(to))
		case AddressFamilyIPv6:
			prefixFrom = netip.AddrFrom16(*(*[16]byte)(from))
			prefixTo = netip.AddrFrom16(*(*[16]byte)(to))
		default:
			continue
		}

		nhCount := iter.nexthopCount()
		nexthops := make([]FIBNexthop, nhCount)

		for idx := range nhCount {
			dstMAC := iter.nexthopDstMAC(idx)
			srcMAC := iter.nexthopSrcMAC(idx)

			nexthops[idx] = FIBNexthop{
				DstMAC: net.HardwareAddr(dstMAC[:]),
				SrcMAC: net.HardwareAddr(srcMAC[:]),
				Device: iter.nexthopDeviceName(idx),
			}
		}

		entries = append(entries, FIBEntry{
			AddressFamily: af,
			PrefixFrom:    prefixFrom,
			PrefixTo:      prefixTo,
			Nexthops:      nexthops,
		})
	}

	return entries, nil
}
