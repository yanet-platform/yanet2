package operator

import (
	"net/netip"

	"github.com/yanet-platform/yanet2/common/go/maptrie"
	"github.com/yanet-platform/yanet2/operators/route/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/operators/route/internal/rib"
)

type routeSnapshot interface {
	Snapshot() map[string]*rib.RIB
}

// RouteSnapshot is the desired state for one reconcile pass: the
// per-module RIB dumps and the neighbour view each gateway needs to build
// its FIB.
type RouteSnapshot struct {
	// RIBs maps each module config name to its route dump.
	RIBs map[string]maptrie.MapTrie[netip.Prefix, netip.Addr, rib.RoutesList]
	// Neighbours is the neighbour view used to resolve route nexthops.
	Neighbours neigh.NexthopCacheView
}

// RouteSource is the operator.StateSource[RouteSnapshot] used by the route
// operator.
//
// On every Snapshot it pulls a fresh dump of the RIBs from the route
// reader and the current neighbour-table view, which each gateway actuator
// turns into its own FIB.
//
// Wake is signalled by the wake callbacks wired into RouteService and
// NeighbourService whenever their state mutates; it preempts the
// reconcile loop's sleep so the next pass picks up the change without
// waiting for the steady-state interval.
type RouteSource struct {
	routeReader routeSnapshot
	neighTable  *neigh.NeighTable
	wakeCh      chan struct{}
}

// NewRouteSource constructs a RouteSource bound to the supplied
// neighbour table with its own buffered wake channel.
//
// It reads RIB snapshots from the supplied reader and uses it for all
// reconcile targets.
func NewRouteSource(
	neighTable *neigh.NeighTable,
	ribReader routeSnapshot,
) *RouteSource {
	return &RouteSource{
		routeReader: ribReader,
		neighTable:  neighTable,
		wakeCh:      make(chan struct{}, 1),
	}
}

// Snapshot returns the current desired state: the per-module RIB dumps
// and the neighbour view.
//
// It always reports ok=true, even with no RIBs: the operator republishes
// its network function on every reconcile pass, and an empty state would
// otherwise make the framework skip the pass and never republish.
func (m *RouteSource) Snapshot() (RouteSnapshot, bool) {
	select {
	case <-m.wakeCh:
	default:
	}

	ribs := m.routeReader.Snapshot()

	dumps := make(map[string]maptrie.MapTrie[netip.Prefix, netip.Addr, rib.RoutesList], len(ribs))
	for name, ribRef := range ribs {
		dumps[name] = ribRef.DumpRoutes()
	}
	return RouteSnapshot{RIBs: dumps, Neighbours: m.neighTable.View()}, true
}

func (m *RouteSource) Wake() <-chan struct{} {
	return m.wakeCh
}

func (m *RouteSource) Advance(snapshot RouteSnapshot) {}

// WakeFunc returns a non-blocking sender suitable for wiring into the
// RouteService and NeighbourService OnChanged callbacks.
func (m *RouteSource) WakeFunc() func() {
	wakeCh := m.wakeCh

	return func() {
		select {
		case wakeCh <- struct{}{}:
		default:
		}
	}
}
