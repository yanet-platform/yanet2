package neighbour_test

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	vnetlink "github.com/vishvananda/netlink"

	"github.com/yanet-platform/yanet2/modules/route/controlplane/hwroute"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/desired"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

// Test_ValidateManagedDevices_DeviceNameBoundary verifies that startup mappings
// preserve the same byte-exact logical identity as published FIB entries.
func Test_ValidateManagedDevices_DeviceNameBoundary(t *testing.T) {
	for _, test := range []struct {
		name   string
		device string
		valid  bool
	}{
		{name: "79 bytes", device: strings.Repeat("d", 79), valid: true},
		{name: "80 bytes", device: strings.Repeat("d", 80)},
		{name: "embedded NUL", device: "logical0\x00other"},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := desired.State{Links: []desired.Link{{Name: "kni0"}}}
			err := neighbour.ValidateManagedDevices(state, map[string]string{"kni0": test.device})
			if test.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

type fakeBackend struct {
	links          []vnetlink.Link
	linkError      error
	neighbours     []vnetlink.Neigh
	neighbourError error
}

// generatedBackend exercises an over-limit dump without allocating it first.
type generatedBackend struct {
	fakeBackend
	Count    int
	Visited  int
	CancelAt int
	Cancel   context.CancelFunc
}

func (m *generatedBackend) WalkNeighbours(ctx context.Context, visit func(vnetlink.Neigh) error) error {
	entry := testKernelNeighbour(99, "192.0.2.1", 2, vnetlink.NUD_REACHABLE)
	for range m.Count {
		m.Visited++
		if m.Visited == m.CancelAt {
			m.Cancel()
		}
		if err := visit(entry); err != nil {
			return err
		}
	}
	return nil
}

// Test_Discover_EntryBound verifies that an oversized dump stops at the shared
// limit even when every record is outside the managed topology.
func Test_Discover_EntryBound(t *testing.T) {
	backend := &generatedBackend{fakeBackend: fakeBackend{links: []vnetlink.Link{testLink(1, "kni0", 1)}}, Count: operatorpb.NeighbourSnapshotEntries + 100}
	entries, err := neighbour.Discover(t.Context(), backend, desired.State{Links: []desired.Link{{Name: "kni0"}}}, nil)
	require.ErrorContains(t, err, "entry limit")
	require.Nil(t, entries)
	require.Equal(t, operatorpb.NeighbourSnapshotEntries+1, backend.Visited)
}

// Test_Discover_CancelDuringScan verifies that cancellation aborts a partial
// observation without consuming the remainder or returning publishable data.
func Test_Discover_CancelDuringScan(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	backend := &generatedBackend{fakeBackend: fakeBackend{links: []vnetlink.Link{testLink(1, "kni0", 1)}}, Count: 1000, CancelAt: 3, Cancel: cancel}
	entries, err := neighbour.Discover(ctx, backend, desired.State{Links: []desired.Link{{Name: "kni0"}}}, nil)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, entries)
	require.Equal(t, 3, backend.Visited)
}

// Test_Discover_CanonicalDuplicates verifies that repeated canonical IPs fail
// the dump even if NUD state differs or both payloads are identical.
func Test_Discover_CanonicalDuplicates(t *testing.T) {
	for _, test := range []struct {
		name        string
		conflicting bool
	}{
		{name: "same payload across NUD transition"},
		{name: "changed destination MAC fails the snapshot", conflicting: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			first := testKernelNeighbour(1, "192.0.2.1", 2, vnetlink.NUD_REACHABLE)
			second := testKernelNeighbour(1, "::ffff:192.0.2.1", 2, vnetlink.NUD_STALE)
			if test.conflicting {
				second.HardwareAddr = testMAC(3)
			}
			backend := fakeBackend{links: []vnetlink.Link{testLink(1, "kni0", 1)}, neighbours: []vnetlink.Neigh{first, second}}
			entries, err := neighbour.Discover(t.Context(), backend, desired.State{Links: []desired.Link{{Name: "kni0"}}}, nil)
			require.ErrorContains(t, err, "duplicate next hop")
			require.Nil(t, entries)
		})
	}
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

func (m *changingBackend) WalkNeighbours(ctx context.Context, visit func(vnetlink.Neigh) error) error {
	return walkNeighbours(ctx, m.neighbours, visit)
}

// LinkList returns the configured complete or partial link dump.
func (m fakeBackend) LinkList() ([]vnetlink.Link, error) {
	return m.links, m.linkError
}

// WalkNeighbours delivers partial data before reporting an interrupted dump.
func (m fakeBackend) WalkNeighbours(ctx context.Context, visit func(vnetlink.Neigh) error) error {
	if err := walkNeighbours(ctx, m.neighbours, visit); err != nil {
		return err
	}
	return m.neighbourError
}

// walkNeighbours stops the fixture at the same visitor boundary as the kernel.
func walkNeighbours(ctx context.Context, entries []vnetlink.Neigh, visit func(vnetlink.Neigh) error) error {
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(entry); err != nil {
			return err
		}
	}
	return nil
}

