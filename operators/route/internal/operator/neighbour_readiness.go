package operator

import (
	"context"
	"sync"
	"time"

	"github.com/yanet-platform/yanet2/common/go/readiness"
	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
	"github.com/yanet-platform/yanet2/operators/route/internal/discovery/neigh"
)

// NeighbourReadiness commits complete input and its freshness together,
// independently of FIB apply.
type NeighbourReadiness struct {
	mu         sync.Mutex
	table      string
	maxAge     time.Duration
	receivedAt time.Time
	generation uint64
	tracker    *readiness.Tracker
}

// NewNeighbourReadiness starts with unknown input until a complete snapshot arrives.
func NewNeighbourReadiness(table string, maxAge time.Duration, tracker *readiness.Tracker) *NeighbourReadiness {
	m := &NeighbourReadiness{table: table, maxAge: maxAge, tracker: tracker}
	m.observe()
	return m
}

// ReplaceSnapshot commits content and its authorization in one critical section.
//
// Empty and equivalent snapshots refresh input age. Only semantic changes
// advance the generation; restored availability also requests a reconcile wake.
func (m *NeighbourReadiness) ReplaceSnapshot(
	ctx context.Context,
	neighbours *neigh.NeighTable,
	table string,
	priority uint32,
	entries map[neigh.Key]neigh.NeighbourEntry,
) (changed bool, recovered bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	changed, err = neighbours.ReplaceSource(ctx, table, priority, entries)
	if err != nil || table != m.table {
		return changed, false, err
	}
	recovered = m.receivedAt.IsZero() || time.Since(m.receivedAt) >= m.maxAge
	m.receivedAt = time.Now()
	if changed {
		m.generation++
	}
	m.tracker.Set("neighbours", readinesspb.State_STATE_READY)
	return changed, recovered, nil
}

// RemoveTable invalidates authorization atomically with expected-source deletion.
func (m *NeighbourReadiness) RemoveTable(neighbours *neigh.NeighTable, table string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := neighbours.DeleteSource(table); err != nil {
		return err
	}
	if table != m.table {
		return nil
	}
	m.receivedAt = time.Time{}
	m.generation++
	m.tracker.SetWithReason("neighbours", readinesspb.State_STATE_NOT_READY, &readinesspb.Reason{Code: "SYNCING"})
	return nil
}

// Available checks freshness at capture time without waiting for a sampling tick.
func (m *NeighbourReadiness) Available() bool {
	_, available := m.Generation()
	return available
}

// Generation identifies the complete input whose freshness authorizes a snapshot.
func (m *NeighbourReadiness) Generation() (uint64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.generation, !m.receivedAt.IsZero() && time.Since(m.receivedAt) < m.maxAge
}

func (m *NeighbourReadiness) observe() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.receivedAt.IsZero() {
		m.tracker.SetWithReason("neighbours", readinesspb.State_STATE_NOT_READY, &readinesspb.Reason{Code: "SYNCING"})
	} else if time.Since(m.receivedAt) >= m.maxAge {
		m.tracker.SetWithReason("neighbours", readinesspb.State_STATE_NOT_READY, &readinesspb.Reason{Code: "STALE"})
	}
}

// Run expires silent input while leaving the last committed data available for diagnostics.
func (m *NeighbourReadiness) Run(ctx context.Context) error {
	ticker := time.NewTicker(max(time.Millisecond, min(time.Second, m.maxAge/4)))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			m.observe()
		}
	}
}
