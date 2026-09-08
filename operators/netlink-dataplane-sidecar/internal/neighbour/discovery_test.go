package neighbour_test

import (
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	vnetlink "github.com/vishvananda/netlink"

	"github.com/yanet-platform/yanet2/modules/route/controlplane/hwroute"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
	netreconcile "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netlink"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
)

type fakeBackend struct {
	links          []vnetlink.Link
	linkError      error
	neighbours     []vnetlink.Neigh
	neighbourError error
}

type changingBackend struct {
	linkSnapshots [][]vnetlink.Link
	linkCalls     int
	neighbours    []vnetlink.Neigh
}

func (m *changingBackend) LinkList() ([]vnetlink.Link, error) {
	snapshot := m.linkSnapshots[min(m.linkCalls, len(m.linkSnapshots)-1)]
	m.linkCalls++
	return snapshot, nil
}

func (m *changingBackend) NeighList(int, int) ([]vnetlink.Neigh, error) {
	return m.neighbours, nil
}

// LinkList returns the configured complete or partial link dump.
func (m fakeBackend) LinkList() ([]vnetlink.Link, error) {
	return m.links, m.linkError
}

// NeighList returns the configured complete or partial neighbour dump.
func (m fakeBackend) NeighList(linkIndex, family int) ([]vnetlink.Neigh, error) {
	return m.neighbours, m.neighbourError
}

// Test_Discover_ManagedIsolationAndLinkMapping verifies that only neighbours
// on state-owned links are returned with mapped or fallback device names.
func Test_Discover_ManagedIsolationAndLinkMapping(t *testing.T) {
	state := netplan.State{Links: []netplan.Link{
		{Name: "kni0"},
		{Name: "tenant.100", Parent: "kni0", VLANID: 100},
	}}
	backend := fakeBackend{
		links: []vnetlink.Link{
			testLink(3, "management0", 3),
			testVLAN(2, "tenant.100", 1, 100, 2),
			testLink(4, "kni9", 4),
			testLink(1, "kni0", 1),
		},
		neighbours: []vnetlink.Neigh{
			testKernelNeighbour(3, "192.0.2.30", 30, vnetlink.NUD_PERMANENT),
			testKernelNeighbour(2, "192.0.2.20", 20, vnetlink.NUD_STALE),
			testKernelNeighbour(4, "192.0.2.40", 40, vnetlink.NUD_DELAY),
			testKernelNeighbour(1, "192.0.2.10", 10, vnetlink.NUD_REACHABLE),
		},
	}

	entries, err := neighbour.Discover(
		backend,
		state,
		map[string]string{"tenant.100": "dataplane-vlan"},
	)
	require.NoError(t, err)
	require.Equal(t, []neighbour.Entry{
		{
			NextHop: netip.MustParseAddr("192.0.2.10"),
			HardwareRoute: hwroute.HardwareRoute{
				SourceMAC:      testMACArray(1),
				DestinationMAC: testMACArray(10),
				Device:         "kni0",
			},
			State: neighbour.NeighbourState(vnetlink.NUD_REACHABLE),
		},
		{
			NextHop: netip.MustParseAddr("192.0.2.20"),
			HardwareRoute: hwroute.HardwareRoute{
				SourceMAC:      testMACArray(2),
				DestinationMAC: testMACArray(20),
				Device:         "dataplane-vlan",
			},
			State: neighbour.NeighbourState(vnetlink.NUD_STALE),
		},
	}, entries)
}

// Test_Discover_RejectsMappedAndFallbackDeviceCollision verifies that a mapped
// name cannot alias another managed link's unmapped logical device.
func Test_Discover_RejectsMappedAndFallbackDeviceCollision(t *testing.T) {
	entries, err := neighbour.Discover(
		fakeBackend{},
		netplan.State{Links: []netplan.Link{{Name: "kni0"}, {Name: "kni1"}}},
		map[string]string{"kni0": "kni1"},
	)

	require.ErrorContains(t, err, `managed links "kni0" and "kni1" map to duplicate logical device "kni1"`)
	require.Nil(t, entries)
}