// Test_Discover_ManagedIsolationAndLinkMapping verifies that only neighbours
// on state-owned links are returned with mapped or fallback device names.
func Test_Discover_ManagedIsolationAndLinkMapping(t *testing.T) {
	state := desired.State{Links: []desired.Link{
		{Name: "kni0"},
		{Name: "tenant.100", Kind: desired.LinkKindVLAN, Parent: "kni0", VLANID: 100},
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
		t.Context(),
		backend,
		state,
		map[string]string{"tenant.100": "dataplane-vlan"},
	)
	require.NoError(t, err)
	require.ElementsMatch(t, []neighbour.Entry{
		testEntry("192.0.2.10", 1, 10, "kni0"),
		testEntry("192.0.2.20", 2, 20, "dataplane-vlan"),
	}, entries)
}

// Test_Discover_RejectsMappedAndFallbackDeviceCollision verifies that a mapped
// name cannot alias another managed link's unmapped logical device.
func Test_Discover_RejectsMappedAndFallbackDeviceCollision(t *testing.T) {
	entries, err := neighbour.Discover(
		t.Context(),
		fakeBackend{},
		desired.State{Links: []desired.Link{{Name: "kni0"}, {Name: "kni1"}}},
		map[string]string{"kni0": "kni1"},
	)

	require.ErrorContains(t, err, `managed links "kni0" and "kni1" map to duplicate logical device "kni1"`)
	require.Nil(t, entries)
}

// Test_Discover_RejectsMappingForUnmanagedLink verifies that mappings outside
// the managed topology invalidate discovery rather than being silently ignored.
func Test_Discover_RejectsMappingForUnmanagedLink(t *testing.T) {
	entries, err := neighbour.Discover(
		t.Context(),
		fakeBackend{},
		desired.State{Links: []desired.Link{{Name: "kni0"}}},
		map[string]string{"kni1": "logical1"},
	)

	require.ErrorContains(t, err, `link_map entry "kni1" is not a managed link`)
	require.Nil(t, entries)
}

// Test_Discover_SkipsMalformedAddresses verifies that malformed IP and MAC
// data is omitted without invalidating otherwise complete kernel dumps.
func Test_Discover_SkipsMalformedAddresses(t *testing.T) {
	state := desired.State{Links: []desired.Link{{Name: "kni0"}}}
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

	entries, err := neighbour.Discover(t.Context(), backend, state, nil)
	require.NoError(t, err)
	require.Equal(t, []neighbour.Entry{testEntry("192.0.2.1", 1, 1, "kni0")}, entries)
}

// Test_Discover_RejectsInvalidManagedLinks verifies that ambiguous or malformed
// link identity invalidates the entire neighbour snapshot.
func Test_Discover_RejectsInvalidManagedLinks(t *testing.T) {
	tests := []struct {
		name          string
		links         []vnetlink.Link
		errorContains string
	}{
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
			name:          "wrong type without MAC",
			links:         []vnetlink.Link{&vnetlink.Dummy{LinkAttrs: vnetlink.LinkAttrs{Name: "kni0", Index: 1}}},
			errorContains: "incompatible type",
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
				t.Context(),
				fakeBackend{links: test.links},
				desired.State{Links: []desired.Link{{Name: "kni0"}}},
				nil,
			)

			require.ErrorContains(t, err, test.errorContains)
			require.Nil(t, entries)
		})
	}
}

