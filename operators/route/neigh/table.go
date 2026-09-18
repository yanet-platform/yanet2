package neigh

import (
	"errors"
	"fmt"
	"net/netip"
	"sync"

	"github.com/yanet-platform/yanet2/common/go/rcucache"
)

var (
	// ErrSourceExists is reported when creating a source whose name is taken.
	ErrSourceExists = errors.New("source already exists")
	// ErrSourceNotFound is reported when no source has the given name.
	ErrSourceNotFound = errors.New("source not found")
	// ErrBuiltInSource is reported when deleting a built-in source.
	ErrBuiltInSource = errors.New("built-in source cannot be deleted")
)

// NeighSource represents a single source of neighbour entries.
type NeighSource struct {
	// Name is the unique identifier of this source (e.g. "kernel", "static").
	Name string
	// DefaultPriority is assigned to entries that do not specify an explicit
	// priority.
	//
	// Lower value means higher preference.
	DefaultPriority uint32
	// Cache holds the entries for this source.
	Cache *NexthopCache
	// BuiltIn marks sources that cannot be deleted.
	BuiltIn bool
}

// SourceInfo contains metadata about a neighbour source.
type SourceInfo struct {
	Name            string
	DefaultPriority uint32
	EntryCount      int
	BuiltIn         bool
}

// TableOption configures NewNeighTable.
type TableOption func(*tableOptions)

// WithTableOnChanged registers a callback fired after a change that
// altered the hardware route of at least one merged nexthop.
//
// A change leaving the merged hardware routes intact, such as a state
// refresh or one in a source shadowed by a higher-priority entry, does not
// fire it. The callback runs outside the table lock and may read the
// table, while changing it from there recurses.
func WithTableOnChanged(fn func()) TableOption {
	return func(o *tableOptions) {
		o.OnChanged = fn
	}
}

type tableOptions struct {
	OnChanged func()
}

func newTableOptions() *tableOptions {
	return &tableOptions{
		OnChanged: func() {},
	}
}

// NeighTable merges multiple neighbour sources by per-entry priority.
//
// All mutations are serialized under mu. After every mutation the merged
// cache is rebuilt and atomically swapped so that readers (via View)
// never block.
type NeighTable struct {
	mu sync.Mutex
	// sources maps source name to its NeighSource.
	sources map[string]*NeighSource
	// merged is the final merged cache that consumers read via View().
	merged *NexthopCache
	// onChanged fires after a mutation changed the merged hardware routes.
	onChanged func()
}

// NewNeighTable creates a new empty NeighTable.
func NewNeighTable(options ...TableOption) *NeighTable {
	opts := newTableOptions()
	for _, o := range options {
		o(opts)
	}

	return &NeighTable{
		sources:   map[string]*NeighSource{},
		merged:    rcucache.NewEmptyCache[netip.Addr, NeighbourEntry](),
		onChanged: opts.OnChanged,
	}
}

// View returns a lock-free snapshot of the merged table.
func (m *NeighTable) View() NexthopCacheView {
	return m.merged.View()
}

// SourceView returns a lock-free snapshot of a specific source table.
func (m *NeighTable) SourceView(name string) (NexthopCacheView, bool) {
	m.mu.Lock()
	src := m.sources[name]
	m.mu.Unlock()

	if src == nil {
		return NexthopCacheView{}, false
	}

	return src.Cache.View(), true
}

// CreateSource creates a new source with the given default priority.
//
// If the source already exists, an error is returned. Set builtIn to
// true for sources that must not be deleted (e.g. "kernel", "static").
func (m *NeighTable) CreateSource(name string, defaultPriority uint32, builtIn bool) (*NeighSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.sources[name]; ok {
		return nil, fmt.Errorf("%w: %q", ErrSourceExists, name)
	}

	src := &NeighSource{
		Name:            name,
		DefaultPriority: defaultPriority,
		Cache:           rcucache.NewEmptyCache[netip.Addr, NeighbourEntry](),
		BuiltIn:         builtIn,
	}

	m.sources[name] = src
	// No need to rebuild: new source is empty.
	return src, nil
}

// UpdateSource changes the default priority of an existing source.
func (m *NeighTable) UpdateSource(name string, defaultPriority uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	src, ok := m.sources[name]
	if !ok {
		return fmt.Errorf("%w: %q", ErrSourceNotFound, name)
	}

	src.DefaultPriority = defaultPriority
	return nil
}

