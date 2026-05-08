package operator

import (
	"github.com/yanet-platform/yanet2/operators/route/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/operators/route/internal/rib"
)

type routeSnapshot interface {
	Snapshot() map[string]*rib.RIB
}

// RouteSource is the operator.StateSource[[]FIB] used by the route
// operator.
//
// On every Snapshot it pulls a fresh snapshot of the RIBs from the
// RouteSource reader, joins them with the current neighbour-table view and
// builds a per-RIB FIB. The resulting slice is the desired state for
// the next reconcile pass.
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

// Snapshot builds the current desired FIB set.
//
// The route operator publishes its single network function on every
// reconcile pass, so an empty FIB set is still a valid state worth
// applying — Snapshot always returns ok=true. The framework would
// otherwise skip the apply pass on an empty slice and the function
// would never be republished while no RIBs exist.
func (m *RouteSource) Snapshot() ([]FIB, bool) {
	select {
	case <-m.wakeCh:
	default:
	}

	ribs := m.routeReader.Snapshot()
	view := m.neighTable.View()

	fibs := make([]FIB, 0, len(ribs))
	for name, ribRef := range ribs {
		fib, _ := BuildFIB(ribRef.DumpRoutes(), view)
		fib.Name = name
		fibs = append(fibs, fib)
	}
	return fibs, true
}

func (m *RouteSource) Wake() <-chan struct{} {
	return m.wakeCh
}

func (m *RouteSource) Advance(fibs []FIB) {}

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
