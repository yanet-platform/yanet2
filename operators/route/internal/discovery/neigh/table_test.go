package neigh_test

import (
	"context"
	"maps"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/operators/route/internal/discovery/neigh"
)

// neighbourEntry returns an IP-keyed entry with caller metadata.
func neighbourEntry(address, device string, priority uint32) neigh.NeighbourEntry {
	return neigh.NeighbourEntry{
		NextHop:       netip.MustParseAddr(address),
		HardwareRoute: neigh.HardwareRoute{Device: device, DestinationMAC: [6]byte{2, 0, 0, 0, 0, 1}},
		Priority:      priority, State: neigh.NeighbourStatePermanent,
		UpdatedAt: time.Unix(1, 0),
	}
}

// keyedEntries indexes a snapshot by canonical IP.
func keyedEntries(entries ...neigh.NeighbourEntry) map[netip.Addr]neigh.NeighbourEntry {
	result := map[netip.Addr]neigh.NeighbourEntry{}
	for _, entry := range entries {
		result[entry.NextHop.Unmap()] = entry
	}
	return result
}

// Test_NeighTable_IPPriority verifies that priority selects one entry per IP,
// even when source tables advertise different devices.
func Test_NeighTable_IPPriority(t *testing.T) {
	table := neigh.NewNeighTable()
	_, err := table.CreateSource("kernel", 100, true)
	require.NoError(t, err)
	_, err = table.CreateSource("static", 10, true)
	require.NoError(t, err)
	first := neighbourEntry("fe80::1", "kni0", 0)
	second := neighbourEntry("fe80::2", "kni1", 0)
	second.HardwareRoute.DestinationMAC[5] = 2
	require.NoError(t, table.SwapSource("kernel", keyedEntries(first, second)))
	preferred := first
	preferred.HardwareRoute.Device = "kni2"
	preferred.HardwareRoute.DestinationMAC[5] = 3
	require.NoError(t, table.Add("static", []neigh.NeighbourEntry{preferred}))
	view := table.View()
	_, count := view.Entries()
	require.Equal(t, 2, count)
	actual, found := view.Lookup(first.NextHop)
	require.True(t, found)
	require.Equal(t, "static", actual.Source)
	require.Equal(t, uint32(10), actual.Priority)
	require.Equal(t, preferred.HardwareRoute, actual.HardwareRoute)
	actual, found = view.Lookup(second.NextHop)
	require.True(t, found)
	require.Equal(t, "kernel", actual.Source)
	require.Equal(t, uint32(100), actual.Priority)
	require.Equal(t, second.HardwareRoute, actual.HardwareRoute)
	require.NoError(t, table.SwapSource("static", nil))
	actual, found = table.View().Lookup(first.NextHop)
	require.True(t, found)
	require.Equal(t, "kernel", actual.Source)
	require.Equal(t, first.HardwareRoute, actual.HardwareRoute)
}

// Test_NeighTable_IPRemoval verifies that removal preserves other addresses
// and sources, regardless of their device payload.
func Test_NeighTable_IPRemoval(t *testing.T) {
	table := neigh.NewNeighTable()
	_, err := table.CreateSource("kernel", 100, true)
	require.NoError(t, err)
	_, err = table.CreateSource("static", 10, true)
	require.NoError(t, err)
	first := neighbourEntry("fe80::1", "kni0", 0)
	unrelated := neighbourEntry("fe80::2", "kni0", 0)
	require.NoError(t, table.SwapSource("kernel", keyedEntries(first, unrelated)))
	first.HardwareRoute.Device = "kni1"
	require.NoError(t, table.Add("static", []neigh.NeighbourEntry{first}))
	require.NoError(t, table.Remove("kernel", []netip.Addr{first.NextHop}))
	kernel, found := table.SourceView("kernel")
	require.True(t, found)
	entries, _ := kernel.All()
	require.Equal(t, keyedEntries(neighbourEntry("fe80::2", "kni0", 100)), maps.Collect(entries))
	actual, found := table.View().Lookup(first.NextHop)
	require.True(t, found)
	require.Equal(t, "static", actual.Source)
	require.Equal(t, "kni1", actual.HardwareRoute.Device)
}

