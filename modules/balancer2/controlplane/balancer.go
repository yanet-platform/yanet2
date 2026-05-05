package balancer2

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"

	"github.com/yanet-platform/yanet2/common/filterpb"
	"github.com/yanet-platform/yanet2/common/go/xnetip"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer2/bindings/go/cbalancer2"
	"github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb"
)

type ConfigParams struct {
	Vs       *balancerpb.VsConfigList
	Timeouts *balancerpb.SessionsTimeouts
	Addr     *balancerpb.AddrConfig
	Wlc      *balancerpb.WlcConfig
}

type vsID struct {
	addr  netip.Addr
	port  uint16
	proto balancerpb.TransportProto
}

type realID struct {
	addr netip.Addr
}

type realSlot struct {
	idx     int
	enabled bool
	weight  uint32
}

type vsSlot struct {
	idx   int
	reals map[realID]*realSlot
}

type ModuleConfig struct {
	handle   cbalancer2.Balancer
	name     string
	cfg      *ConfigParams
	sessions *SessionsState
	agent    *ffi.Agent
	index    map[vsID]*vsSlot
}

func NewModuleConfig(
	name string,
	agent *ffi.Agent,
	config *ConfigParams,
	st *SessionsState,
) (*ModuleConfig, error) {
	if config == nil || config.Vs == nil {
		return nil, errors.New("vs configuration is required")
	}
	if config.Timeouts == nil {
		return nil, errors.New("session timeouts are required")
	}

	vs, err := toCVSConfigs(config.Vs.Vs)
	if err != nil {
		return nil, fmt.Errorf("convert vs: %w", err)
	}
	index, err := buildIndex(config.Vs.Vs, nil)
	if err != nil {
		return nil, fmt.Errorf("build index: %w", err)
	}
	timeouts := toCSessionTimeouts(config.Timeouts)

	handle, err := cbalancer2.NewBalancer(agent, name, st.stChain, timeouts, vs)
	if err != nil {
		return nil, fmt.Errorf("create balancer: %w", err)
	}
	for _, slot := range index {
		if err := pushVSRealState(handle, uint32(slot.idx), slot); err != nil {
			handle.Free(agent)
			return nil, fmt.Errorf("seed real state: %w", err)
		}
	}
	if err := handle.Install(agent); err != nil {
		handle.Free(agent)
		return nil, fmt.Errorf("install balancer: %w", err)
	}

	return &ModuleConfig{
		handle:   *handle,
		name:     name,
		cfg:      config,
		sessions: st,
		agent:    agent,
		index:    index,
	}, nil
}

func (m *ModuleConfig) Update(newConfig *ConfigParams, st *SessionsState) error {
	merged := mergeConfig(m.cfg, newConfig)
	if merged.Vs == nil {
		return errors.New("vs configuration is required")
	}
	if merged.Timeouts == nil {
		return errors.New("session timeouts are required")
	}
	if st == nil {
		st = m.sessions
	}

	vs, err := toCVSConfigs(merged.Vs.Vs)
	if err != nil {
		return fmt.Errorf("convert vs: %w", err)
	}
	index, err := buildIndex(merged.Vs.Vs, m.index)
	if err != nil {
		return fmt.Errorf("build index: %w", err)
	}
	timeouts := toCSessionTimeouts(merged.Timeouts)

	handle, err := cbalancer2.NewBalancer(m.agent, m.name, st.stChain, timeouts, vs)
	if err != nil {
		return fmt.Errorf("create balancer: %w", err)
	}
	for _, slot := range index {
		if err := pushVSRealState(handle, uint32(slot.idx), slot); err != nil {
			handle.Free(m.agent)
			return fmt.Errorf("seed real state: %w", err)
		}
	}
	if err := handle.Install(m.agent); err != nil {
		handle.Free(m.agent)
		return fmt.Errorf("install balancer: %w", err)
	}

	m.handle.Free(m.agent)
	m.handle = *handle
	m.cfg = merged
	m.sessions = st
	m.index = index
	return nil
}

func (m *ModuleConfig) Free() {
	m.handle.Free(m.agent)
}

func (m *ModuleConfig) Params() *ConfigParams {
	return m.cfg
}

func (m *ModuleConfig) SessionsStateName() string {
	return m.sessions.Name()
}

