package balancer2

import (
	"errors"
	"fmt"
	"net"

	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
	"github.com/yanet-platform/yanet2/common/go/xnetip"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer2/bindings/go/cbalancer2"
	"github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb/v1"
)

const (
	commonCounterName = "cmn"
	l4CounterName     = "l4"
	vsCounterPrefix   = "vs"
	aclCounterPrefix  = "acl"
	realCounterPrefix = "real"
)

// build converts config to the C representation, allocates a fresh balancer
// handle, seeds per-real state and installs it.
func build(
	agent *ffi.Agent,
	name string,
	cfg *ConfigParams,
	st *SessionsState,
	prevIndex map[vsID]*vsSlot,
) (*cbalancer2.Balancer, map[vsID]*vsSlot, error) {
	if cfg.Vs == nil {
		return nil, nil, errors.New("vs configuration is required")
	}
	if cfg.Timeouts == nil {
		return nil, nil, errors.New("session timeouts are required")
	}

	vs, err := toCVSConfigs(cfg.Vs.Vs)
	if err != nil {
		return nil, nil, fmt.Errorf("convert vs: %w", err)
	}
	index, err := buildIndex(cfg.Vs.Vs, prevIndex)
	if err != nil {
		return nil, nil, fmt.Errorf("build index: %w", err)
	}
	timeouts := toCSessionTimeouts(cfg.Timeouts)

	handle, err := cbalancer2.NewBalancer(
		agent,
		name,
		st.stChain,
		timeouts,
		vs,
		commonCounterName,
		l4CounterName,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("create balancer: %w", err)
	}
	for _, slot := range index {
		if err := prepareVSReals(handle, uint32(slot.idx), slot); err != nil {
			handle.Free(agent)
			return nil, nil, fmt.Errorf("set real states: %w", err)
		}
	}
	if err := handle.Install(agent); err != nil {
		handle.Free(agent)
		return nil, nil, fmt.Errorf("install balancer: %w", err)
	}
	return handle, index, nil
}

// prepareVSReals seeds initial enabled/weight state for a virtual service's
// reals before the balancer handle is installed into the dataplane.
func prepareVSReals(handle *cbalancer2.Balancer, vsIdx uint32, slot *vsSlot) error {
	states := make([]bool, len(slot.reals))
	weights := make([]uint32, len(slot.reals))
	for _, rs := range slot.reals {
		states[rs.idx] = rs.enabled
		weights[rs.idx] = rs.effectiveWeight
	}
	if err := handle.UpdateVSReals(vsIdx, weights, states); err != nil {
		return fmt.Errorf("vs[%d]: update reals: %w", vsIdx, err)
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
			weight := uint32(0)
			effectiveWeight := uint32(0)
			if prevSlot != nil {
				if prevRealSlot, exists := prevSlot.reals[rk]; exists {
					enabled = prevRealSlot.enabled
					weight = prevRealSlot.weight
					effectiveWeight = prevRealSlot.effectiveWeight
				}
			}
			if r.Enabled != nil {
				enabled = *r.Enabled
			}
			if r.Weight != nil {
				weight = *r.Weight
				effectiveWeight = *r.Weight
			}
			slot.reals[rk] = &realSlot{
				idx:             rIdx,
				enabled:         enabled,
				weight:          weight,
				effectiveWeight: effectiveWeight,
			}
		}
		out[key] = slot
	}
	return out, nil
}

func mergeConfig(prev, next *ConfigParams) *ConfigParams {
	out := *prev
	if next.Vs != nil {
		out.Vs = next.Vs
	}
	if next.Timeouts != nil {
		out.Timeouts = next.Timeouts
	}
	if next.Addr != nil {
		out.Addr = next.Addr
	}
	if next.Wlc != nil {
		out.Wlc = next.Wlc
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
	id, err := makeVsID(v.Id)
	if err != nil {
		return cbalancer2.VSConfig{}, err
	}

	transport, err := toCTransport(v.Id.Proto)
	if err != nil {
		return cbalancer2.VSConfig{}, err
	}

	scheduler, err := toCScheduler(v.Scheduler)
	if err != nil {
		return cbalancer2.VSConfig{}, err
	}

	aclPrefix := fmt.Sprintf("%s_%s", aclCounterPrefix, id)
	realPrefix := fmt.Sprintf("%s_%s", realCounterPrefix, id)

	allowed := make([]cbalancer2.AllowedSources, len(v.AllowedSources))
	for idx, a := range v.AllowedSources {
		c, err := toCAllowedSources(a, aclPrefix)
		if err != nil {
			return cbalancer2.VSConfig{}, fmt.Errorf("allowed sources at index %d: %w", idx, err)
		}
		allowed[idx] = c
	}

	reals := make([]cbalancer2.RealConfig, len(v.Reals))
	for idx, r := range v.Reals {
		c, err := toCRealConfig(r, realPrefix)
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
		Dst:            id.addr,
		Port:           id.port,
		Transport:      transport,
		AllowedSources: allowed,
		Scheduler:      scheduler,
		Tunnel:         tunnel,
		Reals:          reals,
		FixMSS:         fixMSS,
		CounterName:    fmt.Sprintf("%s_%s", vsCounterPrefix, id),
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
		// The dataplane always runs weighted round-robin; for WLC the control
		// plane recomputes effective weights periodically in the refresh loop.
		return cbalancer2.VSSchedulerWRR, nil
	case balancerpb.VsScheduler_OP:
		return cbalancer2.VSSchedulerOP, nil
	default:
		return 0, fmt.Errorf("unsupported scheduler: %s", s)
	}
}

func toCAllowedSources(
	a *balancerpb.AllowedSources,
	counterPrefix string,
) (cbalancer2.AllowedSources, error) {
	net4s, err := filterpb.ToNet4s(a.Nets)
	if err != nil {
		return cbalancer2.AllowedSources{}, fmt.Errorf("net4s: %w", err)
	}
	net6s, err := filterpb.ToNet6s(a.Nets)
	if err != nil {
		return cbalancer2.AllowedSources{}, fmt.Errorf("net6s: %w", err)
	}
	protoPorts := a.Ports
	if a.Ports == nil {
		protoPorts = []*filterpb.PortRange{{From: 0, To: uint32((1 << 16) - 1)}}
	}
	ports, err := filterpb.ToPortRanges(protoPorts)
	if err != nil {
		return cbalancer2.AllowedSources{}, fmt.Errorf("ports: %w", err)
	}
	counterName := ""
	if a.Tag != nil {
		counterName = fmt.Sprintf("%s_%s", counterPrefix, *a.Tag)
	}
	return cbalancer2.AllowedSources{
		Net4s:       net4s,
		Net6s:       net6s,
		PortRanges:  ports,
		CounterName: counterName,
	}, nil
}

func toCRealConfig(r *balancerpb.RealConfig, counterPrefix string) (cbalancer2.RealConfig, error) {
	id, err := makeRealID(r.Id)
	if err != nil {
		return cbalancer2.RealConfig{}, err
	}
	if r.Src == nil {
		return cbalancer2.RealConfig{}, errors.New("source required")
	}
	src, err := toCNetWithMask(r.Src)
	if err != nil {
		return cbalancer2.RealConfig{}, fmt.Errorf("source: %w", err)
	}
	return cbalancer2.RealConfig{
		Dst:         id.addr,
		Src:         src,
		CounterName: fmt.Sprintf("%s_%s", counterPrefix, id),
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
