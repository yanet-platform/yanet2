package neigh

import (
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"
)

func makeEntry(ip string, mac [6]byte, priority uint32) NeighbourEntry {
	return NeighbourEntry{
		NextHop: netip.MustParseAddr(ip),
		HardwareRoute: HardwareRoute{
			DestinationMAC: mac,
		},
		UpdatedAt: time.Now(),
		State:     NeighbourStatePermanent,
		Priority:  priority,
	}
}

// mustCreateSource is a test helper that creates a source and fails the
// test on error.
func mustCreateSource(t *testing.T, nt *NeighTable, name string, defaultPriority uint32, builtIn bool) *NeighSource {
	t.Helper()
	src, err := nt.CreateSource(name, defaultPriority, builtIn)
	require.NoError(t, err)
	return src
}

func TestNeighTableMergeLowestPriorityWins(t *testing.T) {
	nt := NewNeighTable()
	mustCreateSource(t, nt, "kernel", 100, true)
	mustCreateSource(t, nt, "static", 10, true)

	// Add an entry with priority 100 to kernel.
	require.NoError(t, nt.SwapSource("kernel", map[netip.Addr]NeighbourEntry{
		netip.MustParseAddr("10.0.0.1"): makeEntry("10.0.0.1", [6]byte{0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA}, 100),
	}))

	// Add the same IP with priority 10 to static.
	require.NoError(t, nt.Add("static", []NeighbourEntry{makeEntry("10.0.0.1", [6]byte{0xBB, 0xBB, 0xBB, 0xBB, 0xBB, 0xBB}, 10)}))

	// Merged view should pick static (priority 10 < 100).
	view := nt.View()
	entry, ok := view.Lookup(netip.MustParseAddr("10.0.0.1"))
	require.True(t, ok)
	require.Equal(t, uint32(10), entry.Priority)
	require.Equal(t, "static", entry.Source)
	require.Equal(t, [6]byte{0xBB, 0xBB, 0xBB, 0xBB, 0xBB, 0xBB}, entry.HardwareRoute.DestinationMAC)
}

func TestNeighTableMergeHigherPriorityLoses(t *testing.T) {
	nt := NewNeighTable()
	mustCreateSource(t, nt, "static", 200, true)
	mustCreateSource(t, nt, "kernel", 50, true)

	require.NoError(t, nt.Add("static", []NeighbourEntry{makeEntry("10.0.0.1", [6]byte{0xCC, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC}, 200)}))
	require.NoError(t, nt.SwapSource("kernel", map[netip.Addr]NeighbourEntry{
		netip.MustParseAddr("10.0.0.1"): makeEntry("10.0.0.1", [6]byte{0xDD, 0xDD, 0xDD, 0xDD, 0xDD, 0xDD}, 50),
	}))

	view := nt.View()
	entry, ok := view.Lookup(netip.MustParseAddr("10.0.0.1"))
	require.True(t, ok)
	require.Equal(t, uint32(50), entry.Priority)
	require.Equal(t, "kernel", entry.Source)
}

func TestNeighTableDefaultPriority(t *testing.T) {
	nt := NewNeighTable()
	mustCreateSource(t, nt, "static", 42, true)

	// Add entry with priority 0 -> should inherit default.
	entry := makeEntry("10.0.0.1", [6]byte{0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA}, 0)
	require.NoError(t, nt.Add("static", []NeighbourEntry{entry}))

	view := nt.View()
	e, ok := view.Lookup(netip.MustParseAddr("10.0.0.1"))
	require.True(t, ok)
	require.Equal(t, uint32(42), e.Priority)
}

func TestNeighTableRemoveEntry(t *testing.T) {
	nt := NewNeighTable()
	mustCreateSource(t, nt, "static", 10, true)

	require.NoError(t, nt.Add("static", []NeighbourEntry{makeEntry("10.0.0.1", [6]byte{0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA}, 10)}))

	view := nt.View()
	_, ok := view.Lookup(netip.MustParseAddr("10.0.0.1"))
	require.True(t, ok)

	require.NoError(t, nt.Remove("static", []netip.Addr{netip.MustParseAddr("10.0.0.1")}))

	view = nt.View()
	_, ok = view.Lookup(netip.MustParseAddr("10.0.0.1"))
	require.False(t, ok)
}