func (m *ModuleConfig) UpdateVS(vs []*balancerpb.VsConfig) error {
	cur := m.cfg.Vs.Vs
	merged := slices.Clone(cur)
	for _, v := range vs {
		id, err := makeVsID(v.Id)
		if err != nil {
			return fmt.Errorf("update vs: %w", err)
		}
		if slot, ok := m.index[id]; ok {
			merged[slot.idx] = v
		} else {
			merged = append(merged, v)
		}
	}
	return m.Update(&ConfigParams{Vs: &balancerpb.VsConfigList{Vs: merged}}, nil)
}

func (m *ModuleConfig) DeleteVS(vs []*balancerpb.VsIdentifier) error {
	toDelete := make(map[vsID]struct{}, len(vs))
	for _, raw := range vs {
		id, err := makeVsID(raw)
		if err != nil {
			return fmt.Errorf("delete vs: %w", err)
		}
		if _, ok := m.index[id]; !ok {
			return fmt.Errorf("virtual service not found: %v", id)
		}
		toDelete[id] = struct{}{}
	}

	cur := m.cfg.Vs.Vs
	kept := make([]*balancerpb.VsConfig, 0, len(cur)-len(toDelete))
	for _, v := range cur {
		id, err := makeVsID(v.Id)
		if err != nil {
			return fmt.Errorf("delete vs: stored config invalid: %w", err)
		}
		if _, drop := toDelete[id]; drop {
			continue
		}
		kept = append(kept, v)
	}
	return m.Update(&ConfigParams{Vs: &balancerpb.VsConfigList{Vs: kept}}, nil)
}

func (m *ModuleConfig) UpdateReals(updates []*balancerpb.RealUpdate) error {
	type vsUpdate struct {
		state  bool
		weight bool
	}

	vsUpdates := make(map[int]*vsUpdate)

	for idx, update := range updates {
		vsID, err := makeVsID(update.RealId.Vs)
		if err != nil {
			return fmt.Errorf("update[%d]: vs: %w", idx, err)
		}
		realID, err := makeRealID(update.RealId.Real)
		if err != nil {
			return fmt.Errorf("update[%d]: real: %w", idx, err)
		}
		if _, found := m.index[vsID]; !found {
			return fmt.Errorf("update[%d]: vs not found", idx)
		}
		vsSlot := m.index[vsID]
		if _, found := vsSlot.reals[realID]; !found {
			return fmt.Errorf("update[%d]: vs not found", idx)
		}
		realSlot := vsSlot.reals[realID]
		if _, found := vsUpdates[vsSlot.idx]; !found {
			vsUpdates[vsSlot.idx] = &vsUpdate{}
		}
		vsUpdate := vsUpdates[vsSlot.idx]
		if update.Enable != nil {
			realSlot.enabled = *update.Enable
			vsUpdate.state = true
		}
		if update.Weight != nil {
			realSlot.weight = *update.Weight
			vsUpdate.weight = true
		}
	}

	for vsIdx, updateInfo := range vsUpdates {
		vsID, err := makeVsID(m.cfg.Vs.Vs[vsIdx].Id)
		if err != nil {
			return errors.New("internal error")
		}
		vsSlot := m.index[vsID]
		if updateInfo.state {
			states := make([]bool, len(vsSlot.reals))
			for _, realSlot := range vsSlot.reals {
				states[realSlot.idx] = realSlot.enabled
			}
			if err := m.handle.UpdateVSRealStates(uint32(vsIdx), states); err != nil {
				return fmt.Errorf("failed to update real states: %w", err)
			}
		}
		if updateInfo.weight {
			weights := make([]uint32, len(vsSlot.reals))
			for _, realSlot := range vsSlot.reals {
				weights[realSlot.idx] = realSlot.weight
			}
			if err := m.handle.UpdateVSRealWeights(uint32(vsIdx), weights); err != nil {
				return fmt.Errorf("failed to update real weights: %w", err)
			}
		}
	}

	return nil
}

