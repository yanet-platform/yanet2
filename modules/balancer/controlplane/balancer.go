// Package balancer implements the balancer control plane.
package balancer

import (
	"time"

	"github.com/yanet-platform/yanet2/common/go/relptr"
	yanet "github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// Protocol constants matching C netinet/in.h values.
const (
	ipprotoIP   = 0  // IPv4
	ipprotoIPv6 = 41 // IPv6
	ipprotoTCP  = 6
	ipprotoUDP  = 17
)

var errNoAgentMemory = status.Error(codes.ResourceExhausted, "no agent memory")

func (b *Balancer) Destroy() {
	handler := b.handler
	agent := b.agent
	if handler.Session_table != nil {
		agent.destroySessionTable(relptr.Deref(&handler.Session_table))
	}
	handler.free(agent)
	yanet.Free(agent.AsYanetAgent(), handler)
}

// Balancer manages a single balancer instance including its shared-memory
// packet handler, session table, and lookup indices.
type Balancer struct {
	// Pointer to the packet handler instance in the shared memory.
	// It uses relative pointers, so one can use relptr package to access it.
	handler *PacketHandler
	agent   *BalancerAgent

	realUpdateBuffer []*balancerpb.RealUpdate

	// vsIndex maps virtual service identities to their positions in the
	// shared-memory arrays. Rebuilt after every config update via buildIndexes.
	vsIndex map[vsKey]vsSlot

	// Last applied config for diffing on Update.
	config *balancerpb.BalancerConfig
}

// nullifyReusedFields clears pointers to resources that were reused (via relptr.Equate)
// by the new handler. Both old and new handlers share these resources; nullifying them
// on the old handler prevents Free from double-freeing the shared resources.
// Must be called before freeing the old handler.
func (b *Balancer) nullifyReusedFields(
	newHandler *PacketHandler,
	reuseReport *balancerpb.ReuseReport,
) {
	handler := b.handler
	services := relptr.Slice(&handler.Vs, handler.Vs_count)
	for _, vsReuse := range reuseReport.VsReuseReports {
		key := makeVsKey(vsReuse.VsIdentifier)
		slot, ok := b.vsIndex[key]
		if !ok {
			continue
		}
		if vsReuse.AclReused {
			relptr.Set(&services[slot.index].Acl, nil)
		}
		if vsReuse.SelectorReused {
			relptr.Set(&services[slot.index].Selector, nil)
		}
	}
	if reuseReport.Ipv4VsMatcherReused {
		relptr.Set(&handler.Ipv4_vs_matcher, nil)
	}
	if reuseReport.Ipv6VsMatcherReused {
		relptr.Set(&handler.Ipv6_vs_matcher, nil)
	}
	if reuseReport.Ipv4DecapFilterReused {
		relptr.Set(&handler.Decap_ipv4_filter, nil)
	}
	if reuseReport.Ipv6DecapFilterReused {
		relptr.Set(&handler.Decap_ipv6_filter, nil)
	}

	// Nullify tracker_shards on old reals that were inherited by new reals.
	// real.populate copies tracker_shards via relptr.Equate, creating shared
	// ownership. Without nullifying the old side, vs.free would free shared memory.
	nullifySharedTrackerShards(handler, newHandler)
}

// nullifySharedTrackerShards clears tracker_shards on old reals whose shards
// were inherited (via relptr.Equate) by the corresponding new real.
func nullifySharedTrackerShards(oldHandler, newHandler *PacketHandler) {
	oldVsList := relptr.Slice(&oldHandler.Vs, oldHandler.Vs_count)
	newVsList := relptr.Slice(&newHandler.Vs, newHandler.Vs_count)
	for i := range min(len(oldVsList), len(newVsList)) {
		oldReals := relptr.Slice(&oldVsList[i].Reals, oldVsList[i].Reals_count)
		newReals := relptr.Slice(&newVsList[i].Reals, newVsList[i].Reals_count)
		for j := range min(len(oldReals), len(newReals)) {
			oldTracker := relptr.Deref(&oldReals[j].Tracker_shards)
			newTracker := relptr.Deref(&newReals[j].Tracker_shards)
			if oldTracker != nil && oldTracker == newTracker {
				relptr.Set(&oldReals[j].Tracker_shards, nil)
			}
		}
	}
}

func (b *Balancer) buildIndexes() {
	services := relptr.Slice(&b.handler.Vs, b.handler.Vs_count)
	b.vsIndex = make(map[vsKey]vsSlot, b.handler.Vs_count)

	for idx := range services {
		vs := &services[idx]
		if vs.Flags&VSFlagRemoved != 0 {
			continue
		}
		slot := vsSlot{
			index:     idx,
			realSlots: make(map[realKey]int, int(vs.Reals_count)),
		}

		reals := relptr.Slice(&vs.Reals, vs.Reals_count)
		for realIdx := range reals {
			rl := &reals[realIdx]
			if rl.isRemoved() {
				continue
			}
			slot.realSlots[rl.key()] = realIdx
		}

		b.vsIndex[vs.key()] = slot
	}
}

func (b *Balancer) Config() *balancerpb.BalancerConfig {
	return b.config
}

// SessionTableCapacity returns the current session table capacity.
func (b *Balancer) SessionTableCapacity() uint64 {
	st := relptr.Deref(&b.handler.Session_table)
	if st == nil {
		return 0
	}
	return uint64(st.capacity())
}

// BufferedRealUpdates returns the currently buffered real updates.
func (b *Balancer) BufferedRealUpdates() []*balancerpb.RealUpdate {
	return b.realUpdateBuffer
}

// Update applies a new configuration to the balancer. If config.PacketHandler is nil,
// only the state (session table, WLC params) is updated in-place. Otherwise a new
// packet handler is built, installed, and the old one is freed.
func (b *Balancer) Update(
	config *balancerpb.BalancerConfig,
	now *time.Time,
) (*balancerpb.ReuseReport, error) {
	mergedStateConfig := mergeStateConfig(b.config.State, config.State)

	if config.PacketHandler == nil && now != nil {
		st := relptr.Deref(&b.handler.Session_table)
		b.handler.setState(mergedStateConfig, st)
		newStSize := int(*mergedStateConfig.SessionTableCapacity)
		if err := b.handler.resizeSessionTable(st, newStSize, *now); err != nil {
			return nil, status.Errorf(codes.Internal, "resize session table: %v", err)
		}
		return nil, nil
	}

	if err := validatePacketHandlerConfig(config.PacketHandler); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid packet handler config: %v", err)
	}

	handler, reuseReport, err := NewPacketHandler(
		config,
		b.handler.name(),
		relptr.Deref(&b.handler.Session_table),
		b.agent,
		b.handler,
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create packet handler: %v", err)
	}

	if err := b.agent.install(handler); err != nil {
		handler.free(b.agent)
		yanet.Free(b.agent.AsYanetAgent(), handler)
		return nil, status.Errorf(codes.Internal, "install handler: %v", err)
	}

	// The ordering below is critical:
	// 1. Nullify reused fields on the OLD handler so Free won't double-free shared resources.
	// 2. Forget the old handler (frees its slot in the agent's handler table).
	// 3. Register the new handler (takes the freed slot — cannot fail after Forget).
	// 4. Free the old handler's remaining (non-reused) resources.
	b.nullifyReusedFields(handler, reuseReport)

	b.agent.forget(b.handler)
	if err := b.agent.register(handler); err != nil {
		panic("register after forget should never fail: agent slot was just freed")
	}

	b.handler.free(b.agent)
	yanet.Free(b.agent.AsYanetAgent(), b.handler)

	b.handler = handler
	b.config = config

	b.buildIndexes()

	return reuseReport, nil
}

