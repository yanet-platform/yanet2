// Package balancer2 is the controlplane for balancer module
package balancer2

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer2/bindings/go/cbalancer2"
	balancerpb "github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb/v1"
)

type ConfigParams struct {
	Vs       *balancerpb.VsConfigList
	Timeouts *balancerpb.SessionsTimeouts
	// Addr is not yet propagated to the dataplane;
	// ICMP source/decap support is not implemented.
	Addr *balancerpb.AddrConfig
	Wlc  *balancerpb.WlcConfig
}

type vsID struct {
	addr  netip.Addr
	port  uint16
	proto balancerpb.TransportProto
}

func (m vsID) String() string {
	addrPort := netip.AddrPortFrom(m.addr, m.port)
	switch m.proto {
	case balancerpb.TransportProto_TCP:
		return fmt.Sprintf("%s/tcp", addrPort)
	case balancerpb.TransportProto_UDP:
		return fmt.Sprintf("%s/udp", addrPort)
	}
	// unreachable: unknown protos rejected on id create.
	return fmt.Sprintf("%s/unknown", addrPort)
}

// vsIDFromString parses a virtual-service identifier string back into a vsID.
//
// Recognizes "addr:port/tcp" and "addr:port/udp"; anything else, including a
// missing slash, empty parts, extra slashes, or an unknown protocol, is rejected.
func vsIDFromString(id string) (vsID, error) {
	addrPortStr, proto, ok := strings.Cut(id, "/")
	if !ok {
		return vsID{}, fmt.Errorf("invalid vs id %q: missing '/'", id)
	}

	addrPort, err := netip.ParseAddrPort(addrPortStr)
	if err != nil {
		return vsID{}, fmt.Errorf("invalid vs id %q: %w", id, err)
	}

	var transport balancerpb.TransportProto
	switch proto {
	case "tcp":
		transport = balancerpb.TransportProto_TCP
	case "udp":
		transport = balancerpb.TransportProto_UDP
	default:
		return vsID{}, fmt.Errorf("invalid vs id %q: unknown proto %q", id, proto)
	}

	return vsID{
		addr:  addrPort.Addr(),
		port:  addrPort.Port(),
		proto: transport,
	}, nil
}

type realID struct {
	addr netip.Addr
}

func (m realID) String() string {
	return m.addr.String()
}

// realIDFromString parses a real-server identifier string back into a realID.
//
// Expects a bare IP address; addr:port strings, empty input, and any other
// deviation are rejected.
func realIDFromString(id string) (realID, error) {
	if id == "" {
		return realID{}, errors.New("invalid real id: empty")
	}
	addr, err := netip.ParseAddr(id)
	if err != nil {
		return realID{}, fmt.Errorf("invalid real id %q: %w", id, err)
	}
	return realID{addr: addr}, nil
}

type realSlot struct {
	idx     int
	enabled bool
	// weight is the configured/base runtime weight (set from
	// RealConfig.Weight or RealUpdate.Weight). WLC refresh must not
	// change it.
	weight uint32
	// effectiveWeight is the value pushed to the dataplane selector;
	// WLC refresh may override it independently of the base weight.
	effectiveWeight uint32
}

type vsSlot struct {
	idx   int
	reals map[realID]*realSlot
}

type ModuleConfig struct {
	handle   *cbalancer2.Balancer
	name     string
	cfg      *ConfigParams
	sessions *SessionsState
	agent    *ffi.Agent
	index    map[vsID]*vsSlot
	wlcLoop  *wlcRefreshLoop
	mu       sync.Mutex
}

func NewModuleConfig(
	name string,
	agent *ffi.Agent,
	config *ConfigParams,
	sessions *SessionsState,
) (*ModuleConfig, error) {
	if config == nil {
		return nil, errors.New("configuration is required")
	}

	handle, index, err := build(agent, name, config, sessions, nil)
	if err != nil {
		return nil, err
	}

	mc := &ModuleConfig{
		handle:   handle,
		name:     name,
		cfg:      config,
		sessions: sessions,
		agent:    agent,
		index:    index,
		mu:       sync.Mutex{},
	}

	mc.wlcLoop = newWLCRefreshLoop(mc)
	mc.wlcLoop.Reset(context.Background())

	return mc, nil
}

func (m *ModuleConfig) Update(newConfig *ConfigParams, st *SessionsState) error {
	// Stop before acquiring the lock: the refresh goroutine takes m.mu inside
	// its tick handler, so driving the loop lifecycle outside the lock avoids
	// a deadlock between Stop's drain wait and the goroutine's lock attempt.
	m.wlcLoop.Stop()
	m.mu.Lock()
	defer m.wlcLoop.Reset(context.Background())
	defer m.mu.Unlock()

	merged := mergeConfig(m.cfg, newConfig)
	if st == nil {
		st = m.sessions
	}

	handle, index, err := build(m.agent, m.name, merged, st, m.index)
	if err != nil {
		return err
	}

	m.handle.Free(m.agent)
	m.handle = handle
	m.cfg = merged
	m.sessions = st
	m.index = index

	return nil
}