func pushVSRealState(handle *cbalancer2.Balancer, vsIdx uint32, slot *vsSlot) error {
	states := make([]bool, len(slot.reals))
	weights := make([]uint32, len(slot.reals))
	for _, rs := range slot.reals {
		states[rs.idx] = rs.enabled
		weights[rs.idx] = rs.weight
	}
	if err := handle.UpdateVSRealStates(vsIdx, states); err != nil {
		return fmt.Errorf("vs[%d]: update real states: %w", vsIdx, err)
	}
	if err := handle.UpdateVSRealWeights(vsIdx, weights); err != nil {
		return fmt.Errorf("vs[%d]: update real weights: %w", vsIdx, err)
	}
	return nil
}

func buildIndex(vs []*balancerpb.VsConfig, prev map[vsID]*vsSlot) (map[vsID]*vsSlot, error) {
	out := make(map[vsID]*vsSlot, len(vs))
	for vsIdx, v := range vs {
		key, err := makeVsID(v.Id)
		if err != nil {
			return nil, fmt.Errorf("vs[%d]: %w", vsIdx, err)
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("vs[%d]: duplicate found", vsIdx)
		}
		slot := &vsSlot{idx: vsIdx, reals: make(map[realID]*realSlot, len(v.Reals))}
		var prevSlot *vsSlot
		if prev != nil {
			prevSlot = prev[key]
		}
		for rIdx, r := range v.Reals {
			rk, err := makeRealID(r.Id)
			if err != nil {
				return nil, fmt.Errorf("vs[%d]: real[%d]: %w", vsIdx, rIdx, err)
			}
			if _, dup := slot.reals[rk]; dup {
				return nil, fmt.Errorf("vs[%d]: real[%d]: duplicate found", vsIdx, rIdx)
			}
			enabled := false
			weight := r.Weight
			if prevSlot != nil {
				if prevRealSlot, exists := prevSlot.reals[rk]; exists {
					enabled = prevRealSlot.enabled
					weight = prevRealSlot.weight
				}
			}
			slot.reals[rk] = &realSlot{
				idx:     rIdx,
				enabled: enabled,
				weight:  weight,
			}
		}
		out[key] = slot
	}
	return out, nil
}

func makeVsID(id *balancerpb.VsIdentifier) (vsID, error) {
	if id == nil {
		return vsID{}, errors.New("nil vs identifier")
	}
	addr, ok := netip.AddrFromSlice(id.Addr)
	if !ok {
		return vsID{}, fmt.Errorf("invalid vs address: %x", id.Addr)
	}
	return vsID{addr: addr, port: uint16(id.Port), proto: id.Proto}, nil
}

func makeRealID(id *balancerpb.RelativeRealIdentifier) (realID, error) {
	if id == nil {
		return realID{}, errors.New("real identifier required")
	}
	addr, ok := netip.AddrFromSlice(id.Ip)
	if !ok {
		return realID{}, fmt.Errorf("invalid real address: %x", id.Ip)
	}
	return realID{addr: addr}, nil
}

func mergeConfig(prev, upd *ConfigParams) *ConfigParams {
	out := *prev
	if upd.Vs != nil {
		out.Vs = upd.Vs
	}
	if upd.Timeouts != nil {
		out.Timeouts = upd.Timeouts
	}
	if upd.Addr != nil {
		out.Addr = upd.Addr
	}
	if upd.Wlc != nil {
		out.Wlc = upd.Wlc
	}
	return &out
}

func toCSessionTimeouts(t *balancerpb.SessionsTimeouts) cbalancer2.SessionTimeouts {
	return cbalancer2.SessionTimeouts{
		TCPSynAck: t.TcpSynAck,
		TCPSyn:    t.TcpSyn,
		TCPFin:    t.TcpFin,
		TCP:       t.Tcp,
		UDP:       t.Udp,
	}
}

func toCVSConfigs(vs []*balancerpb.VsConfig) ([]cbalancer2.VSConfig, error) {
	out := make([]cbalancer2.VSConfig, len(vs))
	for idx, v := range vs {
		c, err := toCVSConfig(v)
		if err != nil {
			return nil, fmt.Errorf("vs[%d]: %w", idx, err)
		}
		out[idx] = c
	}
	return out, nil
}