// Stable index encoding:
// A stable index is a uint64 that uniquely identifies a VS or real across config updates.
// High 32 bits = epoch (incremented when a slot is reused by a different entity).
// Low 32 bits  = config index (position in the allocated array).
// When an entity keeps its position across an update, it inherits the same stable index.
// When a new entity occupies a previously-used slot, the epoch is bumped.
func makeStableIdx(epoch uint32, configIndex uint32) uint64 {
	return uint64(epoch)<<32 | uint64(configIndex)
}

func epochOf(stableIdx uint64) uint32 {
	return uint32(stableIdx >> 32)
}

func configIndexOf(stableIdx uint64) uint32 {
	return uint32(stableIdx & 0xFFFFFFFF)
}

// NewBalancer creates a new balancer instance from the given config.
//
// It allocates a packet handler and session table in shared memory,
// populates all fields, compiles filters, registers counters, and
// installs the handler into the dataplane.
//
// On any failure, all allocated resources are freed via Destroy.
func NewBalancer(
	agent *BalancerAgent,
	name string,
	config *balancerpb.BalancerConfig,
) (*Balancer, error) {
	if err := validateBalancerConfig(config); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid config: %v", err)
	}

	stateConfig := config.State

	st := agent.createSessionTable(int(*stateConfig.SessionTableCapacity))
	if st == nil {
		return nil, errNoAgentMemory
	}

	handler, _, err := NewPacketHandler(config, name, st, agent, nil)
	if err != nil {
		agent.destroySessionTable(st)
		return nil, status.Errorf(codes.Internal, "create handler: %v", err)
	}

	// From this point on, handler is properly initialized and Destroy
	// can be called safely for cleanup on any subsequent failure.

	b := &Balancer{
		handler: handler,
		agent:   agent,
		config:  config,
	}

	// Register handler in agent storage, then install into dataplane.
	if err := agent.register(handler); err != nil {
		b.Destroy()
		return nil, status.Errorf(codes.Internal, "register handler: %v", err)
	}

	if err := agent.install(handler); err != nil {
		agent.forget(handler)
		b.Destroy()
		return nil, status.Errorf(codes.Internal, "install handler: %v", err)
	}

	b.buildIndexes()

	return b, nil
}

