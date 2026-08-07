package croute

import (
	"fmt"
	"maps"
	"net"
	"net/netip"
	"slices"
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

	// Counter is the per-nexthop dataplane counter name.
	//
	// Empty means no name was read, ordinarily because the nexthop isn't
	// individually counted.
	Counter string
}

// FIBEntry represents a single FIB prefix with its nexthops.
type FIBEntry struct {
	AddressFamily uint8
	PrefixFrom    netip.Addr
	PrefixTo      netip.Addr
	Nexthops      []FIBNexthop
}

// AddRoute adds a hardware route with MAC addresses, egress device, and an
// optional per-nexthop dataplane counter name.
//
// An empty counter leaves the nexthop uncounted.
func (m *ModuleConfig) AddRoute(srcAddr net.HardwareAddr, dstAddr net.HardwareAddr, device string, counter string) (int, error) {
	if len(srcAddr) != 6 {
		return -1, fmt.Errorf("unsupported source MAC address: must be EUI-48")
	}
	if len(dstAddr) != 6 {
		return -1, fmt.Errorf("unsupported destination MAC address: must be EUI-48")
	}
	if device == "" {
		return -1, fmt.Errorf("device name is required")
	}

	return m.addRoute([6]byte(dstAddr), [6]byte(srcAddr), device, counter)
}

// AddRouteList adds a list of route indices as an ECMP group.
func (m *ModuleConfig) AddRouteList(routeIndices []uint32) (int, error) {
	if len(routeIndices) == 0 {
		return -1, fmt.Errorf("routeIndices must not be empty")
	}

	return m.addRouteList(routeIndices)
}

// AddRange adds a contiguous address range to the LPM table, pointing at
// the given route list.
//
// start and end must both be valid, belong to the same address family, and
// satisfy start <= end. An IPv4-mapped IPv6 address is treated as IPv6,
// since Is4 returns false for it.
func (m *ModuleConfig) AddRange(start, end netip.Addr, routeListIdx uint32) error {
	if !start.IsValid() || !end.IsValid() {
		return fmt.Errorf("start and end must both be valid addresses")
	}
	if start.Is4() != end.Is4() {
		return fmt.Errorf("address family mismatch: start and end must be the same address family")
	}
	if start.Compare(end) > 0 {
		return fmt.Errorf("invalid range: start %s is after end %s", start, end)
	}
	if start.Is4() {
		return m.addPrefixV4(start.As4(), end.As4(), routeListIdx)
	}
	return m.addPrefixV6(start.As16(), end.As16(), routeListIdx)
}

// DumpFIB reads the Forwarding Information Base from shared memory using a
// zero-copy iterator.
func (m *ModuleConfig) DumpFIB() ([]FIBEntry, error) {
	iter, err := newFIBIter(m)
	if err != nil {
		return nil, fmt.Errorf("failed to create FIB iterator: %w", err)
	}

	return iter.Entries(), nil
}

// ActiveNexthopCounterNames returns the deduplicated, sorted set of
// per-nexthop counter names reachable through the resolved FIB.
//
// The iterator walks the LPM after overlap resolution, so a nexthop fully
// shadowed by a later, overlapping entry is excluded here even though its
// route and counter are still registered in shared memory.
func (m *ModuleConfig) ActiveNexthopCounterNames() ([]string, error) {
	iter, err := newFIBIter(m)
	if err != nil {
		return nil, fmt.Errorf("failed to create FIB iterator: %w", err)
	}

	return slices.Sorted(maps.Keys(iter.ActiveCounterNames())), nil
}
