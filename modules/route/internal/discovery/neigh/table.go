package neigh

import (
	"fmt"
	"net/netip"
	"sync"

	"github.com/yanet-platform/yanet2/modules/route/internal/discovery"
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
}

// NewNeighTable creates a new empty NeighTable.
func NewNeighTable() *NeighTable {
	return &NeighTable{
		sources: map[string]*NeighSource{},
		merged:  discovery.NewEmptyCache[netip.Addr, NeighbourEntry](),
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
		return nil, fmt.Errorf("source %q already exists", name)
	}

	src := &NeighSource{
		Name:            name,
		DefaultPriority: defaultPriority,
		Cache:           discovery.NewEmptyCache[netip.Addr, NeighbourEntry](),
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
		return fmt.Errorf("source %q not found", name)
	}

	src.DefaultPriority = defaultPriority
	return nil
}

// DeleteSource removes a user-defined source and triggers a re-merge.
func (m *NeighTable) DeleteSource(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	src, ok := m.sources[name]
	if !ok {
		return fmt.Errorf("source %q not found", name)
	}

	if src.BuiltIn {
		return fmt.Errorf("cannot delete built-in source %q", name)
	}

	delete(m.sources, name)
	m.rebuildMergedCacheLocked()
	return nil
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
	m.mu.Lock()
	defer m.mu.Unlock()

	src, ok := m.sources[table]
	if !ok {
		return fmt.Errorf("source %q not found", table)
	}

	for _, entry := range entries {
		if entry.Priority == 0 {
			entry.Priority = src.DefaultPriority
		}
		src.Cache.Set(entry.NextHop, entry)
	}

	m.rebuildMergedCacheLocked()
	return nil
}

// Remove deletes entries from the specified source table and triggers
// a single re-merge.
func (m *NeighTable) Remove(table string, addrs []netip.Addr) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	src, ok := m.sources[table]
	if !ok {
		return fmt.Errorf("source %q not found", table)
	}

	for _, addr := range addrs {
		src.Cache.Delete(addr)
	}

	m.rebuildMergedCacheLocked()
	return nil
}

// SwapSource atomically replaces all entries in the named source and triggers
// a re-merge.
//
// Entries with zero priority inherit the source's default priority.
func (m *NeighTable) SwapSource(name string, entries map[netip.Addr]NeighbourEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	src, ok := m.sources[name]
	if !ok {
		return fmt.Errorf("source %q not found", name)
	}

	for addr, entry := range entries {
		if entry.Priority == 0 {
			entry.Priority = src.DefaultPriority
			entries[addr] = entry
		}
	}

	src.Cache.Swap(entries)
	m.rebuildMergedCacheLocked()
	return nil
}

// rebuildMergedCacheLocked rebuilds the merged cache from all sources.
//
// Must be called with m.mu held.
func (m *NeighTable) rebuildMergedCacheLocked() {
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

	m.merged.Swap(merged)
}