func (m *ModuleConfig) Free() {
	// Stop before acquiring the lock for the same reason as in Update.
	m.wlcLoop.Stop()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handle.Free(m.agent)
}

func (m *ModuleConfig) Params() *ConfigParams {
	return m.cfg
}

func (m *ModuleConfig) SessionsStateName() string {
	return m.sessions.Name()
}

func (m *ModuleConfig) UpdateVS(vs []*balancerpb.VsConfig) error {
	merged, err := func() ([]*balancerpb.VsConfig, error) {
		m.mu.Lock()
		defer m.mu.Unlock()

		cur := m.cfg.Vs.Vs
		merged := slices.Clone(cur)
		for _, v := range vs {
			id, err := makeVsID(v.Id)
			if err != nil {
				return nil, fmt.Errorf("update vs: %w", err)
			}
			if slot, ok := m.index[id]; ok {
				merged[slot.idx] = v
			} else {
				merged = append(merged, v)
			}
		}
		return merged, nil
	}()
	if err != nil {
		return err
	}

	return m.Update(&ConfigParams{Vs: &balancerpb.VsConfigList{Vs: merged}}, nil)
}

func (m *ModuleConfig) DeleteVS(vs []*balancerpb.VsIdentifier) error {
	kept, err := func() ([]*balancerpb.VsConfig, error) {
		m.mu.Lock()
		defer m.mu.Unlock()

		toDelete := make(map[vsID]struct{}, len(vs))
		for _, raw := range vs {
			id, err := makeVsID(raw)
			if err != nil {
				return nil, fmt.Errorf("delete vs: %w", err)
			}
			if _, ok := m.index[id]; !ok {
				return nil, fmt.Errorf("virtual service not found: %v", id)
			}
			toDelete[id] = struct{}{}
		}

		cur := m.cfg.Vs.Vs
		kept := make([]*balancerpb.VsConfig, 0, len(cur)-len(toDelete))
		for _, v := range cur {
			id, err := makeVsID(v.Id)
			if err != nil {
				return nil, fmt.Errorf("delete vs: stored config invalid: %w", err)
			}
			if _, drop := toDelete[id]; drop {
				continue
			}
			kept = append(kept, v)
		}
		return kept, nil
	}()
	if err != nil {
		return err
	}

	return m.Update(&ConfigParams{Vs: &balancerpb.VsConfigList{Vs: kept}}, nil)
}

type vsUpdate struct {
	id    vsID
	reals map[realID]*realSlot
}

func (m *ModuleConfig) UpdateReals(updates []*balancerpb.RealUpdate) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	staged, err := m.stageRealUpdates(updates)
	if err != nil {
		return err
	}
	return m.commitRealUpdates(staged)
}

func (m *ModuleConfig) stageRealUpdates(
	updates []*balancerpb.RealUpdate,
) (map[int]*vsUpdate, error) {
	staged := map[int]*vsUpdate{}
	for idx, update := range updates {
		if update == nil || update.RealId == nil {
			return nil, fmt.Errorf("update[%d]: real identifier required", idx)
		}
		vid, err := makeVsID(update.RealId.Vs)
		if err != nil {
			return nil, fmt.Errorf("update[%d]: vs: %w", idx, err)
		}
		rid, err := makeRealID(update.RealId.Real)
		if err != nil {
			return nil, fmt.Errorf("update[%d]: real: %w", idx, err)
		}
		slot, ok := m.index[vid]
		if !ok {
			return nil, fmt.Errorf("update[%d]: vs not found", idx)
		}
		next, ok := staged[slot.idx]
		if !ok {
			cloned := make(map[realID]*realSlot, len(slot.reals))
			for k, v := range slot.reals {
				cp := *v
				cloned[k] = &cp
			}
			next = &vsUpdate{
				id:    vid,
				reals: cloned,
			}
			staged[slot.idx] = next
		}
		rs, ok := next.reals[rid]
		if !ok {
			return nil, fmt.Errorf("update[%d]: real not found", idx)
		}
		if update.Enable != nil {
			rs.enabled = *update.Enable
		}
		if update.Weight != nil {
			rs.weight = *update.Weight
			rs.effectiveWeight = *update.Weight
		}
	}
	return staged, nil
}

