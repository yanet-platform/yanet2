package balancer2

import (
	"math"
	"time"

	"github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb"
)

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
		if m.cfg.Vs.Vs[slot.idx].Scheduler != balancerpb.VsScheduler_WLC {
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

func (m *ModuleConfig) refreshWLC(now time.Time) {
	if m.cfg == nil || m.cfg.Vs == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	counts := m.collectSessionCounts(now)

	power, maxWeight := float64(m.cfg.Wlc.Power), m.cfg.Wlc.MaxWeight

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

		m.vsRefreshWLC(slot, counts[vid], power, maxWeight)
	}
}

func (m *ModuleConfig) vsRefreshWLC(
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