// Test_Discover_RejectsInvalidManagedVLANIdentity verifies that a VLAN's type,
// tag, protocol, and parent must match before publishing neighbours.
func Test_Discover_RejectsInvalidManagedVLANIdentity(t *testing.T) {
	tests := []struct {
		name          string
		link          vnetlink.Link
		errorContains string
	}{
		{
			name:          "wrong type",
			link:          testLink(2, "tenant.100", 2),
			errorContains: "incompatible",
		},
		{
			name:          "wrong VLAN ID",
			link:          testVLAN(2, "tenant.100", 1, 200, 2),
			errorContains: "incompatible",
		},
		{
			name: "wrong VLAN protocol",
			link: &vnetlink.Vlan{
				LinkAttrs: vnetlink.LinkAttrs{
					Index:        2,
					Name:         "tenant.100",
					ParentIndex:  1,
					Alias:        "existing-alias",
					HardwareAddr: testMAC(2),
				},
				VlanId:       100,
				VlanProtocol: vnetlink.VLAN_PROTOCOL_8021AD,
			},
			errorContains: "incompatible",
		},
		{
			name:          "wrong parent",
			link:          testVLAN(2, "tenant.100", 99, 100, 2),
			errorContains: "incompatible",
		},
	}
	state := desired.State{Links: []desired.Link{
		{Name: "kni0"},
		{Name: "tenant.100", Kind: desired.LinkKindVLAN, Parent: "kni0", VLANID: 100},
	}}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entries, err := neighbour.Discover(t.Context(), fakeBackend{
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
			entries, err := neighbour.Discover(t.Context(), fakeBackend{
				links: []vnetlink.Link{testLink(1, "kni0", 1)},
				neighbours: []vnetlink.Neigh{
					testKernelNeighbour(1, "192.0.2.1", 2, test.state),
				},
			}, desired.State{Links: []desired.Link{{Name: "kni0"}}}, nil)
			require.NoError(t, err)
			if !test.usable {
				require.Empty(t, entries)
				return
			}
			require.Equal(t, []neighbour.Entry{testEntry("192.0.2.1", 1, 2, "kni0")}, entries)
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
				t.Context(),
				test.backend,
				desired.State{Links: []desired.Link{{Name: "kni0"}}},
				nil,
			)
			require.ErrorIs(t, err, vnetlink.ErrDumpInterrupted)
			require.Nil(t, entries)
		})
	}
}

