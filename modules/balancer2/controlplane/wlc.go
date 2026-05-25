package balancer2

import (
	"math"
	"time"

	"github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb"
)

// refreshWLC recomputes effective weights for every VS whose scheduler is
// WLC, based on the live active-session counts observed at now, and pushes
// the result to the dataplane. The base weight stored in realSlot.weight is
// not changed by this method; only realSlot.effectiveWeight may move, and
// only after the dataplane update for the same VS has succeeded.
//
// Sessions for unknown VSes, unknown reals, or unsupported transports are
// silently skipped. VSes with non-WLC schedulers are left untouched. If the
// dataplane update fails for a VS, that VS's index entries are left at their
// previous effective weights and the next VS is still processed.
func (m *ModuleConfig) refreshWLC(now time.Time) {
	if m.cfg == nil || m.cfg.Vs == nil {
		return
	}

	counts := m.collectSessionCounts(now)

	power, maxWeight := wlcParams(m.cfg.Wlc)

	for _, vs := range m.cfg.Vs.Vs {
		if vs.Scheduler != balancerpb.VsScheduler_WLC {
			continue
		}
		vid, err := makeVsID(vs.Id)
		if err != nil {
			continue
		}
		slot, ok := m.index[vid]
		if !ok {
			continue
		}
		m.refreshWLCSlot(slot, counts[vid], power, maxWeight)
	}
}

// collectSessionCounts scans live sessions once and returns per-VS,
// per-real active-session counts. Sessions whose transport, VS, or real is
// not known to the current configuration are skipped.
func (m *ModuleConfig) collectSessionCounts(now time.Time) map[vsID]map[realID]uint64 {
	counts := map[vsID]map[realID]uint64{}
	for id, state := range m.sessions.IterSessions(now) {
		proto, err := toPBTransport(id.Transport)
		if err != nil {
			continue
		}
		vid := vsID{addr: id.VIP, port: id.VSPort, proto: proto}
		slot, ok := m.index[vid]
		if !ok {
			continue
		}
		rid := realID{addr: state.RealIP}
		if _, ok := slot.reals[rid]; !ok {
			continue
		}
		perVS, ok := counts[vid]
		if !ok {
			perVS = map[realID]uint64{}
			counts[vid] = perVS
		}
		perVS[rid]++
	}
	return counts
}

// wlcParams returns the (power, maxWeight) pair to use during WLC refresh.
// A nil config yields (0, 0), which collapses the formula to base weights
// via the max(1.0, ...) clamp and disables the max-weight ceiling.
func wlcParams(cfg *balancerpb.WlcConfig) (float64, uint32) {
	if cfg == nil {
		return 0, 0
	}
	return float64(cfg.Power), cfg.MaxWeight
}

// refreshWLCSlot computes new effective weights for a single WLC VS and
// pushes them to the dataplane. realSessions is the per-real live-session
// map for this VS, or nil if no live sessions matched.
//
// The dataplane is updated first; only on success are the new effective
// weights written back to the index. On failure the index is left at its
// previous effective weights so callers still observe a coherent state.
func (m *ModuleConfig) refreshWLCSlot(
	slot *vsSlot,
	realSessions map[realID]uint64,
	power float64,
	maxWeight uint32,
) {
	var totalSessions uint64
	var totalWeight uint64
	for rid, rs := range slot.reals {
		totalSessions += realSessions[rid]
		if rs.enabled {
			totalWeight += uint64(rs.weight)
		}
	}

	effective := make(map[realID]uint32, len(slot.reals))
	for rid, rs := range slot.reals {
		effective[rid] = computeEffectiveWeight(
			rs,
			realSessions[rid],
			totalSessions,
			totalWeight,
			power,
			maxWeight,
		)
	}

	weights := make([]uint32, len(slot.reals))
	states := make([]bool, len(slot.reals))
	for rid, rs := range slot.reals {
		weights[rs.idx] = effective[rid]
		states[rs.idx] = rs.enabled
	}

	if err := m.handle.UpdateVSReals(uint32(slot.idx), weights, states); err != nil {
		return
	}

	for rid, rs := range slot.reals {
		rs.effectiveWeight = effective[rid]
	}
}

// computeEffectiveWeight applies the WLC formula from balancer_new
// (modules/balancer2/balancer_new/controlplane/refresh.go:130) to a single
// real. Disabled reals keep their current effective weight so the dataplane
// selector sees no spurious change.
func computeEffectiveWeight(
	rs *realSlot,
	realSessions uint64,
	totalSessions uint64,
	totalWeight uint64,
	power float64,
	maxWeight uint32,
) uint32 {
	if !rs.enabled {
		return rs.effectiveWeight
	}
	if rs.weight == 0 {
		return 0
	}

	var eff float64
	if totalSessions == 0 || totalWeight == 0 {
		eff = float64(rs.weight)
	} else {
		ratio := float64(realSessions) * float64(totalWeight) /
			(float64(totalSessions) * float64(rs.weight))
		ratio = math.Min(ratio, 1.0)
		wlcFactor := math.Max(1.0, power*(1.0-ratio))
		eff = float64(rs.weight) * wlcFactor
	}

	if maxWeight > 0 {
		eff = math.Min(eff, float64(maxWeight))
	}
	return uint32(eff)
}
