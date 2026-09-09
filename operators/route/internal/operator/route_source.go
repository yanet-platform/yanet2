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

type neighbourSnapshot interface {
	Snapshot() neigh.TableSnapshot
}

// RouteSnapshot is the desired state for one reconcile pass: the
// per-module RIB dumps and the neighbour view each gateway needs to build
// its FIB.
type RouteSnapshot struct {
	// RIBs maps each module config name to its route dump.
	RIBs map[string]maptrie.MapTrie[netip.Prefix, netip.Addr, rib.RoutesList]
	// Neighbours retains individual sources so gateways can filter by device
	// before equal next hops are merged.
	Neighbours           neigh.TableSnapshot
	NeighbourScopeSource string
}

// RouteSource is the operator.StateSource[RouteSnapshot] used by the route
// operator.
//
// On every Snapshot it pulls a fresh dump of the RIBs from the route
// reader and the current neighbour-table view, which each gateway actuator
// turns into its own FIB.
//
// Wake is signalled by the wake callbacks wired into RouteService and
// NeighbourService whenever their state mutates — it preempts the
// reconcile loop's sleep so the next pass picks up the change without
// waiting for the steady-state interval.
type RouteSource struct {
	routeReader routeSnapshot
	neighTable  neighbourSnapshot
	wakeCh      chan struct{}
	remoteInput *NeighbourReadiness
	scopeSource string
}

// NewRouteSource constructs a RouteSource bound to the supplied
// neighbour table with its own buffered wake channel.
//
// It reads RIB snapshots from the supplied reader and uses it for all
// reconcile targets.
func NewRouteSource(
	neighTable neighbourSnapshot,
	ribReader routeSnapshot,
	options ...RouteSourceOption,
) *RouteSource {
	opts := &routeSourceOptions{}
	for _, option := range options {
		option(opts)
	}
	return &RouteSource{
		routeReader: ribReader,
		neighTable:  neighTable,
		wakeCh:      make(chan struct{}, 1),
		scopeSource: opts.ScopeSource,
		remoteInput: opts.RemoteInput,
	}
}

type routeSourceOptions struct {
	ScopeSource string
	RemoteInput *NeighbourReadiness
}

// RouteSourceOption configures neighbour provenance and optional input gating.
type RouteSourceOption func(*routeSourceOptions)

// WithRouteSourceNeighbours binds snapshots to the selected input namespace.
func WithRouteSourceNeighbours(source string, input *NeighbourReadiness) RouteSourceOption {
	return func(options *routeSourceOptions) { options.ScopeSource, options.RemoteInput = source, input }
}

// Snapshot preserves the last dataplane state while required remote input is unknown or stale.
func (m *RouteSource) Snapshot() (RouteSnapshot, bool) {
	select {
	case <-m.wakeCh:
	default:
	}

	var generation uint64
	if m.remoteInput != nil {
		var available bool
		generation, available = m.remoteInput.Generation()
		if !available {
			return RouteSnapshot{}, false
		}
	}
	neighbours := m.neighTable.Snapshot()
	if m.remoteInput != nil {
		current, available := m.remoteInput.Generation()
		if _, present := neighbours.SourceView(m.scopeSource); !present || !available || current != generation {
			return RouteSnapshot{}, false
		}
	}
	ribs := m.routeReader.Snapshot()

	dumps := make(map[string]maptrie.MapTrie[netip.Prefix, netip.Addr, rib.RoutesList], len(ribs))
	for name, ribRef := range ribs {
		dumps[name] = ribRef.DumpRoutes()
	}
	return RouteSnapshot{RIBs: dumps, Neighbours: neighbours, NeighbourScopeSource: m.scopeSource}, true
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