// Test_Discover_RejectsLinkChangesDuringDump verifies that overlapping creation,
// disappearance, replacement or MAC readiness cannot authorize a partial dump.
func Test_Discover_RejectsLinkChangesDuringDump(t *testing.T) {
	for _, tc := range []struct {
		name          string
		before, after []vnetlink.Link
	}{
		{name: "created", after: []vnetlink.Link{testLink(1, "kni0", 1)}},
		{name: "created without MAC", after: []vnetlink.Link{&vnetlink.Device{LinkAttrs: vnetlink.LinkAttrs{Name: "kni0", Index: 1}}}},
		{name: "deleted", before: []vnetlink.Link{testLink(1, "kni0", 1)}},
		{name: "replaced", before: []vnetlink.Link{testLink(1, "kni0", 1)}, after: []vnetlink.Link{testLink(2, "kni0", 2)}},
		{name: "MAC became ready", before: []vnetlink.Link{&vnetlink.Device{LinkAttrs: vnetlink.LinkAttrs{Name: "kni0", Index: 1}}}, after: []vnetlink.Link{testLink(1, "kni0", 1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &changingBackend{
				linkSnapshots: [][]vnetlink.Link{tc.before, tc.after},
				neighbours:    []vnetlink.Neigh{testKernelNeighbour(1, "192.0.2.1", 3, vnetlink.NUD_REACHABLE)},
			}
			entries, err := neighbour.Discover(t.Context(), backend, desired.State{Links: []desired.Link{{Name: "kni0"}}}, nil)
			require.ErrorContains(t, err, "managed links changed during dump")
			require.Nil(t, entries)
		})
	}
}

// Test_Discover_DumpOrderIndependence verifies that dump order does not alter
// the complete contents of the desired snapshot.
func Test_Discover_DumpOrderIndependence(t *testing.T) {
	links := []vnetlink.Link{
		testLink(1, "kni0", 1),
		testVLAN(2, "tenant.100", 1, 100, 2),
	}
	firstNeighbour := testKernelNeighbour(1, "2001:db8::1", 1, vnetlink.NUD_STALE)
	secondNeighbour := testKernelNeighbour(2, "192.0.2.20", 2, vnetlink.NUD_REACHABLE)
	thirdNeighbour := testKernelNeighbour(1, "192.0.2.3", 3, vnetlink.NUD_DELAY)
	state := desired.State{Links: []desired.Link{
		{Name: "kni0"},
		{Name: "tenant.100", Kind: desired.LinkKindVLAN, Parent: "kni0", VLANID: 100},
	}}

	first, err := neighbour.Discover(t.Context(), fakeBackend{
		links: links,
		neighbours: []vnetlink.Neigh{
			firstNeighbour,
			secondNeighbour,
			thirdNeighbour,
		},
	}, state, nil)
	require.NoError(t, err)
	second, err := neighbour.Discover(t.Context(), fakeBackend{
		links:      []vnetlink.Link{links[1], links[0]},
		neighbours: []vnetlink.Neigh{thirdNeighbour, secondNeighbour, firstNeighbour},
	}, state, nil)
	require.NoError(t, err)

	require.ElementsMatch(t, first, second)
	require.ElementsMatch(t, []neighbour.Entry{
		testEntry("192.0.2.3", 1, 3, "kni0"),
		testEntry("192.0.2.20", 2, 2, "tenant.100"),
		testEntry("2001:db8::1", 1, 1, "kni0"),
	}, first)
}

// Test_Discover_ExcludesMulticast verifies that multicast neighbours shared by
// managed devices neither enter the snapshot nor hide valid unicast neighbours.
func Test_Discover_ExcludesMulticast(t *testing.T) {
	for _, tc := range []struct {
		name         string
		nextHop      string
		hardwareAddr net.HardwareAddr
		state        int
	}{
		{name: "IPv6 MLD NOARP", nextHop: "ff02::16", hardwareAddr: net.HardwareAddr{0x33, 0x33, 0, 0, 0, 0x16}, state: vnetlink.NUD_NOARP},
		{name: "IPv6 global multicast permanent", nextHop: "ff0e::16", hardwareAddr: net.HardwareAddr{0x33, 0x33, 0, 0, 0, 0x16}, state: vnetlink.NUD_PERMANENT},
		{name: "IPv4 multicast NOARP", nextHop: "224.0.0.22", hardwareAddr: net.HardwareAddr{0x01, 0, 0x5e, 0, 0, 0x16}, state: vnetlink.NUD_NOARP},
		{name: "IPv4 multicast upper boundary reachable", nextHop: "239.255.255.255", hardwareAddr: net.HardwareAddr{0x01, 0, 0x5e, 0x7f, 0xff, 0xff}, state: vnetlink.NUD_REACHABLE},
		{name: "mapped IPv4 multicast NOARP", nextHop: "::ffff:224.0.0.22", hardwareAddr: net.HardwareAddr{0x01, 0, 0x5e, 0, 0, 0x16}, state: vnetlink.NUD_NOARP},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := vnetlink.Neigh{
				LinkIndex:    1,
				IP:           net.IP(netip.MustParseAddr(tc.nextHop).AsSlice()),
				HardwareAddr: tc.hardwareAddr,
				State:        tc.state,
			}
			second := first
			second.LinkIndex = 2
			links := []vnetlink.Link{testLink(1, "kni0", 1), testLink(2, "kni1", 2)}
			neighbours := []vnetlink.Neigh{first, second}
			state := desired.State{Links: []desired.Link{{Name: "kni0"}, {Name: "kni1"}}}
			entries, err := neighbour.Discover(t.Context(), fakeBackend{links: links, neighbours: neighbours}, state, nil)
			require.NoError(t, err)
			require.Empty(t, entries)

			neighbours = append(neighbours,
				testKernelNeighbour(1, "fe80::1", 3, vnetlink.NUD_NOARP),
				testKernelNeighbour(2, "192.0.2.1", 4, vnetlink.NUD_PERMANENT),
			)
			entries, err = neighbour.Discover(t.Context(), fakeBackend{links: links, neighbours: neighbours}, state, nil)
			require.NoError(t, err)
			require.ElementsMatch(t, []neighbour.Entry{
				testEntry("192.0.2.1", 2, 4, "kni1"),
				testEntry("fe80::1", 1, 3, "kni0"),
			}, entries)

			neighbours = append(neighbours, testKernelNeighbour(1, "::ffff:192.0.2.1", 4, vnetlink.NUD_NOARP))
			entries, err = neighbour.Discover(t.Context(), fakeBackend{links: links, neighbours: neighbours}, state, nil)
			require.ErrorContains(t, err, "duplicate next hop 192.0.2.1")
			require.Nil(t, entries)
		})
	}
}

