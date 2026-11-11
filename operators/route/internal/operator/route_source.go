package operator

import (
	"net/netip"
	"sync"
	"time"

	"github.com/yanet-platform/yanet2/common/go/maptrie"
	"github.com/yanet-platform/yanet2/operators/route/internal/rib"
	"github.com/yanet-platform/yanet2/operators/route/neigh"
)

type routeSnapshot interface {
	Snapshot() map[string]*rib.RIB
}

// RouteSnapshot is the desired state the gateways apply until the next
// build: the per-module RIB dumps and the neighbour view each gateway needs
// to build its FIB.
type RouteSnapshot struct {
	// RIBs maps each module config name to its route dump.
	RIBs map[string]maptrie.MapTrie[netip.Prefix, netip.Addr, rib.RoutesList]
	// Neighbours is the neighbour view used to resolve route nexthops.
	Neighbours neigh.NexthopCacheView
}

// RouteSource builds the route operator's desired state: a fresh dump of
// the RIBs and the current neighbour-table view, which each gateway
// actuator turns into its own FIB.
type RouteSource struct {
	routeReader routeSnapshot
	neighTable  *neigh.NeighTable

	mu sync.Mutex
	// built holds the change time of every RIB as the last build saw it.
	built map[string]time.Time
}

// NewRouteSource constructs a RouteSource reading the RIBs from the
// supplied reader and the neighbours from the supplied table.
func NewRouteSource(
	neighTable *neigh.NeighTable,
	ribReader routeSnapshot,
) *RouteSource {
	return &RouteSource{
		routeReader: ribReader,
		neighTable:  neighTable,
	}
}

// Build returns the current desired state: the per-module RIB dumps and the
// neighbour view.
//
// Empty desired state remains actionable so failed applies refresh readiness
// and keep retrying until current input permits forwarding publication.
func (m *RouteSource) Build() RouteSnapshot {
	ribs := m.routeReader.Snapshot()

	// A change time is read before its dump, so a change in between lands
	// in this build and still counts as a change afterwards.
	built := make(map[string]time.Time, len(ribs))
	dumps := make(map[string]maptrie.MapTrie[netip.Prefix, netip.Addr, rib.RoutesList], len(ribs))
	for name, ribRef := range ribs {
		built[name] = ribRef.Stats().ChangedAt
		dumps[name] = ribRef.DumpRoutes()
	}
	m.setBuilt(built)

	return RouteSnapshot{RIBs: dumps, Neighbours: m.neighTable.View()}
}

// Changed reports whether a RIB changed since the last build, reported or
// not.
func (m *RouteSource) Changed() bool {
	ribs := m.routeReader.Snapshot()

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(ribs) != len(m.built) {
		return true
	}
	for name, ribRef := range ribs {
		changedAt, ok := m.built[name]
		if !ok || !ribRef.Stats().ChangedAt.Equal(changedAt) {
			return true
		}
	}
	return false
}

// setBuilt records the change times the last build saw.
func (m *RouteSource) setBuilt(built map[string]time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.built = built
}