func TestNeighTableRemoveEntryFallsBack(t *testing.T) {
	nt := NewNeighTable()
	mustCreateSource(t, nt, "static", 10, true)
	mustCreateSource(t, nt, "kernel", 100, true)

	// Both sources have the same IP.
	require.NoError(t, nt.Add("static", []NeighbourEntry{makeEntry("10.0.0.1", [6]byte{0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA}, 10)}))
	require.NoError(t, nt.SwapSource("kernel", map[netip.Addr]NeighbourEntry{
		netip.MustParseAddr("10.0.0.1"): makeEntry("10.0.0.1", [6]byte{0xBB, 0xBB, 0xBB, 0xBB, 0xBB, 0xBB}, 100),
	}))

	// Remove from static -> kernel should take over.
	require.NoError(t, nt.Remove("static", []netip.Addr{netip.MustParseAddr("10.0.0.1")}))

	view := nt.View()
	entry, ok := view.Lookup(netip.MustParseAddr("10.0.0.1"))
	require.True(t, ok)
	require.Equal(t, "kernel", entry.Source)
	require.Equal(t, uint32(100), entry.Priority)
}

func TestNeighTableSourceView(t *testing.T) {
	nt := NewNeighTable()
	mustCreateSource(t, nt, "kernel", 100, true)
	mustCreateSource(t, nt, "static", 10, true)

	require.NoError(t, nt.Add("static", []NeighbourEntry{makeEntry("10.0.0.1", [6]byte{0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA}, 10)}))
	require.NoError(t, nt.SwapSource("kernel", map[netip.Addr]NeighbourEntry{
		netip.MustParseAddr("10.0.0.2"): makeEntry("10.0.0.2", [6]byte{0xBB, 0xBB, 0xBB, 0xBB, 0xBB, 0xBB}, 100),
	}))

	// Source view for static should only contain 10.0.0.1.
	sv, ok := nt.SourceView("static")
	require.True(t, ok)
	_, count := sv.Entries()
	require.Equal(t, 1, count)
	_, ok = sv.Lookup(netip.MustParseAddr("10.0.0.1"))
	require.True(t, ok)

	// Source view for kernel should only contain 10.0.0.2.
	sv, ok = nt.SourceView("kernel")
	require.True(t, ok)
	_, count = sv.Entries()
	require.Equal(t, 1, count)
	_, ok = sv.Lookup(netip.MustParseAddr("10.0.0.2"))
	require.True(t, ok)

	// Non-existent source returns false.
	_, ok = nt.SourceView("nonexistent")
	require.False(t, ok)
}

func TestNeighTableCreateSource(t *testing.T) {
	nt := NewNeighTable()

	_, err := nt.CreateSource("custom", 50, false)
	require.NoError(t, err)

	sources := nt.ListSources()
	require.Len(t, sources, 1)
	require.Equal(t, "custom", sources[0].Name)
	require.Equal(t, uint32(50), sources[0].DefaultPriority)
	require.False(t, sources[0].BuiltIn)

	// Duplicate creation fails.
	_, err = nt.CreateSource("custom", 60, false)
	require.Error(t, err)
}

func TestNeighTableUpdateSource(t *testing.T) {
	nt := NewNeighTable()
	mustCreateSource(t, nt, "kernel", 100, true)

	require.NoError(t, nt.UpdateSource("kernel", 200))

	sources := nt.ListSources()
	require.Len(t, sources, 1)
	require.Equal(t, uint32(200), sources[0].DefaultPriority)

	// Non-existent source fails.
	require.Error(t, nt.UpdateSource("nonexistent", 50))
}

