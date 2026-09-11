package neigh

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"sync"
	"time"

	"github.com/yanet-platform/yanet2/common/go/rcucache"
)

// ErrBuiltInSource indicates that a complete replacement targeted a built-in.
var ErrBuiltInSource = errors.New("cannot replace built-in neighbour source")

// ValidateSourceName checks the bounded namespace for complete replacements.
func ValidateSourceName(name string) error {
	if len(name) == 0 || len(name) > 128 {
		return errors.New("table name must contain 1..128 bytes")
	}
	for idx, character := range name {
		alphanumeric := character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' || character >= '0' && character <= '9'
		if !alphanumeric && (idx == 0 || character != '.' && character != '_' && character != '-') {
			return errors.New("table name must start with an ASCII letter or digit and contain only letters, digits, '.', '_' or '-'")
		}
	}
	return nil
}

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
		merged:  rcucache.NewEmptyCache[netip.Addr, NeighbourEntry](),
	}
}

// View returns a lock-free snapshot of the merged table.
func (m *NeighTable) View() NexthopCacheView {
	return m.merged.View()
}

// SourceView returns a lock-free snapshot of a specific source table.
func (m *NeighTable) SourceView(name string) (NexthopCacheView, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	src := m.sources[name]

	if src == nil {
		return NexthopCacheView{}, false
	}

	return src.Cache.View(), true
}

// ReplaceSource commits a complete user source and its priority together.
//
// The input is copied, and existing snapshots remain immutable. Equivalent
// entries keep their timestamps; the result reports only semantic changes.
// Cancellation observed before the commit leaves entries and metadata intact.
func (m *NeighTable) ReplaceSource(
	ctx context.Context,
	name string,
	defaultPriority uint32,
	entries map[netip.Addr]NeighbourEntry,
) (bool, error) {
	if err := ValidateSourceName(name); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return false, err
	}
	source := m.sources[name]
	if source != nil && source.BuiltIn {
		return false, fmt.Errorf("%w: %q", ErrBuiltInSource, name)
	}
	var previous NexthopCacheView
	changed := source == nil || source.DefaultPriority != defaultPriority
	if source != nil {
		previous = source.Cache.View()
		_, count := previous.Entries()
		changed = changed || count != len(entries)
	}
	next := map[netip.Addr]NeighbourEntry{}
	now := time.Now()
	for key, entry := range entries {
		key = key.Unmap()
		if _, duplicate := next[key]; duplicate {
			return false, errors.New("duplicate canonical neighbour key")
		}
		entry.NextHop = key
		entry.Source = ""
		if entry.Priority == 0 {
			entry.Priority = defaultPriority
		}
		old, found := previous.Lookup(key)
		if found && old.HardwareRoute == entry.HardwareRoute &&
			old.Priority == entry.Priority && old.State == entry.State {
			entry.UpdatedAt = old.UpdatedAt
		} else {
			entry.UpdatedAt = now
			changed = true
		}
		next[key] = entry
	}
	if !changed {
		return false, ctx.Err()
	}
	replacement := &NeighSource{
		Name: name, DefaultPriority: defaultPriority,
		Cache: rcucache.NewCache(next),
	}
	sources := maps.Clone(m.sources)
	sources[name] = replacement
	merged := mergeSources(sources)
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.sources[name] = replacement
	m.merged.Swap(merged)
	return true, nil
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

	next := copyView(src.Cache.View())
	for _, entry := range entries {
		entry.NextHop = entry.NextHop.Unmap()
		if entry.Priority == 0 {
			entry.Priority = src.DefaultPriority
		}
		next[entry.NextHop] = entry
	}

	src.Cache.Swap(next)
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

	next := copyView(src.Cache.View())
	for _, address := range addrs {
		delete(next, address.Unmap())
	}

	src.Cache.Swap(next)
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

	next := make(map[netip.Addr]NeighbourEntry, len(entries))
	for key, entry := range entries {
		key = key.Unmap()
		entry.NextHop = key
		if entry.Priority == 0 {
			entry.Priority = src.DefaultPriority
		}
		next[key] = entry
	}

	src.Cache.Swap(next)
	m.rebuildMergedCacheLocked()
	return nil
}

// rebuildMergedCacheLocked rebuilds the merged cache from all sources.
//
// Must be called with m.mu held.
func (m *NeighTable) rebuildMergedCacheLocked() {
	m.merged.Swap(mergeSources(m.sources))
}

func mergeSources(sources map[string]*NeighSource) map[netip.Addr]NeighbourEntry {
	merged := map[netip.Addr]NeighbourEntry{}
	for _, source := range sources {
		entries, _ := source.Cache.View().All()
		for key, entry := range entries {
			entry.NextHop = key
			entry.Source = source.Name
			existing, ok := merged[key]
			if !ok || entry.Priority < existing.Priority ||
				(entry.Priority == existing.Priority && entry.Source < existing.Source) {
				merged[key] = entry
			}
		}
	}
	return merged
}

func copyView(view NexthopCacheView) map[netip.Addr]NeighbourEntry {
	entries, _ := view.All()
	return maps.Collect(entries)
}
