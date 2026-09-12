package croute

import (
	"fmt"
	"maps"
	"net"
	"net/netip"
	"slices"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
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

// LinkDevice links a device by name and returns its index in the module's
// device table, the index a table object's nexthops name it by.
//
// A name already linked keeps its index. A name the fixed-size table
// entry cannot hold whole is rejected rather than stored truncated, which
// would alias it with its prefix.
func (m *ModuleConfig) LinkDevice(device string) (uint32, error) {
	if device == "" {
		return 0, fmt.Errorf("device name is required")
	}
	if err := ffi.ValidateDeviceName(device); err != nil {
		return 0, fmt.Errorf("device name %q: %w", device, err)
	}
	return m.linkDevice(device)
}

// AddRoute adds a hardware route with MAC addresses, the index of the
// egress device in the linking module's table, and an optional
// per-nexthop dataplane counter name.
//
// An empty counter leaves the nexthop uncounted. The index is stored as
// given, the dataplane drops packets of a nexthop whose index lies past
// the table of the module running it.
func (m *FIBObject) AddRoute(srcAddr net.HardwareAddr, dstAddr net.HardwareAddr, deviceIndex uint32, counter string) (int, error) {
	if len(srcAddr) != 6 {
		return -1, fmt.Errorf("unsupported source MAC address: must be EUI-48")
	}
	if len(dstAddr) != 6 {
		return -1, fmt.Errorf("unsupported destination MAC address: must be EUI-48")
	}
	return m.addRoute([6]byte(dstAddr), [6]byte(srcAddr), deviceIndex, counter)
}

// AddRouteList adds a list of route indices as an ECMP group.
func (m *FIBObject) AddRouteList(routeIndices []uint32) (int, error) {
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
func (m *FIBObject) AddRange(start, end netip.Addr, routeListIdx uint32) error {
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

// ActiveNexthopCounterNames returns the deduplicated, sorted set of
// per-nexthop counter names reachable through the resolved FIB.
//
// The iterator walks the LPM after overlap resolution, so a nexthop fully
// shadowed by a later, overlapping entry is excluded here even though its
// route and counter are still registered in shared memory.
func (m *FIBObject) ActiveNexthopCounterNames() ([]string, error) {
	iter, err := newFIBIter(m)
	if err != nil {
		return nil, fmt.Errorf("failed to create FIB iterator: %w", err)
	}

	return slices.Sorted(maps.Keys(iter.ActiveCounterNames())), nil
}

// Entries reads the published table straight from shared memory.
//
// The error wraps ffi.ErrNotFound when the generation holds no table
// object for the config.
func (m *Snapshot) Entries() ([]FIBEntry, error) {
	iter, err := m.fibIter()
	if err != nil {
		return nil, err
	}
	return iter.Entries(), nil
}