func TestNeighTableDeleteSource(t *testing.T) {
	nt := NewNeighTable()
	mustCreateSource(t, nt, "kernel", 100, true)
	mustCreateSource(t, nt, "custom", 50, false)

	// Cannot delete built-in.
	require.Error(t, nt.DeleteSource("kernel"))

	// Can delete user-defined.
	require.NoError(t, nt.DeleteSource("custom"))

	sources := nt.ListSources()
	require.Len(t, sources, 1)
	require.Equal(t, "kernel", sources[0].Name)

	// Non-existent source fails.
	require.Error(t, nt.DeleteSource("nonexistent"))
}

func TestNeighTableDeleteSourceRemovesEntries(t *testing.T) {
	nt := NewNeighTable()
	mustCreateSource(t, nt, "custom", 10, false)

	require.NoError(t, nt.Add("custom", []NeighbourEntry{makeEntry("10.0.0.1", [6]byte{0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA}, 10)}))

	view := nt.View()
	_, ok := view.Lookup(netip.MustParseAddr("10.0.0.1"))
	require.True(t, ok)

	require.NoError(t, nt.DeleteSource("custom"))

	view = nt.View()
	_, ok = view.Lookup(netip.MustParseAddr("10.0.0.1"))
	require.False(t, ok)
}

func TestNeighTableAddToNonExistentSource(t *testing.T) {
	nt := NewNeighTable()

	require.Error(t, nt.Add("nonexistent", []NeighbourEntry{makeEntry("10.0.0.1", [6]byte{0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA}, 10)}))
}

func TestNeighTableSwapSourceNonExistent(t *testing.T) {
	nt := NewNeighTable()

	require.Error(t, nt.SwapSource("nonexistent", map[netip.Addr]NeighbourEntry{}))
}

func TestNeighTableBatchAdd(t *testing.T) {
	nt := NewNeighTable()
	mustCreateSource(t, nt, "static", 10, true)

	entries := []NeighbourEntry{
		makeEntry("10.0.0.1", [6]byte{0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA}, 10),
		makeEntry("10.0.0.2", [6]byte{0xBB, 0xBB, 0xBB, 0xBB, 0xBB, 0xBB}, 20),
		makeEntry("10.0.0.3", [6]byte{0xCC, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC}, 30),
	}
	require.NoError(t, nt.Add("static", entries))

	view := nt.View()
	_, count := view.Entries()
	require.Equal(t, 3, count)
}

func TestNeighTableBatchRemove(t *testing.T) {
	nt := NewNeighTable()
	mustCreateSource(t, nt, "static", 10, true)

	entries := []NeighbourEntry{
		makeEntry("10.0.0.1", [6]byte{0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA}, 10),
		makeEntry("10.0.0.2", [6]byte{0xBB, 0xBB, 0xBB, 0xBB, 0xBB, 0xBB}, 20),
		makeEntry("10.0.0.3", [6]byte{0xCC, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC}, 30),
	}
	require.NoError(t, nt.Add("static", entries))

	require.NoError(t, nt.Remove("static", []netip.Addr{
		netip.MustParseAddr("10.0.0.1"),
		netip.MustParseAddr("10.0.0.3"),
	}))

	view := nt.View()
	_, count := view.Entries()
	require.Equal(t, 1, count)

	_, ok := view.Lookup(netip.MustParseAddr("10.0.0.2"))
	require.True(t, ok)
}

func TestNeighTableListSources(t *testing.T) {
	nt := NewNeighTable()
	mustCreateSource(t, nt, "kernel", 100, true)
	mustCreateSource(t, nt, "static", 10, true)

	require.NoError(t, nt.Add("static", []NeighbourEntry{makeEntry("10.0.0.1", [6]byte{0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA}, 10)}))
	require.NoError(t, nt.SwapSource("kernel", map[netip.Addr]NeighbourEntry{
		netip.MustParseAddr("10.0.0.2"): makeEntry("10.0.0.2", [6]byte{0xBB, 0xBB, 0xBB, 0xBB, 0xBB, 0xBB}, 100),
		netip.MustParseAddr("10.0.0.3"): makeEntry("10.0.0.3", [6]byte{0xCC, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC}, 100),
	}))

	sources := nt.ListSources()
	require.Len(t, sources, 2)

	sourceMap := map[string]SourceInfo{}
	for _, s := range sources {
		sourceMap[s.Name] = s
	}

	require.Equal(t, 1, sourceMap["static"].EntryCount)
	require.True(t, sourceMap["static"].BuiltIn)
	require.Equal(t, 2, sourceMap["kernel"].EntryCount)
	require.True(t, sourceMap["kernel"].BuiltIn)
}