// Test_Discover_RejectsMappingForUnmanagedLink verifies that mappings outside
// the managed topology invalidate discovery rather than being silently ignored.
func Test_Discover_RejectsMappingForUnmanagedLink(t *testing.T) {
	entries, err := neighbour.Discover(
		fakeBackend{},
		netplan.State{Links: []netplan.Link{{Name: "kni0"}}},
		map[string]string{"kni1": "logical1"},
	)

	require.ErrorContains(t, err, `link_map entry "kni1" is not a managed netplan link`)
	require.Nil(t, entries)
}

// Test_Discover_SkipsMalformedAddresses verifies that malformed IP and MAC
// data is omitted without invalidating otherwise complete kernel dumps.
func Test_Discover_SkipsMalformedAddresses(t *testing.T) {
	state := netplan.State{Links: []netplan.Link{{Name: "kni0"}}}
	backend := fakeBackend{
		links: []vnetlink.Link{
			testLink(1, "kni0", 1),
			&vnetlink.Device{LinkAttrs: vnetlink.LinkAttrs{
				Index:        2,
				Name:         "kni1",
				HardwareAddr: net.HardwareAddr{0x02, 0x01},
			}},
			&vnetlink.Device{LinkAttrs: vnetlink.LinkAttrs{
				Index:        3,
				Name:         "kni2",
				HardwareAddr: make(net.HardwareAddr, 6),
			}},
		},
		neighbours: []vnetlink.Neigh{
			testKernelNeighbour(1, "192.0.2.1", 1, vnetlink.NUD_REACHABLE),
			{
				LinkIndex:    1,
				IP:           net.IP{192, 0, 2},
				HardwareAddr: testMAC(2),
				State:        vnetlink.NUD_REACHABLE,
			},
			{
				LinkIndex:    1,
				IP:           net.ParseIP("192.0.2.3").To4(),
				HardwareAddr: net.HardwareAddr{0x02, 0x03},
				State:        vnetlink.NUD_REACHABLE,
			},
			{
				LinkIndex:    1,
				IP:           net.ParseIP("192.0.2.4").To4(),
				HardwareAddr: make(net.HardwareAddr, 6),
				State:        vnetlink.NUD_REACHABLE,
			},
			testKernelNeighbour(2, "192.0.2.5", 5, vnetlink.NUD_REACHABLE),
			testKernelNeighbour(3, "192.0.2.6", 6, vnetlink.NUD_REACHABLE),
		},
	}

	entries, err := neighbour.Discover(backend, state, nil)
	require.NoError(t, err)
	require.Equal(t, []neighbour.Entry{{
		NextHop: netip.MustParseAddr("192.0.2.1"),
		HardwareRoute: hwroute.HardwareRoute{
			SourceMAC:      testMACArray(1),
			DestinationMAC: testMACArray(1),
			Device:         "kni0",
		},
		State: neighbour.NeighbourState(vnetlink.NUD_REACHABLE),
	}}, entries)
}