// Test_Discover_DuplicateIPRejected verifies that equal canonical unicast IPs on
// different devices invalidate the entire dump before a winner can be chosen.
func Test_Discover_DuplicateIPRejected(t *testing.T) {
	for _, tc := range []struct {
		name   string
		first  string
		second string
		state  int
	}{
		{name: "IPv4 on two devices", first: "192.0.2.1", second: "192.0.2.1", state: vnetlink.NUD_REACHABLE},
		{name: "mapped IPv4 on another device", first: "192.0.2.1", second: "::ffff:192.0.2.1", state: vnetlink.NUD_PERMANENT},
		{name: "link-local IPv6 on two devices", first: "fe80::1", second: "fe80::1", state: vnetlink.NUD_REACHABLE},
		{name: "IPv4 NOARP on two devices", first: "192.0.2.1", second: "192.0.2.1", state: vnetlink.NUD_NOARP},
		{name: "link-local IPv6 NOARP on two devices", first: "fe80::1", second: "fe80::1", state: vnetlink.NUD_NOARP},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := neighbour.Discover(t.Context(), fakeBackend{
				links: []vnetlink.Link{testLink(1, "kni0", 1), testLink(2, "kni1", 1)},
				neighbours: []vnetlink.Neigh{
					testKernelNeighbour(1, "2001:db8::1", 3, vnetlink.NUD_REACHABLE),
					testKernelNeighbour(1, tc.first, 2, tc.state),
					testKernelNeighbour(2, tc.second, 2, tc.state),
				},
			}, desired.State{Links: []desired.Link{{Name: "kni0"}, {Name: "kni1"}}}, nil)
			require.ErrorContains(t, err, "duplicate next hop")
			require.Nil(t, entries)
		})
	}
}