// DeleteSource removes a user-defined source and triggers a re-merge.
func (m *NeighTable) DeleteSource(name string) error {
	return m.update(func() error {
		src, ok := m.sources[name]
		if !ok {
			return fmt.Errorf("%w: %q", ErrSourceNotFound, name)
		}

		if src.BuiltIn {
			return fmt.Errorf("%w: %q", ErrBuiltInSource, name)
		}

		delete(m.sources, name)
		return nil
	})
}

// ListSources returns metadata about all registered sources.
func (m *NeighTable) ListSources() []SourceInfo {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]SourceInfo, 0, len(m.sources))
	for _, src := range m.sources {
		view := src.Cache.View()
		_, count := view.Entries()
		result = append(result, SourceInfo{
			Name:            src.Name,
			DefaultPriority: src.DefaultPriority,
			EntryCount:      count,
			BuiltIn:         src.BuiltIn,
		})
	}
	return result
}

// Source returns a source by name.
func (m *NeighTable) Source(name string) (*NeighSource, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	src, ok := m.sources[name]
	return src, ok
}

// Add inserts or updates entries in the specified source table and
// triggers a single re-merge.
func (m *NeighTable) Add(table string, entries []NeighbourEntry) error {
	return m.update(func() error {
		src, ok := m.sources[table]
		if !ok {
			return fmt.Errorf("%w: %q", ErrSourceNotFound, table)
		}

		for _, entry := range entries {
			if entry.Priority == 0 {
				entry.Priority = src.DefaultPriority
			}
			src.Cache.Set(entry.NextHop, entry)
		}
		return nil
	})
}

// Remove deletes entries from the specified source table and triggers
// a single re-merge.
func (m *NeighTable) Remove(table string, addrs []netip.Addr) error {
	return m.update(func() error {
		src, ok := m.sources[table]
		if !ok {
			return fmt.Errorf("%w: %q", ErrSourceNotFound, table)
		}

		for _, addr := range addrs {
			src.Cache.Delete(addr)
		}
		return nil
	})
}

// SwapSource atomically replaces all entries in the named source and triggers
// a re-merge.
//
// Entries with zero priority inherit the source's default priority.
func (m *NeighTable) SwapSource(name string, entries map[netip.Addr]NeighbourEntry) error {
	return m.update(func() error {
		src, ok := m.sources[name]
		if !ok {
			return fmt.Errorf("%w: %q", ErrSourceNotFound, name)
		}

		for addr, entry := range entries {
			if entry.Priority == 0 {
				entry.Priority = src.DefaultPriority
				entries[addr] = entry
			}
		}

		src.Cache.Swap(entries)
		return nil
	})
}

// update applies a change to one source, re-merges, and fires the change
// hook once the lock is released when the merged hardware routes differ.
//
// A failed change neither re-merges nor fires the hook.
func (m *NeighTable) update(fn func() error) error {
	changed, err := m.updateAndMerge(fn)
	if err != nil {
		return err
	}

	if changed {
		m.onChanged()
	}
	return nil
}

// updateAndMerge holds the lock across both the change and the re-merge,
// so the hook can fire without it.
func (m *NeighTable) updateAndMerge(fn func() error) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := fn(); err != nil {
		return false, err
	}
	return m.rebuildMergedCacheLocked(), nil
}

// rebuildMergedCacheLocked rebuilds the merged cache from all sources and
// reports whether the hardware route of any merged nexthop changed.
//
// Must be called with m.mu held.
func (m *NeighTable) rebuildMergedCacheLocked() bool {
	merged := map[netip.Addr]NeighbourEntry{}

	for _, src := range m.sources {
		view := src.Cache.View()
		entries, _ := view.Entries()

		for entry := range entries {
			entry.Source = src.Name
			existing, ok := merged[entry.NextHop]
			if !ok || entry.Priority < existing.Priority {
				merged[entry.NextHop] = entry
			}
		}
	}

	changed := !sameHardwareRoutes(m.merged.View(), merged)
	m.merged.Swap(merged)
	return changed
}

// sameHardwareRoutes compares the only projection a forwarding table is
// built from: which nexthops resolve, and to what hardware route.
func sameHardwareRoutes(before NexthopCacheView, after map[netip.Addr]NeighbourEntry) bool {
	if _, n := before.Entries(); n != len(after) {
		return false
	}

	for addr, entry := range after {
		previous, ok := before.Lookup(addr)
		if !ok || previous.HardwareRoute != entry.HardwareRoute {
			return false
		}
	}
	return true
}