// Test_NeighTable_OnChanged_FiresOnlyOnHardwareRouteChange verifies that
// the change hook fires exactly when a merged nexthop gains, changes or
// loses its hardware route.
func Test_NeighTable_OnChanged_FiresOnlyOnHardwareRouteChange(t *testing.T) {
	nexthop := netip.MustParseAddr("10.0.0.1")
	macA := [6]byte{0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA}
	macB := [6]byte{0xBB, 0xBB, 0xBB, 0xBB, 0xBB, 0xBB}

	// kernelEntry is a kernel-learned nexthop with the given MAC and state.
	kernelEntry := func(mac [6]byte, state NeighbourState) NeighbourEntry {
		entry := makeEntry("10.0.0.1", mac, 0)
		entry.HardwareRoute.Device = "eth0"
		entry.State = state
		return entry
	}
	// seedKernel fills the kernel source with one reachable nexthop.
	seedKernel := func(t *testing.T, nt *NeighTable) {
		t.Helper()
		require.NoError(t, nt.SwapSource("kernel", map[netip.Addr]NeighbourEntry{
			nexthop: kernelEntry(macA, NeighbourState(netlink.NUD_REACHABLE)),
		}))
	}
	// seedStaticOverKernel shadows the kernel nexthop with a static one.
	seedStaticOverKernel := func(t *testing.T, nt *NeighTable) {
		t.Helper()
		seedKernel(t, nt)
		require.NoError(t, nt.Add("static", []NeighbourEntry{kernelEntry(macB, NeighbourStatePermanent)}))
	}

	cases := []struct {
		name      string
		setup     func(t *testing.T, nt *NeighTable)
		mutate    func(nt *NeighTable) error
		wantFired bool
		wantErr   bool
	}{
		{
			name:  "new nexthop appears",
			setup: func(t *testing.T, nt *NeighTable) {},
			mutate: func(nt *NeighTable) error {
				return nt.Add("static", []NeighbourEntry{kernelEntry(macA, NeighbourStatePermanent)})
			},
			wantFired: true,
		},
		{
			name:  "same entry added again",
			setup: seedKernel,
			mutate: func(nt *NeighTable) error {
				return nt.Add("kernel", []NeighbourEntry{kernelEntry(macA, NeighbourState(netlink.NUD_REACHABLE))})
			},
			wantFired: false,
		},
		{
			name:  "state-only refresh",
			setup: seedKernel,
			mutate: func(nt *NeighTable) error {
				return nt.SwapSource("kernel", map[netip.Addr]NeighbourEntry{
					nexthop: kernelEntry(macA, NeighbourState(netlink.NUD_STALE)),
				})
			},
			wantFired: false,
		},
		{
			name:  "destination mac changes",
			setup: seedKernel,
			mutate: func(nt *NeighTable) error {
				return nt.SwapSource("kernel", map[netip.Addr]NeighbourEntry{
					nexthop: kernelEntry(macB, NeighbourState(netlink.NUD_REACHABLE)),
				})
			},
			wantFired: true,
		},
		{
			name:  "egress device changes",
			setup: seedKernel,
			mutate: func(nt *NeighTable) error {
				entry := kernelEntry(macA, NeighbourState(netlink.NUD_REACHABLE))
				entry.HardwareRoute.Device = "eth1"
				return nt.Add("kernel", []NeighbourEntry{entry})
			},
			wantFired: true,
		},
		{
			name:  "merged nexthop removed",
			setup: seedKernel,
			mutate: func(nt *NeighTable) error {
				return nt.Remove("kernel", []netip.Addr{nexthop})
			},
			wantFired: true,
		},
		{
			name:  "unknown nexthop removed",
			setup: seedKernel,
			mutate: func(nt *NeighTable) error {
				return nt.Remove("kernel", []netip.Addr{netip.MustParseAddr("10.0.0.2")})
			},
			wantFired: false,
		},
		{
			name:  "shadowed source changes",
			setup: seedStaticOverKernel,
			mutate: func(nt *NeighTable) error {
				return nt.SwapSource("kernel", map[netip.Addr]NeighbourEntry{})
			},
			wantFired: false,
		},
		{
			name:  "winning source removed with a different fallback",
			setup: seedStaticOverKernel,
			mutate: func(nt *NeighTable) error {
				return nt.Remove("static", []netip.Addr{nexthop})
			},
			wantFired: true,
		},
		{
			name: "fully shadowed source deleted",
			setup: func(t *testing.T, nt *NeighTable) {
				mustCreateSource(t, nt, "extra", 200, false)
				seedKernel(t, nt)
				require.NoError(t, nt.Add("extra", []NeighbourEntry{kernelEntry(macB, NeighbourStatePermanent)}))
			},
			mutate: func(nt *NeighTable) error {
				return nt.DeleteSource("extra")
			},
			wantFired: false,
		},
		{
			name: "only source of a nexthop deleted",
			setup: func(t *testing.T, nt *NeighTable) {
				mustCreateSource(t, nt, "extra", 200, false)
				require.NoError(t, nt.Add("extra", []NeighbourEntry{kernelEntry(macB, NeighbourStatePermanent)}))
			},
			mutate: func(nt *NeighTable) error {
				return nt.DeleteSource("extra")
			},
			wantFired: true,
		},
		{
			name:  "source created",
			setup: func(t *testing.T, nt *NeighTable) {},
			mutate: func(nt *NeighTable) error {
				_, err := nt.CreateSource("extra", 200, false)
				return err
			},
			wantFired: false,
		},
		{
			name:  "source default priority updated",
			setup: seedKernel,
			mutate: func(nt *NeighTable) error {
				return nt.UpdateSource("kernel", 5)
			},
			wantFired: false,
		},
		{
			name:  "mutation of an unknown source fails",
			setup: seedKernel,
			mutate: func(nt *NeighTable) error {
				return nt.Add("missing", []NeighbourEntry{kernelEntry(macB, NeighbourStatePermanent)})
			},
			wantFired: false,
			wantErr:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fired := 0
			nt := NewNeighTable(WithTableOnChanged(func() { fired++ }))
			mustCreateSource(t, nt, "kernel", 100, true)
			mustCreateSource(t, nt, "static", 10, true)
			tc.setup(t, nt)
			fired = 0

			err := tc.mutate(nt)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tc.wantFired {
				require.Equal(t, 1, fired, "the hook must fire once for a merged hardware route change")
			} else {
				require.Zero(t, fired, "the hook must stay silent when the merged hardware routes are intact")
			}
		})
	}
}

// Test_NeighTable_OnChanged_RunsOutsideTheLock verifies that the change
// hook may read the table it was fired from without deadlocking.
func Test_NeighTable_OnChanged_RunsOutsideTheLock(t *testing.T) {
	var nt *NeighTable
	// The hook reads through a lock-taking accessor, so a hook still holding
	// the table lock blocks here instead of returning.
	sources := make(chan []SourceInfo, 1)
	nt = NewNeighTable(WithTableOnChanged(func() {
		sources <- nt.ListSources()
	}))
	mustCreateSource(t, nt, "static", 10, true)

	added := make(chan error, 1)
	go func() {
		added <- nt.Add("static", []NeighbourEntry{
			makeEntry("10.0.0.1", [6]byte{0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA}, 0),
		})
	}()

	select {
	case err := <-added:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the change hook must not run under the table lock")
	}
	require.Len(t, <-sources, 1)
}
