package operator

import (
	"context"
	"sync"
	"time"

	"github.com/yanet-platform/yanet2/common/go/readiness"
	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
)

// NeighbourReadiness observes complete expected-source input independently of FIB apply.
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

// OnSnapshotReceived refreshes input age even for empty or unchanged snapshots.
func (m *NeighbourReadiness) OnSnapshotReceived(table string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if table != m.table {
		return
	}
	m.receivedAt = time.Now()
	m.generation++
	m.tracker.Set("neighbours", readinesspb.State_STATE_READY)
}

// OnTableRemoved requires a new complete replacement after expected-source deletion.
func (m *NeighbourReadiness) OnTableRemoved(table string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if table != m.table {
		return
	}
	m.receivedAt = time.Time{}
	m.generation++
	m.tracker.SetWithReason("neighbours", readinesspb.State_STATE_NOT_READY, &readinesspb.Reason{Code: "SYNCING"})
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