// commitRealUpdates pushes staged changes to the dataplane and writes
// each successful push back to index, so on a partial failure the
// index always reflects what the dataplane currently holds. Updates
// are applied in vsIdx order so retries are reproducible.
func (m *ModuleConfig) commitRealUpdates(staged map[int]*vsUpdate) error {
	order := make([]int, 0, len(staged))
	for vsIdx := range staged {
		order = append(order, vsIdx)
	}
	sort.Ints(order)
	for _, vsIdx := range order {
		info := staged[vsIdx]
		index := m.index[info.id].reals
		states := make([]bool, len(info.reals))
		for _, rs := range info.reals {
			states[rs.idx] = rs.enabled
		}
		weights := make([]uint32, len(info.reals))
		for _, rs := range info.reals {
			weights[rs.idx] = rs.effectiveWeight
		}
		if err := m.handle.UpdateVSReals(uint32(vsIdx), weights, states); err != nil {
			return fmt.Errorf("vs[%d]: update reals: %w", vsIdx, err)
		}
		for k, rs := range info.reals {
			index[k].enabled = rs.enabled
			index[k].weight = rs.weight
			index[k].effectiveWeight = rs.effectiveWeight
		}
		m.syncVSConfigReals(vsIdx, info.reals)
	}
	return nil
}

// syncVSConfigReals mirrors successful UpdateReals changes into the stored
// config snapshot so a later full rebuild (for example via UpdateVS on another
// virtual service) does not resurrect stale real enabled/weight values.
func (m *ModuleConfig) syncVSConfigReals(vsIdx int, reals map[realID]*realSlot) {
	if m.cfg == nil || m.cfg.Vs == nil || vsIdx < 0 || vsIdx >= len(m.cfg.Vs.Vs) {
		return
	}
	vs := m.cfg.Vs.Vs[vsIdx]
	if vs == nil {
		return
	}
	for _, rs := range reals {
		if rs.idx < 0 || rs.idx >= len(vs.Reals) {
			continue
		}
		realCfg := vs.Reals[rs.idx]
		if realCfg == nil {
			continue
		}
		enabled := rs.enabled
		weight := rs.weight
		realCfg.Enabled = &enabled
		realCfg.Weight = &weight
	}
}

func makeVsID(id *balancerpb.VsIdentifier) (vsID, error) {
	if id == nil {
		return vsID{}, errors.New("identifier required")
	}
	addr, ok := netip.AddrFromSlice(id.Addr)
	if !ok {
		return vsID{}, fmt.Errorf("invalid vs address: %x", id.Addr)
	}
	if id.Proto != balancerpb.TransportProto_TCP && id.Proto != balancerpb.TransportProto_UDP {
		return vsID{}, fmt.Errorf("invalid transport proto: %v", id.Proto)
	}
	if id.Port > math.MaxUint16 {
		return vsID{}, fmt.Errorf("port out of range: %d", id.Port)
	}
	return vsID{addr: addr, port: uint16(id.Port), proto: id.Proto}, nil
}

func makeRealID(id *balancerpb.RelativeRealIdentifier) (realID, error) {
	if id == nil {
		return realID{}, errors.New("identifier required")
	}
	addr, ok := netip.AddrFromSlice(id.Ip)
	if !ok {
		return realID{}, fmt.Errorf("invalid real address: %x", id.Ip)
	}
	if id.Port > math.MaxUint16 {
		return realID{}, fmt.Errorf("port out of range: %d", id.Port)
	}
	// real ports not used for now
	return realID{addr: addr}, nil
}

func (m *ModuleConfig) GetState(
	handleRef *balancerpb.PacketHandlerRef,
	filter *balancerpb.Filter,
	now time.Time,
) []*balancerpb.BalancerState {
	dpConfig := m.agent.DPConfig()

	matcher := newStateFilter(filter)

	states := make([]*balancerpb.BalancerState, 0)
	for position := range dpConfig.AllModulePositions("balancer2") {
		if position.ModuleName != m.name {
			continue
		}
		if !matchesHandlerRef(handleRef, &position) {
			continue
		}

		m.mu.Lock()
		state, lookup := m.buildBaseState(&position, matcher)
		sess := m.sessions
		m.mu.Unlock()

		counters := dpConfig.ModuleCounters(
			position.Device,
			position.Pipeline,
			position.Function,
			position.Chain,
			"balancer2",
			m.name,
			nil,
		)
		applyCounters(state, lookup, counters)

		applySessions(state, lookup, sess.IterSessions(now))

		states = append(states, state)
	}

	return states
}

func matchesHandlerRef(ref *balancerpb.PacketHandlerRef, module *ffi.ModuleReference) bool {
	if ref == nil {
		return true
	}
	if ref.Device != nil && *ref.Device != module.Device {
		return false
	}
	if ref.Pipeline != nil && *ref.Pipeline != module.Pipeline {
		return false
	}
	if ref.Function != nil && *ref.Function != module.Function {
		return false
	}
	if ref.Chain != nil && *ref.Chain != module.Chain {
		return false
	}
	return true
}