func toCVSConfig(v *balancerpb.VsConfig) (cbalancer2.VSConfig, error) {
	dst, ok := netip.AddrFromSlice(v.Id.Addr)
	if !ok {
		return cbalancer2.VSConfig{}, errors.New("invalid address")
	}

	transport, err := toCTransport(v.Id.Proto)
	if err != nil {
		return cbalancer2.VSConfig{}, err
	}

	scheduler, err := toCScheduler(v.Scheduler)
	if err != nil {
		return cbalancer2.VSConfig{}, err
	}

	allowed := make([]cbalancer2.AllowedSources, len(v.AllowedSources))
	for idx, a := range v.AllowedSources {
		c, err := toCAllowedSources(a)
		if err != nil {
			return cbalancer2.VSConfig{}, fmt.Errorf("allowed sources at index %d: %w", idx, err)
		}
		allowed[idx] = c
	}

	reals := make([]cbalancer2.RealConfig, len(v.Reals))
	for idx, r := range v.Reals {
		c, err := toCRealConfig(r)
		if err != nil {
			return cbalancer2.VSConfig{}, fmt.Errorf("real at index %d: %w", idx, err)
		}
		reals[idx] = c
	}

	tunnel := cbalancer2.TunnelKindIP
	fixMSS := false
	if v.Flags != nil {
		if v.Flags.Gre {
			tunnel = cbalancer2.TunnelKindGRE
		}
		fixMSS = v.Flags.FixMss
	}

	return cbalancer2.VSConfig{
		Dst:            dst,
		Port:           uint16(v.Id.Port),
		Transport:      transport,
		AllowedSources: allowed,
		Scheduler:      scheduler,
		Tunnel:         tunnel,
		Reals:          reals,
		FixMSS:         fixMSS,
	}, nil
}

func toCTransport(p balancerpb.TransportProto) (cbalancer2.TransportProto, error) {
	switch p {
	case balancerpb.TransportProto_TCP:
		return cbalancer2.TransportTCP, nil
	case balancerpb.TransportProto_UDP:
		return cbalancer2.TransportUDP, nil
	default:
		return 0, fmt.Errorf("unsupported transport: %s", p)
	}
}

func toCScheduler(s balancerpb.VsScheduler) (cbalancer2.VSScheduler, error) {
	switch s {
	case balancerpb.VsScheduler_SH:
		return cbalancer2.VSSchedulerSH, nil
	case balancerpb.VsScheduler_WRR:
		return cbalancer2.VSSchedulerWRR, nil
	case balancerpb.VsScheduler_WLC:
		return cbalancer2.VSSchedulerWRR, nil
	case balancerpb.VsScheduler_OP:
		return cbalancer2.VSSchedulerOP, nil
	default:
		return 0, fmt.Errorf("unsupported scheduler: %s", s)
	}
}

func toCAllowedSources(a *balancerpb.AllowedSources) (cbalancer2.AllowedSources, error) {
	net4s, err := filterpb.ToNet4s(a.Nets)
	if err != nil {
		return cbalancer2.AllowedSources{}, fmt.Errorf("net4s: %w", err)
	}
	net6s, err := filterpb.ToNet6s(a.Nets)
	if err != nil {
		return cbalancer2.AllowedSources{}, fmt.Errorf("net6s: %w", err)
	}
	ports, err := filterpb.ToPortRanges(a.Ports)
	if err != nil {
		return cbalancer2.AllowedSources{}, fmt.Errorf("ports: %w", err)
	}
	tag := ""
	if a.Tag != nil {
		tag = *a.Tag
	}
	return cbalancer2.AllowedSources{
		Net4s:      net4s,
		Net6s:      net6s,
		PortRanges: ports,
		Tag:        tag,
	}, nil
}

func toCRealConfig(r *balancerpb.RealConfig) (cbalancer2.RealConfig, error) {
	dst, ok := netip.AddrFromSlice(r.Id.Ip)
	if !ok {
		return cbalancer2.RealConfig{}, errors.New("invalid address")
	}
	src, err := toCNetWithMask(r.Src)
	if err != nil {
		return cbalancer2.RealConfig{}, fmt.Errorf("source: %w", err)
	}
	return cbalancer2.RealConfig{
		Dst: dst,
		Src: src,
	}, nil
}

func toCNetWithMask(n *filterpb.IPNet) (xnetip.NetWithMask, error) {
	ipNet, err := filterpb.ToIPNet(n)
	if err != nil {
		return xnetip.NetWithMask{}, err
	}
	return xnetip.NetWithMask{
		Addr: ipNet.Addr,
		Mask: net.IPMask(ipNet.Mask.AsSlice()),
	}, nil
}