func (b *Balancer) UpdateVirtualServices(
	vsList []*balancerpb.VirtualService,
) (*balancerpb.ReuseReport, error) {
	currentVs := b.config.PacketHandler.Vs

	vsMap := make(map[vsKey]int, len(currentVs))
	for idx, vs := range currentVs {
		vsMap[makeVsKey(vs.Id)] = idx
	}

	newVsList := append([]*balancerpb.VirtualService(nil), currentVs...)
	for _, vs := range vsList {
		key := makeVsKey(vs.Id)
		if idx, ok := vsMap[key]; ok {
			newVsList[idx] = vs
		} else {
			newVsList = append(newVsList, vs)
		}
	}

	config := proto.Clone(b.config).(*balancerpb.BalancerConfig)
	config.PacketHandler.Vs = newVsList

	return b.Update(config, nil)
}

func (b *Balancer) DeleteVirtualServices(
	vsList []*balancerpb.VirtualService,
) (*balancerpb.ReuseReport, error) {
	for idx, vs := range vsList {
		k := makeVsKey(vs.Id)
		if _, ok := b.vsIndex[k]; !ok {
			return nil, status.Errorf(codes.NotFound, "virtual service at index %d not found", idx)
		}
	}

	deletedVs := make(map[vsKey]struct{})
	for _, vs := range vsList {
		k := makeVsKey(vs.Id)
		deletedVs[k] = struct{}{}
	}

	newVsList := make([]*balancerpb.VirtualService, 0, len(b.config.PacketHandler.Vs))
	for _, vs := range b.config.PacketHandler.Vs {
		k := makeVsKey(vs.Id)
		if _, ok := deletedVs[k]; !ok {
			newVsList = append(newVsList, vs)
		}
	}

	config := proto.Clone(b.config).(*balancerpb.BalancerConfig)
	config.PacketHandler.Vs = newVsList

	return b.Update(config, nil)
}

func (b *Balancer) GetState(
	handlerRef *balancerpb.PacketHandlerRef,
	filter *balancerpb.Filter,
	includeCounters bool,
) ([]*balancerpb.BalancerState, error) {
	if err := validateFilter(filter); err != nil {
		return nil, err
	}

	matcher := newFilterMatcher(filter)
	dpConfig := b.agent.AsYanetAgent().DPConfig()
	workers := dpConfig.WorkerCount()
	balancerName := b.handler.name()

	if !includeCounters {
		state := b.buildState(workers, &matcher, nil)
		matcher.filterReals(state)
		compactBalancerState(state)
		return []*balancerpb.BalancerState{state}, nil
	}

	var results []*balancerpb.BalancerState

	for position := range dpConfig.AllModulePositions("balancer") {
		if position.ModuleName != balancerName {
			continue
		}
		if !matchesHandlerRef(handlerRef, &position) {
			continue
		}

		state := b.buildState(workers, &matcher, &position)
		b.applyCounters(state, dpConfig, &position)
		matcher.filterReals(state)
		compactBalancerState(state)
		results = append(results, state)
	}

	return results, nil
}

func (b *Balancer) buildState(
	workers uint32,
	matcher *filterMatcher,
	position *yanet.ModuleReference,
) *balancerpb.BalancerState {
	services := relptr.Slice(&b.handler.Vs, b.handler.Vs_count)

	state := &balancerpb.BalancerState{
		BalancerName:  b.handler.name(),
		L4Stats:       &balancerpb.L4Stats{},
		CommonStats:   &balancerpb.CommonStats{},
		IcmpIpv4Stats: &balancerpb.IcmpStats{},
		IcmpIpv6Stats: &balancerpb.IcmpStats{},
	}
	if position != nil {
		state.Ref = &balancerpb.PacketHandlerRef{
			Device:   &position.Device,
			Pipeline: &position.Pipeline,
			Function: &position.Function,
			Chain:    &position.Chain,
		}
	}

	state.VirtualServices = make([]*balancerpb.VsState, len(services))
	for vsIdx := range services {
		vs := &services[vsIdx]
		if vs.isRemoved() {
			continue
		}
		if matcher.hasVsFilter && !matcher.matchVsID(vs.id()) {
			continue
		}
		state.VirtualServices[vsIdx] = vs.state(workers)
	}

	return state
}

