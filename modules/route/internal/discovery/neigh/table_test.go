package neigh

import (
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
