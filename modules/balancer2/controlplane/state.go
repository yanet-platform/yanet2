package balancer2

import (
	"iter"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer2/bindings/go/cbalancer2"
	"github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb"
)

type stateLookup struct {
	vs map[vsID]*vsLookup
}

type vsLookup struct {
	state *balancerpb.VsState
	reals map[realID]*balancerpb.RealState
}

func newStateLookup() stateLookup {
	return stateLookup{vs: map[vsID]*vsLookup{}}
}

func (m stateLookup) findVs(key vsID) *balancerpb.VsState {
	entry, ok := m.vs[key]
	if !ok {
		return nil
	}
	return entry.state
}

func (m stateLookup) findReal(vsKey vsID, realKey realID) *balancerpb.RealState {
	entry, ok := m.vs[vsKey]
	if !ok {
		return nil
	}
	return entry.reals[realKey]
}

func (m *ModuleConfig) buildBaseState(
	position *ffi.ModuleReference,
	matcher stateFilter,
) (*balancerpb.BalancerState, stateLookup) {
	state := &balancerpb.BalancerState{
		ConfigName:        m.name,
		SessionsStateName: m.sessions.Name(),
		Ref: &balancerpb.PacketHandlerRef{
			Device:   &position.Device,
			Pipeline: &position.Pipeline,
			Function: &position.Function,
			Chain:    &position.Chain,
		},
		Addr:                m.cfg.Addr,
		LastPacketTimestamp: timestamppb.New(time.Unix(0, 0)),
	}
	lookup := newStateLookup()
	if m.cfg.Vs == nil {
		return state, lookup
	}

	state.Vs = make([]*balancerpb.VsState, 0)
	for _, vs := range m.cfg.Vs.Vs {
		if !matcher.matchVs(vs.Id) {
			continue
		}
		state.Vs = append(state.Vs, m.buildVsState(vs, matcher, lookup))
	}
	return state, lookup
}

func (m *ModuleConfig) buildVsState(
	vs *balancerpb.VsConfig,
	matcher stateFilter,
	lookup stateLookup,
) *balancerpb.VsState {
	vsState := &balancerpb.VsState{
		Config: &balancerpb.VsConfig{
			Id:        vs.Id,
			Scheduler: vs.Scheduler,
			Flags:     vs.Flags,
			Peers:     vs.Peers,
		},
		Reals:               make([]*balancerpb.RealState, 0, len(vs.Reals)),
		LastPacketTimestamp: timestamppb.New(time.Unix(0, 0)),
	}

	vid, vidErr := makeVsID(vs.Id)
	var (
		slot  *vsSlot
		reals map[realID]*balancerpb.RealState
	)
	if vidErr == nil {
		slot = m.index[vid]
		reals = make(map[realID]*balancerpb.RealState, len(vs.Reals))
		lookup.vs[vid] = &vsLookup{state: vsState, reals: reals}
	}

	for _, r := range vs.Reals {
		if !matcher.matchReal(r.Id) {
			continue
		}
		cfgReal := &balancerpb.RealConfig{
			Id:      r.Id,
			Weight:  r.Weight,
			Enabled: r.Enabled,
			Src:     r.Src,
		}
		rs := &balancerpb.RealState{
			Config:              cfgReal,
			LastPacketTimestamp: timestamppb.New(time.Unix(0, 0)),
		}
		rid, ridErr := makeRealID(r.Id)
		if ridErr == nil {
			if slot != nil {
				if rSlot, ok := slot.reals[rid]; ok {
					rs.Enabled = rSlot.enabled
					rs.EffectiveWeight = uint64(rSlot.effectiveWeight)
					// Real runtime state is sourced from current index, not
					// from stored config optional fields.
					cfgReal.Enabled = boolPtr(rSlot.enabled)
					cfgReal.Weight = uint32Ptr(rSlot.weight)
				}
			}
			if reals != nil {
				reals[rid] = rs
			}
		}
		vsState.Reals = append(vsState.Reals, rs)
	}
	return vsState
}

