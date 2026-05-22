package balancer2

import (
	"strings"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer2/bindings/go/cbalancer2"
	"github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb"
)

type stateLookup struct {
	vs map[string]*vsLookup
}

type vsLookup struct {
	state *balancerpb.VsState
	reals map[string]*balancerpb.RealState
}

func newStateLookup() stateLookup {
	return stateLookup{vs: map[string]*vsLookup{}}
}

func (m stateLookup) findVs(key string) *balancerpb.VsState {
	entry, ok := m.vs[key]
	if !ok {
		return nil
	}
	return entry.state
}

func (m stateLookup) findReal(vsKey, realKey string) *balancerpb.RealState {
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
		Addr: m.cfg.Addr,
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
		Reals: make([]*balancerpb.RealState, 0, len(vs.Reals)),
	}

	vid, vidErr := makeVsID(vs.Id)
	var (
		slot  *vsSlot
		reals map[string]*balancerpb.RealState
	)
	if vidErr == nil {
		slot = m.index[vid]
		reals = make(map[string]*balancerpb.RealState, len(vs.Reals))
		lookup.vs[vid.String()] = &vsLookup{state: vsState, reals: reals}
	}

	for _, r := range vs.Reals {
		if !matcher.matchReal(r.Id) {
			continue
		}
		rs := &balancerpb.RealState{
			Config: r,
		}
		rid, ridErr := makeRealID(r.Id)
		if ridErr == nil {
			if slot != nil {
				if rSlot, ok := slot.reals[rid]; ok {
					rs.Enabled = rSlot.enabled
					rs.EffectiveWeight = uint64(rSlot.weight)
				}
			}
			if reals != nil {
				reals[rid.String()] = rs
			}
		}
		vsState.Reals = append(vsState.Reals, rs)
	}
	return vsState
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
		vsKey := strings.TrimPrefix(name, vsCounterPrefix+"_")
		vsState := lookup.findVs(vsKey)
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
		vsState := lookup.findVs(vsKey)
		if vsState == nil {
			return
		}
		values := aggregateCounterValues(counter.Values)
		if len(values) == 0 {
			return
		}
		vsState.AllowedSourcesStats = append(vsState.AllowedSourcesStats, &balancerpb.AllowedSourcesStats{
			Tag:    tag,
			Passes: values[0],
		})
	case strings.HasPrefix(name, realCounterPrefix+"_"):
		vsKey, realKey, ok := splitRealCounterName(name)
		if !ok {
			return
		}
		realState := lookup.findReal(vsKey, realKey)
		if realState == nil {
			return
		}
		values := aggregateCounterValues(counter.Values)
		if c := cbalancer2.ParseRealCounter(values); c != nil {
			realState.Stats = realCounterToProto(c)
		}
	}
}