// Test_NeighTable_SourceLifecycle verifies that mutations retain source metadata
// and reject missing or protected built-in sources.
func Test_NeighTable_SourceLifecycle(t *testing.T) {
	table := neigh.NewNeighTable()
	_, err := table.CreateSource("kernel", 100, true)
	require.NoError(t, err)
	_, err = table.CreateSource("custom", 50, false)
	require.NoError(t, err)
	_, err = table.CreateSource("custom", 10, false)
	require.Error(t, err)
	entry := neighbourEntry("192.0.2.1", "kni0", 0)
	require.NoError(t, table.Add("custom", []neigh.NeighbourEntry{entry}))
	require.NoError(t, table.UpdateSource("custom", 70))
	view, found := table.SourceView("custom")
	require.True(t, found)
	_, found = view.Lookup(entry.NextHop)
	require.True(t, found)
	require.ElementsMatch(t, []neigh.SourceInfo{
		{Name: "kernel", DefaultPriority: 100, BuiltIn: true},
		{Name: "custom", DefaultPriority: 70, EntryCount: 1},
	}, table.ListSources())
	require.Error(t, table.DeleteSource("kernel"))
	_, err = table.ReplaceSource(t.Context(), "kernel", 200, nil)
	require.ErrorIs(t, err, neigh.ErrBuiltInSource)
	require.NoError(t, table.DeleteSource("custom"))
	_, count := table.View().Entries()
	require.Zero(t, count)
	_, found = table.SourceView("custom")
	require.False(t, found)
	require.Error(t, table.Add("missing", []neigh.NeighbourEntry{entry}))
	require.Error(t, table.Remove("missing", []netip.Addr{entry.NextHop}))
	require.Error(t, table.SwapSource("missing", nil))
	require.Error(t, table.UpdateSource("missing", 1))
	require.Error(t, table.DeleteSource("missing"))
}

// Test_NeighTable_ImmutableReplacement verifies that source views and filtered
// snapshots keep their old entries, provenance and timestamps across replacement.
func Test_NeighTable_ImmutableReplacement(t *testing.T) {
	table := neigh.NewNeighTable()
	first := neighbourEntry("fe80::1", "kni0", 0)
	second := neighbourEntry("fe80::2", "kni1", 0)
	input := keyedEntries(first, second)
	changed, err := table.ReplaceSource(t.Context(), "snapshot", 50, input)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, first, input[first.NextHop])
	clear(input)
	previous, found := table.SourceView("snapshot")
	require.True(t, found)
	oldMerged := table.View()
	oldFiltered := neigh.FilterByDevices(oldMerged, []string{"kni0"})
	stored, found := previous.Lookup(first.NextHop)
	require.True(t, found)
	require.True(t, stored.UpdatedAt.After(first.UpdatedAt))
	require.Empty(t, stored.Source)
	changed, err = table.ReplaceSource(t.Context(), "snapshot", 50, keyedEntries(first, second))
	require.NoError(t, err)
	require.False(t, changed)
	current, _ := table.SourceView("snapshot")
	actual, found := current.Lookup(first.NextHop)
	require.True(t, found)
	require.Equal(t, stored, actual)
	first.HardwareRoute.Device = "kni2"
	changed, err = table.ReplaceSource(t.Context(), "snapshot", 100, keyedEntries(first))
	require.NoError(t, err)
	require.True(t, changed)
	_, found = neigh.FilterByDevices(table.View(), []string{"kni0"}).Lookup(first.NextHop)
	require.False(t, found)
	for _, view := range []neigh.NexthopCacheView{previous, oldMerged, oldFiltered} {
		actual, found := view.Lookup(first.NextHop)
		require.True(t, found)
		require.Equal(t, stored.HardwareRoute, actual.HardwareRoute)
		require.Equal(t, stored.UpdatedAt, actual.UpdatedAt)
	}
}

