package neigh

import (
	"context"
	"fmt"
	"maps"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"
	"golang.org/x/sync/errgroup"
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

// Test_NeighTable_SnapshotFiltersBeforeMerging verifies that device-specific
// views retain colliding next hops and an old snapshot survives source updates.
func Test_NeighTable_SnapshotFiltersBeforeMerging(t *testing.T) {
	nt := NewNeighTable()
	mustCreateSource(t, nt, "gateway-a", 100, false)
	mustCreateSource(t, nt, "gateway-b", 100, false)

	nextHop := netip.MustParseAddr("fe80::1")
	entryA := makeEntry(nextHop.String(), [6]byte{0xAA, 0, 0, 0, 0, 1}, 100)
	entryA.HardwareRoute.Device = "kni0"
	entryB := makeEntry(nextHop.String(), [6]byte{0xBB, 0, 0, 0, 0, 2}, 100)
	entryB.HardwareRoute.Device = "kni1"
	require.NoError(t, nt.Add("gateway-a", []NeighbourEntry{entryA}))
	require.NoError(t, nt.Add("gateway-b", []NeighbourEntry{entryB}))

	snapshot := nt.Snapshot()
	viewA := snapshot.ViewByDevices([]string{"kni0"})
	actualA, ok := viewA.Lookup(nextHop)
	require.True(t, ok)
	require.Equal(t, "kni0", actualA.HardwareRoute.Device)
	require.Equal(t, "gateway-a", actualA.Source)

	viewB := snapshot.ViewByDevices([]string{"kni1"})
	actualB, ok := viewB.Lookup(nextHop)
	require.True(t, ok)
	require.Equal(t, "kni1", actualB.HardwareRoute.Device)
	require.Equal(t, "gateway-b", actualB.Source)

	unfiltered, ok := snapshot.ViewByDevices(nil).Lookup(nextHop)
	require.True(t, ok)
	require.Equal(t, "gateway-a", unfiltered.Source)

	updatedA := entryA
	updatedA.HardwareRoute.Device = "kni2"
	require.NoError(t, nt.Add("gateway-a", []NeighbourEntry{updatedA}))
	oldSnapshotEntry, ok := snapshot.ViewByDevices([]string{"kni0"}).Lookup(nextHop)
	require.True(t, ok)
	require.Equal(t, "kni0", oldSnapshotEntry.HardwareRoute.Device)
	newSnapshotEntry, ok := nt.Snapshot().ViewByDevices([]string{"kni2"}).Lookup(nextHop)
	require.True(t, ok)
	require.Equal(t, "kni2", newSnapshotEntry.HardwareRoute.Device)
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

// Test_NeighTable_ReplaceSourceCopiesAndRetainsSnapshots verifies that input
// mutation and later replacements cannot alter previously published views.
func Test_NeighTable_ReplaceSourceCopiesAndRetainsSnapshots(t *testing.T) {
	table := NewNeighTable()
	address := netip.MustParseAddr("192.0.2.1")
	entry := makeEntry("192.0.2.99", [6]byte{2, 0, 0, 0, 0, 1}, 0)
	entry.Source = "client-owned"
	entry.UpdatedAt = time.Unix(1, 0)
	input := map[netip.Addr]NeighbourEntry{address: entry}
	changed, err := table.ReplaceSource(t.Context(), "snapshot", 50, input)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, entry, input[address])
	previous, found := table.SourceView("snapshot")
	require.True(t, found)
	oldMerged := table.View()
	oldSnapshot := table.Snapshot()
	stored, found := previous.Lookup(address)
	require.True(t, found)
	require.Equal(t, address, stored.NextHop)
	require.Empty(t, stored.Source)
	require.Equal(t, uint32(50), stored.Priority)
	require.True(t, stored.UpdatedAt.After(entry.UpdatedAt))
	clear(input)
	actual, found := table.View().Lookup(address)
	require.True(t, found)
	require.Equal(t, stored.HardwareRoute, actual.HardwareRoute)

	changed, err = table.ReplaceSource(t.Context(), "snapshot", 50, map[netip.Addr]NeighbourEntry{address: entry})
	require.NoError(t, err)
	require.False(t, changed)
	current, found := table.SourceView("snapshot")
	require.True(t, found)
	actual, found = current.Lookup(address)
	require.True(t, found)
	require.Equal(t, stored, actual)

	entry.HardwareRoute.Device = "another-device"
	changed, err = table.ReplaceSource(t.Context(), "snapshot", 100, map[netip.Addr]NeighbourEntry{address: entry})
	require.NoError(t, err)
	require.True(t, changed)
	actual, found = table.View().Lookup(address)
	require.True(t, found)
	require.Equal(t, "another-device", actual.HardwareRoute.Device)
	require.Equal(t, uint32(100), actual.Priority)
	for _, view := range []NexthopCacheView{previous, oldMerged, oldSnapshot.ViewByDevices(nil)} {
		actual, found := view.Lookup(address)
		require.True(t, found)
		require.Equal(t, stored.HardwareRoute, actual.HardwareRoute)
		require.Equal(t, stored.UpdatedAt, actual.UpdatedAt)
		require.Equal(t, stored.Priority, actual.Priority)
	}
}

// Test_NeighTable_ReplaceSourceSemanticChanges verifies that only changed
// entries receive new timestamps, including inherited but not explicit priority.
func Test_NeighTable_ReplaceSourceSemanticChanges(t *testing.T) {
	for _, test := range []struct {
		name     string
		mutate   func(*NeighbourEntry)
		priority uint32
	}{
		{name: "source MAC", priority: 100, mutate: func(entry *NeighbourEntry) { entry.HardwareRoute.SourceMAC[5]++ }},
		{name: "destination MAC", priority: 100, mutate: func(entry *NeighbourEntry) { entry.HardwareRoute.DestinationMAC[5]++ }},
		{name: "device", priority: 100, mutate: func(entry *NeighbourEntry) { entry.HardwareRoute.Device = "another-device" }},
		{name: "state", priority: 100, mutate: func(entry *NeighbourEntry) { entry.State = NeighbourState(netlink.NUD_REACHABLE) }},
		{name: "explicit priority", priority: 100, mutate: func(entry *NeighbourEntry) { entry.Priority = 20 }},
		{name: "inherited priority", priority: 200, mutate: func(entry *NeighbourEntry) {}},
	} {
		t.Run(test.name, func(t *testing.T) {
			table := NewNeighTable()
			first := makeEntry("192.0.2.1", [6]byte{2, 0, 0, 0, 0, 1}, 0)
			second := makeEntry("192.0.2.2", [6]byte{2, 0, 0, 0, 0, 2}, 7)
			input := map[netip.Addr]NeighbourEntry{first.NextHop: first, second.NextHop: second}
			changed, err := table.ReplaceSource(t.Context(), "snapshot", 100, input)
			require.NoError(t, err)
			require.True(t, changed)
			old := table.View()
			test.mutate(&first)
			input[first.NextHop] = first
			changed, err = table.ReplaceSource(t.Context(), "snapshot", test.priority, input)
			require.NoError(t, err)
			require.True(t, changed)
			prior, _ := old.Lookup(first.NextHop)
			current, _ := table.View().Lookup(first.NextHop)
			require.True(t, current.UpdatedAt.After(prior.UpdatedAt))
			prior, _ = old.Lookup(second.NextHop)
			current, _ = table.View().Lookup(second.NextHop)
			require.Equal(t, prior, current)
			changed, err = table.ReplaceSource(t.Context(), "snapshot", test.priority, input)
			require.NoError(t, err)
			require.False(t, changed)
		})
	}
}

// Test_NeighTable_ReplaceSourceCancellation verifies that a canceled caller
// cannot create, clear, replace, or acknowledge an equivalent snapshot.
func Test_NeighTable_ReplaceSourceCancellation(t *testing.T) {
	table := NewNeighTable()
	entry := makeEntry("192.0.2.1", [6]byte{2, 0, 0, 0, 0, 1}, 0)
	input := map[netip.Addr]NeighbourEntry{entry.NextHop: entry}
	changed, err := table.ReplaceSource(t.Context(), "snapshot", 100, input)
	require.NoError(t, err)
	require.True(t, changed)
	before := table.ListSources()
	oldEntries, _ := table.View().All()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, name := range []string{"snapshot", "absent"} {
		for _, entries := range []map[netip.Addr]NeighbourEntry{nil, input} {
			changed, err := table.ReplaceSource(ctx, name, 100, entries)
			require.ErrorIs(t, err, context.Canceled)
			require.False(t, changed)
			require.Equal(t, before, table.ListSources())
			currentEntries, _ := table.View().All()
			require.Equal(t, maps.Collect(oldEntries), maps.Collect(currentEntries))
		}
	}
}

// Test_NeighTable_ReplaceSourceConcurrentReaders verifies that each source view,
// merged view, and metadata response remains internally consistent at commit.
func Test_NeighTable_ReplaceSourceConcurrentReaders(t *testing.T) {
	table := NewNeighTable()
	first := makeEntry("192.0.2.1", [6]byte{2, 0, 0, 0, 0, 1}, 0)
	second := makeEntry("192.0.2.2", [6]byte{2, 0, 0, 0, 0, 2}, 0)
	snapshots := []map[netip.Addr]NeighbourEntry{
		{first.NextHop: first},
		{first.NextHop: first, second.NextHop: second},
	}
	_, err := table.ReplaceSource(t.Context(), "snapshot", 1, snapshots[0])
	require.NoError(t, err)
	var group errgroup.Group
	group.Go(func() error {
		for idx := range 100 {
			entries := snapshots[idx%len(snapshots)]
			if _, err := table.ReplaceSource(t.Context(), "snapshot", uint32(len(entries)), entries); err != nil {
				return err
			}
		}
		return nil
	})
	group.Go(func() error {
		for range 500 {
			source, _ := table.SourceView("snapshot")
			for _, view := range []NexthopCacheView{source, table.View()} {
				entries, count := view.Entries()
				for entry := range entries {
					if entry.Priority != uint32(count) {
						return fmt.Errorf("entry priority %d disagrees with snapshot size %d", entry.Priority, count)
					}
				}
			}
			for _, source := range table.ListSources() {
				if source.DefaultPriority != uint32(source.EntryCount) {
					return fmt.Errorf("source priority %d disagrees with entry count %d", source.DefaultPriority, source.EntryCount)
				}
			}
		}
		return nil
	})
	require.NoError(t, group.Wait())
}