// applySessions scans live sessions yielded by IterSessions(now) once and
// folds their stats into the already-built BalancerState.
func applySessions(
	state *balancerpb.BalancerState,
	lookup stateLookup,
	iter iter.Seq2[cbalancer2.SessionID, cbalancer2.SessionState],
) {
	balancerMax := state.LastPacketTimestamp.AsTime()
	for id, sessionState := range iter {
		proto, err := toPBTransport(id.Transport)
		if err != nil {
			continue
		}
		vsKey := vsID{addr: id.VIP, port: id.VSPort, proto: proto}
		entry, ok := lookup.vs[vsKey]
		if !ok {
			continue
		}
		realKey := realID{addr: sessionState.RealIP}
		realState, ok := entry.reals[realKey]
		if !ok {
			continue
		}

		realState.ActiveSessions++
		entry.state.ActiveSessions++
		state.ActiveSessions++

		ts := sessionState.LastPacketTimestamp
		if ts.After(realState.LastPacketTimestamp.AsTime()) {
			realState.LastPacketTimestamp = timestamppb.New(ts)
		}
		if ts.After(entry.state.LastPacketTimestamp.AsTime()) {
			entry.state.LastPacketTimestamp = timestamppb.New(ts)
		}
		if ts.After(balancerMax) {
			balancerMax = ts
			state.LastPacketTimestamp = timestamppb.New(ts)
		}
	}
}

func applyCounters(state *balancerpb.BalancerState,
	lookup stateLookup, counters []ffi.CounterInfo,
) {
	for _, counter := range counters {
		applyCounter(state, lookup, counter)
	}
}

// applyCounter dispatches a single dataplane counter to its destination on
// state. Counters that fail to parse or that target unknown VS/reals are
// silently dropped.
func applyCounter(
	state *balancerpb.BalancerState,
	lookup stateLookup,
	counter ffi.CounterInfo,
) {
	name := counter.Name
	switch {
	case name == commonCounterName:
		values := aggregateCounterValues(counter.Values)
		if c := cbalancer2.ParseCommonCounter(values); c != nil {
			state.CommonStats = commonCounterToProto(c)
		}
	case name == l4CounterName:
		values := aggregateCounterValues(counter.Values)
		if c := cbalancer2.ParseL4Counter(values); c != nil {
			state.L4Stats = l4CounterToProto(c)
		}
	case strings.HasPrefix(name, vsCounterPrefix+"_"):
		vid, err := vsIDFromString(strings.TrimPrefix(name, vsCounterPrefix+"_"))
		if err != nil {
			return
		}
		vsState := lookup.findVs(vid)
		if vsState == nil {
			return
		}
		values := aggregateCounterValues(counter.Values)
		if c := cbalancer2.ParseVsCounter(values); c != nil {
			vsState.Stats = vsCounterToProto(c)
		}
	case strings.HasPrefix(name, aclCounterPrefix+"_"):
		vsKey, tag, ok := splitACLCounterName(name)
		if !ok {
			return
		}
		vid, err := vsIDFromString(vsKey)
		if err != nil {
			return
		}
		vsState := lookup.findVs(vid)
		if vsState == nil {
			return
		}
		values := aggregateCounterValues(counter.Values)
		if len(values) == 0 {
			return
		}
		vsState.AllowedSourcesStats = append(
			vsState.AllowedSourcesStats,
			&balancerpb.AllowedSourcesStats{
				Tag:    tag,
				Passes: values[0],
			},
		)
	case strings.HasPrefix(name, realCounterPrefix+"_"):
		vsKey, realKey, ok := splitRealCounterName(name)
		if !ok {
			return
		}
		vid, err := vsIDFromString(vsKey)
		if err != nil {
			return
		}
		rid, err := realIDFromString(realKey)
		if err != nil {
			return
		}
		realState := lookup.findReal(vid, rid)
		if realState == nil {
			return
		}
		values := aggregateCounterValues(counter.Values)
		if c := cbalancer2.ParseRealCounter(values); c != nil {
			realState.Stats = realCounterToProto(c)
		}
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func uint32Ptr(v uint32) *uint32 {
	return &v
}
