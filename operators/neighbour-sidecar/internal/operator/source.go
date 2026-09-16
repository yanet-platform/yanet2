package operator

import (
	"sync/atomic"

	"github.com/yanet-platform/yanet2/common/go/readiness"
	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
	"github.com/yanet-platform/yanet2/operators/route/neigh"
)

const publicationScope = "publication"

// NeighbourSource exposes successful observations to the common reconciler.
type NeighbourSource struct {
	table   *neigh.NeighTable
	tracker *readiness.Tracker
	synced  atomic.Bool
	wake    chan struct{}
}

// NewNeighbourSource remains idle until discovery completes its first dump.
func NewNeighbourSource(table *neigh.NeighTable, tracker *readiness.Tracker) *NeighbourSource {
	return &NeighbourSource{table: table, tracker: tracker, wake: make(chan struct{}, 1)}
}

// OnHealthy wakes publication after the monitor commits a complete observation.
func (m *NeighbourSource) OnHealthy() {
	m.synced.Store(true)
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// Snapshot holds the last complete table stable across an outgoing RPC.
func (m *NeighbourSource) Snapshot() (neigh.NexthopCacheView, bool) {
	if !m.synced.Load() {
		return neigh.NexthopCacheView{}, false
	}
	return m.table.View(), true
}

// Wake coalesces notifications while an earlier snapshot is being sent.
func (m *NeighbourSource) Wake() <-chan struct{} {
	return m.wake
}

// Advance records an acknowledged publication and retains the latest observation.
func (m *NeighbourSource) Advance(neigh.NexthopCacheView) {
	m.tracker.Set(publicationScope, readinesspb.State_STATE_READY)
}