// Test_Discover_RejectsMissingOrInvalidManagedLinks verifies that absent,
// duplicate, or unusable managed links invalidate the entire neighbour snapshot.
func Test_Discover_RejectsMissingOrInvalidManagedLinks(t *testing.T) {
	tests := []struct {
		name          string
		links         []vnetlink.Link
		errorContains string
	}{
		{
			name:          "missing",
			links:         []vnetlink.Link{testLink(2, "management0", 2)},
			errorContains: `managed link "kni0" is missing`,
		},
		{
			name: "invalid index",
			links: []vnetlink.Link{&vnetlink.Device{LinkAttrs: vnetlink.LinkAttrs{
				Name:         "kni0",
				Index:        0,
				HardwareAddr: testMAC(1),
			}}},
			errorContains: "invalid index",
		},
		{
			name: "invalid source MAC",
			links: []vnetlink.Link{&vnetlink.Device{LinkAttrs: vnetlink.LinkAttrs{
				Name:         "kni0",
				Index:        1,
				HardwareAddr: make(net.HardwareAddr, 6),
			}}},
			errorContains: "unusable hardware address",
		},
		{
			name: "duplicate",
			links: []vnetlink.Link{
				testLink(1, "kni0", 1),
				testLink(2, "kni0", 2),
			},
			errorContains: "appears more than once",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entries, err := neighbour.Discover(
				fakeBackend{links: test.links},
				netplan.State{Links: []netplan.Link{{Name: "kni0"}}},
				nil,
			)

			require.ErrorContains(t, err, test.errorContains)
			require.Nil(t, entries)
		})
	}
}

// Test_Discover_RejectsInvalidManagedVLANIdentity verifies that a VLAN's type,
// owner, tag, protocol, and parent must match before publishing neighbours.
func Test_Discover_RejectsInvalidManagedVLANIdentity(t *testing.T) {
	tests := []struct {
		name          string
		link          vnetlink.Link
		errorContains string
	}{
		{
			name:          "wrong type",
			link:          testLink(2, "tenant.100", 2),
			errorContains: "is not a VLAN",
		},
		{
			name: "foreign alias",
			link: &vnetlink.Vlan{
				LinkAttrs: vnetlink.LinkAttrs{
					Index:        2,
					Name:         "tenant.100",
					ParentIndex:  1,
					Alias:        "foreign-owner",
					HardwareAddr: testMAC(2),
				},
				VlanId: 100,
			},
			errorContains: "ownership alias",
		},
		{
			name:          "wrong VLAN ID",
			link:          testVLAN(2, "tenant.100", 1, 200, 2),
			errorContains: "has ID 200, want 100",
		},
		{
			name: "wrong VLAN protocol",
			link: &vnetlink.Vlan{
				LinkAttrs: vnetlink.LinkAttrs{
					Index:        2,
					Name:         "tenant.100",
					ParentIndex:  1,
					Alias:        netreconcile.ManagedAlias,
					HardwareAddr: testMAC(2),
				},
				VlanId:       100,
				VlanProtocol: vnetlink.VLAN_PROTOCOL_8021AD,
			},
			errorContains: "has protocol 802.1ad, want 802.1q",
		},
		{
			name:          "wrong parent",
			link:          testVLAN(2, "tenant.100", 99, 100, 2),
			errorContains: "invalid parent",
		},
	}
	state := netplan.State{Links: []netplan.Link{
		{Name: "kni0"},
		{Name: "tenant.100", Parent: "kni0", VLANID: 100},
	}}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entries, err := neighbour.Discover(fakeBackend{
				links: []vnetlink.Link{testLink(1, "kni0", 1), test.link},
			}, state, nil)

			require.ErrorContains(t, err, test.errorContains)
			require.Nil(t, entries)
		})
	}
}

// Test_Discover_FiltersNUDStates verifies that retained MAC addresses do not
// make unresolved or failed neighbours publishable while usable states remain.
func Test_Discover_FiltersNUDStates(t *testing.T) {
	tests := []struct {
		name   string
		state  int
		usable bool
	}{
		{name: "none", state: vnetlink.NUD_NONE},
		{name: "incomplete", state: vnetlink.NUD_INCOMPLETE},
		{name: "failed", state: vnetlink.NUD_FAILED},
		{name: "reachable", state: vnetlink.NUD_REACHABLE, usable: true},
		{name: "stale", state: vnetlink.NUD_STALE, usable: true},
		{name: "delay", state: vnetlink.NUD_DELAY, usable: true},
		{name: "probe", state: vnetlink.NUD_PROBE, usable: true},
		{name: "noarp", state: vnetlink.NUD_NOARP, usable: true},
		{name: "permanent", state: vnetlink.NUD_PERMANENT, usable: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entries, err := neighbour.Discover(fakeBackend{
				links: []vnetlink.Link{testLink(1, "kni0", 1)},
				neighbours: []vnetlink.Neigh{
					testKernelNeighbour(1, "192.0.2.1", 2, test.state),
				},
			}, netplan.State{Links: []netplan.Link{{Name: "kni0"}}}, nil)
			require.NoError(t, err)
			if !test.usable {
				require.Empty(t, entries)
				return
			}
			require.Equal(t, []neighbour.Entry{{
				NextHop: netip.MustParseAddr("192.0.2.1"),
				HardwareRoute: hwroute.HardwareRoute{
					SourceMAC:      testMACArray(1),
					DestinationMAC: testMACArray(2),
					Device:         "kni0",
				},
				State: neighbour.NeighbourState(test.state),
			}}, entries)
		})
	}
}