// Test_Discover_PendingSetupDoesNotGateEgress verifies that absent KNI/VLANs,
// missing source MACs and unrelated configuration do not block healthy egress.
func Test_Discover_PendingSetupDoesNotGateEgress(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pending vnetlink.Link
	}{
		{name: "absent KNI"},
		{name: "KNI without MAC", pending: &vnetlink.Device{LinkAttrs: vnetlink.LinkAttrs{Name: "kni1", Index: 2}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			links := []vnetlink.Link{testLink(1, "kni0", 1)}
			if tc.pending != nil {
				links = append(links, tc.pending)
			}
			state := desired.State{Links: []desired.Link{
				{Name: "kni0", MTU: 9000, Addresses: []netip.Prefix{netip.MustParsePrefix("2001:db8::1/64")}},
				{Name: "kni1"}, {Name: "vlan0", Kind: desired.LinkKindVLAN, Parent: "kni0", VLANID: 100},
				{Name: "lo", Kind: desired.LinkKindLoopback}, {Name: "dummy0", Kind: desired.LinkKindDummy},
			}}
			entries, err := neighbour.Discover(t.Context(), fakeBackend{links: links, neighbours: []vnetlink.Neigh{
				testKernelNeighbour(1, "192.0.2.1", 3, vnetlink.NUD_REACHABLE),
				testKernelNeighbour(2, "192.0.2.2", 4, vnetlink.NUD_REACHABLE),
			}}, state, nil)
			require.NoError(t, err)
			require.Equal(t, []neighbour.Entry{testEntry("192.0.2.1", 1, 3, "kni0")}, entries)
			entries, err = neighbour.Discover(t.Context(), fakeBackend{}, state, nil)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

// testLink returns an Ethernet link with a usable, index-specific MAC.
func testLink(index int, name string, addressByte byte) vnetlink.Link {
	return &vnetlink.Device{LinkAttrs: vnetlink.LinkAttrs{
		Index:        index,
		Name:         name,
		HardwareAddr: testMAC(addressByte),
	}}
}

// testVLAN preserves the parent/tag identity with a usable source MAC.
func testVLAN(index int, name string, parentIndex, vlanID int, addressByte byte) vnetlink.Link {
	return &vnetlink.Vlan{
		LinkAttrs: vnetlink.LinkAttrs{
			Index:        index,
			Name:         name,
			ParentIndex:  parentIndex,
			Alias:        "existing-alias",
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

// testEntry builds an expected route with distinct EUI-48 final octets.
func testEntry(nextHop string, sourceMAC, destinationMAC byte, device string) neighbour.Entry {
	return neighbour.Entry{
		NextHop: netip.MustParseAddr(nextHop),
		HardwareRoute: hwroute.HardwareRoute{
			SourceMAC:      [6]byte{2, 0, 0, 0, 0, sourceMAC},
			DestinationMAC: [6]byte{2, 0, 0, 0, 0, destinationMAC},
			Device:         device,
		},
	}
}

// Test_Discover_ExcludesLoopbacks verifies that dummy MACs and irrelevant
// loopback mappings cannot enter or invalidate an egress snapshot.
func Test_Discover_ExcludesLoopbacks(t *testing.T) {
	entries, err := neighbour.Discover(t.Context(), fakeBackend{
		links: []vnetlink.Link{
			testLink(1, "kni0", 1),
			&vnetlink.Device{LinkAttrs: vnetlink.LinkAttrs{Name: "lo", Index: 2, Flags: net.FlagLoopback}},
			&vnetlink.Dummy{LinkAttrs: vnetlink.LinkAttrs{Name: "loop1", Index: 3, HardwareAddr: testMAC(3)}},
		},
		neighbours: []vnetlink.Neigh{
			testKernelNeighbour(1, "fe80::1", 2, vnetlink.NUD_REACHABLE),
			testKernelNeighbour(3, "fe80::2", 4, vnetlink.NUD_REACHABLE),
		},
	}, desired.State{Links: []desired.Link{
		{Name: "kni0"}, {Name: "lo", Kind: desired.LinkKindLoopback}, {Name: "loop1", Kind: desired.LinkKindDummy},
	}}, map[string]string{"lo": "", "loop1": "kni0"})
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "kni0", entries[0].HardwareRoute.Device)
}
