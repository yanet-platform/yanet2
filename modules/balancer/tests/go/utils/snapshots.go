package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

type RealSnapshot struct {
	ActiveSessions uint64
	Stats          *balancerpb.RealStats
}

type VsSnapshot struct {
	Stats *balancerpb.VsStats
	Reals map[RealID]RealSnapshot
}

func CaptureVsSnapshots(
	t *testing.T,
	ts *TestSetup,
	vsToCapture []*balancerpb.VirtualService,
) map[VsID]VsSnapshot {
	t.Helper()
	state, err := ts.Balancer.GetState(nil, nil, true, ts.Mock.CurrentTime())
	assert.NoError(t, err)
	assert.Len(t, state, 1)

	stateByID := make(map[VsID]*balancerpb.VsState, len(state[0].VirtualServices))
	for _, vsState := range state[0].VirtualServices {
		stateByID[VsIDFromPb(vsState.Id)] = vsState
	}

	snapshots := make(map[VsID]VsSnapshot, len(vsToCapture))
	for _, vs := range vsToCapture {
		id := VsIDFromPb(vs.Id)
		vsState, ok := stateByID[id]
		if !ok {
			continue
		}
		snap := VsSnapshot{
			Reals: make(map[RealID]RealSnapshot, len(vsState.Reals)),
		}
		snap.Stats = vsState.Stats
		for _, r := range vsState.Reals {
			rid := RealIDFromPb(r.Id)
			snap.Reals[rid] = RealSnapshot{
				ActiveSessions: r.ActiveSessions,
				Stats:          r.RealStats,
			}
		}
		snapshots[id] = snap
	}
	return snapshots
}

// VerifyInheritedStats asserts that every VS present in both snapshots and current
// state has the same number packet counters. VS missing from
// current state are silently skipped. Reals no longer
// present are also skipped.
func VerifyInheritedStats(
	t *testing.T,
	ts *TestSetup,
	snapshots map[VsID]VsSnapshot,
) {
	t.Helper()
	state, err := ts.Balancer.GetState(nil, nil, true, ts.Mock.CurrentTime())
	assert.NoError(t, err)
	assert.Len(t, state, 1)

	currentByID := make(map[VsID]*balancerpb.VsState, len(state[0].VirtualServices))
	for _, vsState := range state[0].VirtualServices {
		currentByID[VsIDFromPb(vsState.Id)] = vsState
	}

	for vsID, snap := range snapshots {
		vsState, ok := currentByID[vsID]
		if !ok {
			continue
		}
		assert.True(t, VsStatsEquals(vsState.Stats, snap.Stats),
			"VS %s: stats not inherited after update", vsID.String())

		realByID := make(map[RealID]*balancerpb.RealState, len(vsState.Reals))
		for _, r := range vsState.Reals {
			realByID[RealIDFromPb(r.Id)] = r
		}
		for realID, realSnap := range snap.Reals {
			r, ok := realByID[realID]
			if !ok {
				continue
			}
			assert.Equal(
				t,
				r.ActiveSessions,
				realSnap.ActiveSessions,
				"VS %s, real %s: active sessions not inherited after update",
				vsID.String(),
				realID.String(),
			)
			assert.True(
				t,
				RealStatsEqual(r.RealStats, realSnap.Stats),
				"VS %s, real %s: stats not inherited after update",
				vsID.String(),
				realID.String(),
			)
		}
	}
}