// Test_Discover_DumpErrorsInvalidateSnapshot verifies that interrupted dumps
// never expose the partial records returned alongside the backend error.
func Test_Discover_DumpErrorsInvalidateSnapshot(t *testing.T) {
	partialLink := testLink(1, "kni0", 1)
	partialNeighbour := testKernelNeighbour(
		1,
		"192.0.2.1",
		1,
		vnetlink.NUD_REACHABLE,
	)
	tests := []struct {
		name    string
		backend fakeBackend
	}{
		{
			name: "interrupted link dump",
			backend: fakeBackend{
				links:     []vnetlink.Link{partialLink},
				linkError: vnetlink.ErrDumpInterrupted,
			},
		},
		{
			name: "interrupted neighbour dump",
			backend: fakeBackend{
				links:          []vnetlink.Link{partialLink},
				neighbours:     []vnetlink.Neigh{partialNeighbour},
				neighbourError: vnetlink.ErrDumpInterrupted,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entries, err := neighbour.Discover(
				test.backend,
				netplan.State{Links: []netplan.Link{{Name: "kni0"}}},
				nil,
			)
			require.ErrorIs(t, err, vnetlink.ErrDumpInterrupted)
			require.Nil(t, entries)
		})
	}
}

// Test_Discover_RejectsLinkRecreationDuringNeighbourDump verifies that a link
// replaced between dumps cannot publish neighbours tied to its old identity.
func Test_Discover_RejectsLinkRecreationDuringNeighbourDump(t *testing.T) {
	backend := &changingBackend{
		linkSnapshots: [][]vnetlink.Link{
			{testLink(1, "kni0", 1)},
			{testLink(2, "kni0", 2)},
		},
		neighbours: []vnetlink.Neigh{
			testKernelNeighbour(1, "192.0.2.1", 3, vnetlink.NUD_REACHABLE),
		},
	}

	entries, err := neighbour.Discover(
		backend,
		netplan.State{Links: []netplan.Link{{Name: "kni0"}}},
		nil,
	)
	require.ErrorContains(t, err, "managed links changed during dump")
	require.Nil(t, entries)
}

// Test_Discover_DeterministicOrdering verifies that dump order and exact
// duplicate records do not alter the ordered desired snapshot.
func Test_Discover_DeterministicOrdering(t *testing.T) {
	links := []vnetlink.Link{
		testLink(1, "kni0", 1),
		testVLAN(2, "tenant.100", 1, 100, 2),
	}
	firstNeighbour := testKernelNeighbour(1, "2001:db8::1", 1, vnetlink.NUD_STALE)
	secondNeighbour := testKernelNeighbour(2, "192.0.2.20", 2, vnetlink.NUD_REACHABLE)
	thirdNeighbour := testKernelNeighbour(1, "192.0.2.3", 3, vnetlink.NUD_DELAY)
	state := netplan.State{Links: []netplan.Link{
		{Name: "kni0"},
		{Name: "tenant.100", Parent: "kni0", VLANID: 100},
	}}

	first, err := neighbour.Discover(fakeBackend{
		links: links,
		neighbours: []vnetlink.Neigh{
			firstNeighbour,
			secondNeighbour,
			thirdNeighbour,
			secondNeighbour,
		},
	}, state, nil)
	require.NoError(t, err)
	second, err := neighbour.Discover(fakeBackend{
		links:      []vnetlink.Link{links[1], links[0]},
		neighbours: []vnetlink.Neigh{thirdNeighbour, secondNeighbour, firstNeighbour},
	}, state, nil)
	require.NoError(t, err)

	require.Equal(t, first, second)
	require.Equal(t, []netip.Addr{
		netip.MustParseAddr("192.0.2.3"),
		netip.MustParseAddr("192.0.2.20"),
		netip.MustParseAddr("2001:db8::1"),
	}, entryNextHops(first))
}

