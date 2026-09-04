package operator

import "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/route"

// State is one reconcile pass's static-route state. Initialized distinguishes
// startup from an explicitly empty complete snapshot.
type State struct {
	Routes      []route.Route
	Initialized bool
	RouteUpdate *route.Update
}

// Source exposes the route store as a steady-state common operator source.
type Source struct {
	store *route.Store
}

// NewSource creates a source backed by store.
func NewSource(store *route.Store) *Source {
	return &Source{store: store}
}

// Snapshot always requests a pass so links and neighbours receive periodic
// full reconciliation even before the first route stream completes.
func (m *Source) Snapshot() (State, bool) {
	routes, initialized, update := m.store.SnapshotUpdate()
	return State{
		Routes:      routes,
		Initialized: initialized,
		RouteUpdate: update,
	}, true
}

// Wake forwards the store's coalescing notification channel.
func (m *Source) Wake() <-chan struct{} {
	return m.store.Wake()
}

// Advance is a no-op because the store holds a persistent latest snapshot.
func (m *Source) Advance(State) {}