// Test_NeighTable_CanonicalIdentity verifies that mapped IPv4 identities normalize
// on insertion/removal and duplicate replacement keys are rejected atomically.
func Test_NeighTable_CanonicalIdentity(t *testing.T) {
	table := neigh.NewNeighTable()
	_, err := table.CreateSource("static", 10, true)
	require.NoError(t, err)
	first := neighbourEntry("::ffff:192.0.2.1", "kni0", 0)
	second := neighbourEntry("192.0.2.1", "kni1", 0)
	require.NoError(t, table.Add("static", []neigh.NeighbourEntry{first, second}))
	actual, found := table.View().Lookup(first.NextHop.Unmap())
	require.True(t, found)
	require.True(t, actual.NextHop.Is4())
	require.Equal(t, "kni1", actual.HardwareRoute.Device)
	require.NoError(t, table.Remove("static", []netip.Addr{first.NextHop}))
	_, count := table.View().Entries()
	require.Zero(t, count)
	input := keyedEntries(first)
	input[first.NextHop] = first
	changed, err := table.ReplaceSource(t.Context(), "snapshot", 100, input)
	require.Error(t, err)
	require.False(t, changed)
	_, found = table.SourceView("snapshot")
	require.False(t, found)
}

// Test_NeighTable_ReplacementChanges verifies that device, MAC, state and priority
// changes refresh only the modified entry's timestamp.
func Test_NeighTable_ReplacementChanges(t *testing.T) {
	for _, test := range []struct {
		name     string
		mutate   func(*neigh.NeighbourEntry)
		priority uint32
	}{
		{name: "source MAC", priority: 100, mutate: func(entry *neigh.NeighbourEntry) { entry.HardwareRoute.SourceMAC[5]++ }},
		{name: "destination MAC", priority: 100, mutate: func(entry *neigh.NeighbourEntry) { entry.HardwareRoute.DestinationMAC[5]++ }},
		{name: "device", priority: 100, mutate: func(entry *neigh.NeighbourEntry) { entry.HardwareRoute.Device = "kni2" }},
		{name: "state", priority: 100, mutate: func(entry *neigh.NeighbourEntry) { entry.State = neigh.NeighbourState(2) }},
		{name: "explicit priority", priority: 100, mutate: func(entry *neigh.NeighbourEntry) { entry.Priority = 20 }},
		{name: "default priority", priority: 200, mutate: func(entry *neigh.NeighbourEntry) {}},
	} {
		t.Run(test.name, func(t *testing.T) {
			table := neigh.NewNeighTable()
			first := neighbourEntry("192.0.2.1", "kni0", 0)
			second := neighbourEntry("192.0.2.2", "kni0", 7)
			_, err := table.ReplaceSource(t.Context(), "snapshot", 100, keyedEntries(first, second))
			require.NoError(t, err)
			old := table.View()
			prior, _ := old.Lookup(first.NextHop)
			test.mutate(&first)
			changed, err := table.ReplaceSource(t.Context(), "snapshot", test.priority, keyedEntries(first, second))
			require.NoError(t, err)
			require.True(t, changed)
			current, _ := table.View().Lookup(first.NextHop)
			require.True(t, current.UpdatedAt.After(prior.UpdatedAt))
			prior, _ = old.Lookup(second.NextHop)
			current, _ = table.View().Lookup(second.NextHop)
			require.Equal(t, prior, current)
			changed, err = table.ReplaceSource(t.Context(), "snapshot", test.priority, keyedEntries(first, second))
			require.NoError(t, err)
			require.False(t, changed)
		})
	}
}

// Test_NeighTable_ReplacementCancellation verifies that cancelled calls cannot
// create, clear or acknowledge an unchanged source.
func Test_NeighTable_ReplacementCancellation(t *testing.T) {
	table := neigh.NewNeighTable()
	input := keyedEntries(neighbourEntry("192.0.2.1", "kni0", 0))
	_, err := table.ReplaceSource(t.Context(), "snapshot", 100, input)
	require.NoError(t, err)
	before, _ := table.View().All()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, name := range []string{"snapshot", "absent"} {
		for _, entries := range []map[netip.Addr]neigh.NeighbourEntry{nil, input} {
			changed, err := table.ReplaceSource(ctx, name, 100, entries)
			require.ErrorIs(t, err, context.Canceled)
			require.False(t, changed)
			current, _ := table.View().All()
			require.Equal(t, maps.Collect(before), maps.Collect(current))
		}
	}
}