// Test_Discover_PreservesSameNextHopOnDifferentDevices verifies that
// per-gateway filtering can distinguish identical link-local next hops.
func Test_Discover_PreservesSameNextHopOnDifferentDevices(t *testing.T) {
	nextHop := netip.MustParseAddr("192.0.2.1")
	entries, err := neighbour.Discover(fakeBackend{
		links: []vnetlink.Link{
			testLink(1, "kni0", 1),
			testLink(2, "kni1", 1),
		},
		neighbours: []vnetlink.Neigh{
			testKernelNeighbour(1, nextHop.String(), 2, vnetlink.NUD_REACHABLE),
			testKernelNeighbour(2, nextHop.String(), 2, vnetlink.NUD_REACHABLE),
		},
	}, netplan.State{Links: []netplan.Link{
		{Name: "kni0"},
		{Name: "kni1"},
	}}, nil)

	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.Equal(t, "kni0", entries[0].HardwareRoute.Device)
	require.Equal(t, "kni1", entries[1].HardwareRoute.Device)
}

// testLink returns an Ethernet link with a usable, index-specific MAC.
func testLink(index int, name string, addressByte byte) vnetlink.Link {
	return &vnetlink.Device{LinkAttrs: vnetlink.LinkAttrs{
		Index:        index,
		Name:         name,
		HardwareAddr: testMAC(addressByte),
	}}
}

func testVLAN(index int, name string, parentIndex, vlanID int, addressByte byte) vnetlink.Link {
	return &vnetlink.Vlan{
		LinkAttrs: vnetlink.LinkAttrs{
			Index:        index,
			Name:         name,
			ParentIndex:  parentIndex,
			Alias:        netreconcile.ManagedAlias,
			HardwareAddr: testMAC(addressByte),
		},
		VlanId:       vlanID,
		VlanProtocol: vnetlink.VLAN_PROTOCOL_8021Q,
	}
}

// testKernelNeighbour returns a complete kernel neighbour record.
func testKernelNeighbour(
	linkIndex int,
	nextHop string,
	addressByte byte,
	state int,
) vnetlink.Neigh {
	return vnetlink.Neigh{
		LinkIndex:    linkIndex,
		IP:           net.IP(netip.MustParseAddr(nextHop).AsSlice()),
		HardwareAddr: testMAC(addressByte),
		State:        state,
	}
}

// testMAC returns a usable EUI-48 address with a distinctive final octet.
func testMAC(addressByte byte) net.HardwareAddr {
	return net.HardwareAddr{0x02, 0x00, 0x00, 0x00, 0x00, addressByte}
}

// testMACArray returns the fixed-width form of a test EUI-48 address.
func testMACArray(addressByte byte) [6]byte {
	return [6]byte(testMAC(addressByte))
}

// entryNextHops extracts next hops without changing their current order.
func entryNextHops(entries []neighbour.Entry) []netip.Addr {
	nextHops := make([]netip.Addr, 0, len(entries))
	for _, entry := range entries {
		nextHops = append(nextHops, entry.NextHop)
	}
	return nextHops
}