func (b *Balancer) applyCounters(
	state *balancerpb.BalancerState,
	dpConfig *yanet.DPConfig,
	position *yanet.ModuleReference,
) {
	counters := dpConfig.ModuleCounters(
		position.Device,
		position.Pipeline,
		position.Function,
		position.Chain,
		"balancer",
		b.handler.name(),
		[]string{},
	)
	for _, counter := range counters {
		applyCounter(state, counter)
	}
}

func matchesHandlerRef(ref *balancerpb.PacketHandlerRef, pos *yanet.ModuleReference) bool {
	if ref == nil {
		return true
	}
	if ref.Device != nil && *ref.Device != pos.Device {
		return false
	}
	if ref.Pipeline != nil && *ref.Pipeline != pos.Pipeline {
		return false
	}
	if ref.Function != nil && *ref.Function != pos.Function {
		return false
	}
	if ref.Chain != nil && *ref.Chain != pos.Chain {
		return false
	}
	return true
}

func (m *filterMatcher) filterReals(state *balancerpb.BalancerState) {
	if !m.hasRealFilter {
		return
	}
	for _, vsState := range state.VirtualServices {
		if vsState == nil {
			continue
		}
		for realIdx, realState := range vsState.Reals {
			if realState != nil && !m.matchRealID(realState.Id) {
				vsState.Reals[realIdx] = nil
			}
		}
	}
}

func (b *Balancer) FlushRealUpdates() (int, error) {
	if len(b.realUpdateBuffer) == 0 {
		return 0, nil
	}

	updates := b.realUpdateBuffer
	b.realUpdateBuffer = nil

	return b.UpdateReals(updates, false)
}

func (b *Balancer) UpdateReals(updates []*balancerpb.RealUpdate, buffer bool) (int, error) {
	if buffer {
		b.realUpdateBuffer = append(b.realUpdateBuffer, updates...)
		return len(updates), nil
	}

	services := relptr.Slice(&b.handler.Vs, b.handler.Vs_count)
	affectedVs := make(map[vsKey]int)

	for updateIdx, update := range updates {
		serviceKey := makeVsKey(update.RealId.Vs)
		serviceSlot, ok := b.vsIndex[serviceKey]
		if !ok {
			return 0, status.Errorf(
				codes.NotFound,
				"real update at index %d: virtual service not found",
				updateIdx,
			)
		}
		realSlot, ok := serviceSlot.realSlots[makeRealKey(update.RealId.Real)]
		if !ok {
			return 0, status.Errorf(
				codes.NotFound,
				"real update at index %d: real not found",
				updateIdx,
			)
		}
		vs := &services[serviceSlot.index]
		reals := relptr.Slice(&vs.Reals, vs.Reals_count)
		r := &reals[realSlot]
		if update.Weight != nil {
			r.Weight = *update.Weight
		}
		if update.Enable != nil {
			if *update.Enable {
				r.Flags |= RealFlagEnabled
			} else {
				r.Flags &^= uint8(RealFlagEnabled)
			}
		}
		affectedVs[serviceKey] = serviceSlot.index
	}

	for _, idx := range affectedVs {
		vs := &services[idx]
		if err := vs.updateRealSelector(&b.handler.Rcu, b.agent); err != nil {
			return 0, status.Errorf(
				codes.Internal,
				"failed to update ring for some virtual services: %v",
				err,
			)
		}
	}

	return len(updates), nil
}

func mergeStateConfig(old, update *balancerpb.StateConfig) *balancerpb.StateConfig {
	result := &balancerpb.StateConfig{
		SessionTableCapacity:      old.SessionTableCapacity,
		SessionTableMaxLoadFactor: old.SessionTableMaxLoadFactor,
		Wlc:                       old.Wlc,
		RefreshPeriod:             old.RefreshPeriod,
	}
	if update == nil {
		return result
	}
	if update.SessionTableCapacity != nil {
		result.SessionTableCapacity = update.SessionTableCapacity
	}
	if update.SessionTableMaxLoadFactor != nil {
		result.SessionTableMaxLoadFactor = update.SessionTableMaxLoadFactor
	}
	if update.Wlc != nil {
		mergedWlc := &balancerpb.WlcConfig{
			Power:     old.Wlc.Power,
			MaxWeight: old.Wlc.MaxWeight,
		}
		if update.Wlc.Power != nil {
			mergedWlc.Power = update.Wlc.Power
		}
		if update.Wlc.MaxWeight != nil {
			mergedWlc.MaxWeight = update.Wlc.MaxWeight
		}
		result.Wlc = mergedWlc
	}
	if update.RefreshPeriod != nil {
		result.RefreshPeriod = update.RefreshPeriod
	}
	return result
}
