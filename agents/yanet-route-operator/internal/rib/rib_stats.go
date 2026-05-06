package rib

import (
	"sync/atomic"
	"time"
)

// RIBStats maintains counters for a RIB.
type RIBStats struct {
	prefixes  atomic.Int64
	routes    atomic.Int64
	changedAt atomic.Int64
}

// NewRIBStats creates a RIBStats with changedAt set to the current time.
func NewRIBStats() *RIBStats {
	m := &RIBStats{}
	m.changedAt.Store(time.Now().UnixNano())
	return m
}

// Snapshot returns a point-in-time snapshot of the counters.
//
// Note that the snapshot is not atomic, so it may not be consistent with the
// current state of the RIB.
func (m *RIBStats) Snapshot() RIBStatsSnapshot {
	return RIBStatsSnapshot{
		Prefixes:  int(m.prefixes.Load()),
		Routes:    int(m.routes.Load()),
		ChangedAt: time.Unix(0, m.changedAt.Load()),
	}
}

// OnPrefixAdded increments the prefix counter.
func (m *RIBStats) OnPrefixAdded() {
	m.prefixes.Add(1)
}

// OnPrefixRemoved decrements the prefix counter.
func (m *RIBStats) OnPrefixRemoved() {
	m.prefixes.Add(-1)
}

// OnRouteAdded increments the route counter by delta.
func (m *RIBStats) OnRouteAdded(delta int) {
	m.routes.Add(int64(delta))
}

// OnRouteRemoved decrements the route counter by delta.
func (m *RIBStats) OnRouteRemoved(delta int) {
	m.routes.Add(-int64(delta))
}

// OnChanged records the current time as the last mutation timestamp.
func (m *RIBStats) OnChanged() {
	m.changedAt.Store(time.Now().UnixNano())
}

// RIBStatsSnapshot is an immutable copy of RIB counters.
type RIBStatsSnapshot struct {
	Prefixes  int
	Routes    int
	ChangedAt time.Time
}
