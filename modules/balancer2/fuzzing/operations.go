// Package fuzzing — deterministic operation generator.
//
// OperationGenerator produces a stream of Operation values driven by an
// effective seed and the UpdateVsEvery cadence N. Operation selection
// follows a strict precedence:
//
//   - DeleteVS on op % (2*N) == 0
//   - UpdateVS on op % N == 0
//   - UpdateReals otherwise
//
// The generator never calls RPCs and never mutates the model; it inspects
// model state (active set, original reals, lower bounds) to choose
// operation payloads that respect the 80-100% bounds documented in the
// plan. The runner (Task 7) is responsible for applying successful
// operations back into the model.
package fuzzing

import (
	"fmt"
	"math/rand"
	"net"
	"sort"

	balancerpb "github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb/v1"
)

// OperationType identifies the operation flavour. Callers switch on this
// to dispatch the appropriate RPC and model commit path.
type OperationType int

const (
	// OpUpdate performs bootstrap config update for the first operation.
	OpUpdate OperationType = iota
	// OpUpdateVS replaces the desired state of a VS: scheduler, flags,
	// allowed sources, and the active subset of reals.
	OpUpdateVS
	// OpDeleteVS removes one or more VSes from the active set.
	OpDeleteVS
	// OpDeleteVSNoop is the lower-bound delete cadence variant: it
	// generates an empty-list DeleteVSRequest so the runner still emits
	// the RPC and the GetState pairing, but no VS is actually removed.
	OpDeleteVSNoop
	// OpUpdateReals applies enabled/weight changes to an active VS's
	// existing real subset.
	OpUpdateReals
)

// RealMember is the desired state of a single real inside an UpdateVS
// operation. Identity comes from the parser's RealKey; UpdateVS may flip
// Enabled and rewrite Weight, but never invents reals outside the original
// set.
type RealMember struct {
	Key     RealKey
	Enabled bool
	Weight  uint32
}

// RealUpdate is a single change inside an UpdateReals operation. Only the
// flags that are non-nil should be applied; this mirrors balancerpb
// RealConfig's optional-field semantics.
type RealUpdate struct {
	Key     RealKey
	Enabled *bool
	Weight  *uint32
}

// Operation is a single fuzzing step. Exactly one payload field is
// populated for any given Type: UpdateVS uses UpdateVS, DeleteVS and
// DeleteVSNoop use DeleteVS, UpdateReals uses UpdateReals.
type Operation struct {
	Type        OperationType
	OpNum       uint64
	UpdateVS    *UpdateVSPayload
	DeleteVS    *DeleteVSPayload
	UpdateReals *UpdateRealsPayload
}

// UpdateVSPayload describes a full desired-state replacement for one VS.
// Identity (Key) is immutable; Scheduler, Flags, AllowedSources, and the
// real subset are freshly generated per call.
type UpdateVSPayload struct {
	Key            VsKey
	Scheduler      balancerpb.VsScheduler
	Flags          VsFlags
	AllowedSources []CIDR
	Reals          []RealMember
}

// DeleteVSPayload lists VS keys to remove from the active set. For the
// lower-bound noop variant the slice is empty and ActiveCount/MinActive
// are populated for the log summary.
type DeleteVSPayload struct {
	Keys        []VsKey
	ActiveCount int
	MinActive   int
}

// UpdateRealsBatch contains per-VS real-state diffs for one entry in an
// UpdateReals operation.
type UpdateRealsBatch struct {
	Key     VsKey
	Updates []RealUpdate
}

// UpdateRealsPayload contains batched real-state diffs that are sent in
// one RPC. Batches may be empty when there are no active VS with active
// reals.
type UpdateRealsPayload struct {
	Batches []UpdateRealsBatch
}

// schedulerChoices lists the schedulers the generator rotates among. All
// four values are valid balancerpb.VsScheduler enums; the parser also
// understands all four.
var schedulerChoices = []balancerpb.VsScheduler{
	balancerpb.VsScheduler_WRR,
	balancerpb.VsScheduler_WLC,
	balancerpb.VsScheduler_SH,
	balancerpb.VsScheduler_OP,
}

// OperationGenerator emits a deterministic stream of Operation values for
// a given Model. It is not safe for concurrent use; the runner is
// single-threaded by design.
type OperationGenerator struct {
	model *Model
	n     uint64
	rng   *rand.Rand
}

const (
	minEnabledPercent = 40
	maxEnabledPercent = 60
	realWeightMin     = 1
	realWeightMax     = 30
	// Prefer small DeleteVS steps to avoid sticking near the lower bound.
	// Occasional deep drops keep the active-VS trajectory wide.
	deleteVSLargeDropChancePercent = 25
	deleteVSSmallStepMax           = 3
)

// NewOperationGenerator returns a generator seeded by the runtime config's
// effective seed. The cadence n must equal RuntimeConfig.UpdateVsEvery.
//
// The returned generator owns its RNG; two generators built with the same
// (model, n, seed) produce identical operation streams as long as Generate
// is called the same number of times on the same model state.
func NewOperationGenerator(model *Model, n uint64, seed int64) *OperationGenerator {
	return &OperationGenerator{
		model: model,
		n:     n,
		rng:   rand.New(rand.NewSource(seed)),
	}
}

// Generate returns the operation that should fire at the given 1-based
// operation number. The generator inspects model state but does not
// mutate it; the runner applies successful operations back into the model
// after the corresponding RPC and GetState succeed.
func (m *OperationGenerator) Generate(opNum uint64) Operation {
	if opNum == 1 {
		return Operation{Type: OpUpdate, OpNum: opNum}
	}
	switch {
	case m.n > 0 && opNum%(2*m.n) == 0:
		return m.generateDelete(opNum)
	case m.n > 0 && opNum%m.n == 0:
		return m.generateUpdateVS(opNum)
	default:
		return m.generateUpdateReals(opNum)
	}
}

// generateDelete removes a random subset of active VSes while preserving
// the 80-100% active-set invariant. Most deletes are intentionally small
// (1..3 VS) so UpdateVS operations can rebuild the active set and produce
// a wider long-run amplitude. Occasionally the generator performs a deep
// drop to exercise lower active-set regimes. Fixed VSes are excluded from
// the candidate pool so they remain present in every config.
//
// If no VS can be removed without violating the lower bound or the fixed
// set, it emits a no-op DeleteVS (empty key list) so the runner still
// issues the RPC and GetState pair.
func (m *OperationGenerator) generateDelete(opNum uint64) Operation {
	active := m.model.ActiveCount()
	min := m.model.MinActive()
	deletable := m.model.DeletableActiveOrder()

	maxDelete := active - min
	if maxDelete > len(deletable) {
		maxDelete = len(deletable)
	}
	if maxDelete <= 0 {
		return Operation{
			Type:  OpDeleteVSNoop,
			OpNum: opNum,
			DeleteVS: &DeleteVSPayload{
				Keys:        nil,
				ActiveCount: active,
				MinActive:   min,
			},
		}
	}

	deleteCount := 1
	switch {
	case maxDelete <= 1:
		deleteCount = 1
	case m.rng.Intn(100) < deleteVSLargeDropChancePercent:
		// Deep drop path: any legal delete size.
		deleteCount = 1 + m.rng.Intn(maxDelete)
	default:
		// Default path: keep delete bursts small so UpdateVS can recover.
		upper := deleteVSSmallStepMax
		if upper > maxDelete {
			upper = maxDelete
		}
		deleteCount = 1 + m.rng.Intn(upper)
	}
	targetActive := active - deleteCount
	keys := m.pickVSSubset(deletable, deleteCount)
	return Operation{
		Type:  OpDeleteVS,
		OpNum: opNum,
		DeleteVS: &DeleteVSPayload{
			Keys:        keys,
			ActiveCount: targetActive,
			MinActive:   min,
		},
	}
}

// generateUpdateVS chooses an UpdateVS target. If active count is below
// the original total, it picks a currently-inactive key (restoring the
// active set toward 100%). Otherwise it refreshes a random active VS with
// new scheduler/flags/ACL/real-subset.
func (m *OperationGenerator) generateUpdateVS(opNum uint64) Operation {
	var key VsKey
	inactive := m.model.InactiveKeys()
	if len(inactive) > 0 {
		key = inactive[m.rng.Intn(len(inactive))]
	} else {
		order := m.model.ActiveOrder()
		key = order[m.rng.Intn(len(order))]
	}
	sources := m.randomAllowedSources(key)
	if pinned := m.model.FixedSources(key); len(pinned) > 0 {
		// A fixed VS with pinned allowed sources keeps them constant for
		// the whole run instead of regenerating them on every update. A
		// fixed VS without pinned sources still gets random ones.
		sources = cloneCIDRs(pinned)
	}
	flags := m.randomFlags()
	if pinned := m.model.FixedFlags(key); pinned != nil {
		// A fixed VS with pinned flags keeps them constant for the whole
		// run instead of regenerating them on every update. A fixed VS
		// without pinned flags still gets random ones.
		flags = *pinned
	}
	return Operation{
		Type:  OpUpdateVS,
		OpNum: opNum,
		UpdateVS: &UpdateVSPayload{
			Key:            key,
			Scheduler:      m.randomScheduler(),
			Flags:          flags,
			AllowedSources: sources,
			Reals:          m.randomRealSubset(key, m.model.ActiveVS(key)),
		},
	}
}

// generateUpdateReals emits one operation that may update multiple VSes in
// one batch request. If there are at least two usable active VSes, at
// least two are included. If there is one usable VS, a one-batch update is
// emitted. If there are none, an empty batch set is emitted.
func (m *OperationGenerator) generateUpdateReals(opNum uint64) Operation {
	usable := make([]VsKey, 0, len(m.model.ActiveOrder()))
	for _, key := range m.model.ActiveOrder() {
		vs := m.model.ActiveVS(key)
		if vs == nil || len(vs.Reals()) == 0 {
			continue
		}
		usable = append(usable, key)
	}

	if len(usable) == 0 {
		return Operation{
			Type:        OpUpdateReals,
			OpNum:       opNum,
			UpdateReals: &UpdateRealsPayload{},
		}
	}

	batchCount := 0
	switch len(usable) {
	case 1:
		batchCount = 1
	default:
		batchCount = 2 + m.rng.Intn(len(usable)-1)
	}

	selectedVS := m.pickVSSubset(usable, batchCount)
	batches := make([]UpdateRealsBatch, 0, len(selectedVS))
	for _, key := range selectedVS {
		vs := m.model.ActiveVS(key)
		reals := vs.Reals()
		updates := m.randomRealBatchUpdates(vs, reals)
		batches = append(batches, UpdateRealsBatch{
			Key:     key,
			Updates: updates,
		})
	}

	return Operation{
		Type:  OpUpdateReals,
		OpNum: opNum,
		UpdateReals: &UpdateRealsPayload{
			Batches: batches,
		},
	}
}

// randomScheduler picks one of the four supported schedulers uniformly.
func (m *OperationGenerator) randomScheduler() balancerpb.VsScheduler {
	return schedulerChoices[m.rng.Intn(len(schedulerChoices))]
}

// randomFlags toggles each VS flag independently.
func (m *OperationGenerator) randomFlags() VsFlags {
	return VsFlags{
		Gre:    m.rng.Intn(2) == 0,
		FixMss: m.rng.Intn(2) == 0,
		PureL3: m.rng.Intn(2) == 0,
	}
}

// randomAllowedSources emits 1..5 deterministic networks whose address
// family matches the VS VIP identified by key. IPv4 VSes get /24 networks;
// IPv6 VSes get /48 networks. This keeps the encoding simple while
// satisfying the dataplane constraint that allowed-source families must
// match the VS family.
func (m *OperationGenerator) randomAllowedSources(key VsKey) []CIDR {
	count := 1 + m.rng.Intn(5)
	out := make([]CIDR, 0, count)
	ipv4 := net.IP(key.IP[:]).To4() != nil
	for idx := 0; idx < count; idx++ {
		var addr, mask []byte
		if ipv4 {
			addr = []byte{
				byte(m.rng.Intn(224)), // avoid multicast/reserved high ranges.
				byte(m.rng.Intn(256)),
				byte(m.rng.Intn(256)),
				0,
			}
			mask = []byte{0xff, 0xff, 0xff, 0x00}
		} else {
			// Generate a random /48 in the 2000::/3 (global unicast) range.
			addr = []byte{
				byte(0x20 | (m.rng.Intn(4) << 1)), // 0x20–0x26
				byte(m.rng.Intn(256)),
				byte(m.rng.Intn(256)),
				byte(m.rng.Intn(256)),
				byte(m.rng.Intn(256)),
				byte(m.rng.Intn(256)),
				0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
			}
			mask = []byte{
				0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
				0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
			}
		}
		out = append(out, CIDR{Addr: addr, Mask: mask})
	}
	return out
}

// randomRealSubset returns between ceil(80%) and 100% of the VS's
// original reals, in deterministic order. Each returned member carries an
// Enabled flag and a Weight in 1..30. Membership is always a subset of
// the parser's original real set for the VS. For active VSes, it also
// attempts to change the membership set on each UpdateVS call when the
// 80-100% envelope permits at least one alternative subset.
func (m *OperationGenerator) randomRealSubset(key VsKey, current *VSState) []RealMember {
	original := m.model.OriginalReals(key)
	total := len(original)
	if total == 0 {
		return nil
	}
	min := m.model.MinActiveReals(key)
	// Sub-range is [min, total]; pick a size deterministically.
	span := total - min + 1
	size := min + m.rng.Intn(span)
	if size < 1 {
		size = 1
	}
	picks := m.pickRealSubset(original, size)
	if current != nil && sameRealKeySet(current.Reals(), picks) {
		picks = m.forceDifferentRealSubset(original, picks, min)
	}
	out := make([]RealMember, 0, len(picks))
	enabledMin, enabledMax := enabledCountBounds(len(picks))
	enabledTarget := enabledMin + m.rng.Intn(enabledMax-enabledMin+1)
	enabledPicks := map[RealKey]struct{}{}
	for _, rk := range m.pickRealSubset(picks, enabledTarget) {
		enabledPicks[rk] = struct{}{}
	}
	for _, rk := range picks {
		_, enabled := enabledPicks[rk]
		out = append(out, RealMember{
			Key:     rk,
			Enabled: enabled,
			Weight:  uint32(realWeightMin + m.rng.Intn(realWeightMax-realWeightMin+1)),
		})
	}
	return out
}

// forceDifferentRealSubset rewrites picks so membership differs from the
// input set while staying within [min, total]. If no alternative exists,
// picks is returned unchanged.
func (m *OperationGenerator) forceDifferentRealSubset(
	original []RealKey,
	picks []RealKey,
	min int,
) []RealKey {
	total := len(original)
	if total == 0 {
		return nil
	}
	if len(picks) == total && min == total {
		return picks
	}

	pickSet := map[RealKey]struct{}{}
	for _, rk := range picks {
		pickSet[rk] = struct{}{}
	}
	excluded := make([]RealKey, 0, total-len(picks))
	for _, rk := range original {
		if _, ok := pickSet[rk]; ok {
			continue
		}
		excluded = append(excluded, rk)
	}

	switch {
	case len(picks) == min:
		if len(excluded) == 0 {
			return picks
		}
		add := excluded[m.rng.Intn(len(excluded))]
		pickSet[add] = struct{}{}
	case len(picks) == total:
		if len(picks)-1 < min {
			return picks
		}
		rm := picks[m.rng.Intn(len(picks))]
		delete(pickSet, rm)
	default:
		if len(excluded) > 0 && m.rng.Intn(2) == 0 {
			add := excluded[m.rng.Intn(len(excluded))]
			pickSet[add] = struct{}{}
		} else {
			rm := picks[m.rng.Intn(len(picks))]
			delete(pickSet, rm)
		}
	}

	out := make([]RealKey, 0, len(pickSet))
	for _, rk := range original {
		if _, ok := pickSet[rk]; ok {
			out = append(out, rk)
		}
	}
	return out
}

// randomRealBatchUpdates builds one UpdateReals batch for a single VS.
// Each batch independently chooses which reals to enable, disable, and
// reweight, while forcing the resulting enabled count into the 40-60%
// envelope (inclusive, integer-rounded bounds).
func (m *OperationGenerator) randomRealBatchUpdates(vs *VSState, reals []RealKey) []RealUpdate {
	enabled := make([]RealKey, 0, len(reals))
	disabled := make([]RealKey, 0, len(reals))
	for _, rk := range reals {
		if rs := vs.Real(rk); rs != nil && rs.Enabled {
			enabled = append(enabled, rk)
		} else {
			disabled = append(disabled, rk)
		}
	}

	targetMin, targetMax := enabledCountBounds(len(reals))
	targetEnabled := targetMin + m.rng.Intn(targetMax-targetMin+1)

	enableCount := 0
	disableCount := 0
	switch {
	case len(enabled) < targetEnabled:
		enableCount = targetEnabled - len(enabled)
	case len(enabled) > targetEnabled:
		disableCount = len(enabled) - targetEnabled
	}
	toEnable := m.pickRealSubset(disabled, enableCount)
	toDisable := m.pickRealSubset(enabled, disableCount)

	weightCount := 1 + m.rng.Intn(len(reals))
	toReweight := m.pickRealSubset(reals, weightCount)

	updatesByReal := map[RealKey]*RealUpdate{}
	for _, rk := range toEnable {
		v := true
		updatesByReal[rk] = &RealUpdate{
			Key:     rk,
			Enabled: &v,
		}
	}
	for _, rk := range toDisable {
		v := false
		upd, ok := updatesByReal[rk]
		if !ok {
			updatesByReal[rk] = &RealUpdate{
				Key:     rk,
				Enabled: &v,
			}
			continue
		}
		upd.Enabled = &v
	}
	for _, rk := range toReweight {
		w := uint32(realWeightMin + m.rng.Intn(realWeightMax-realWeightMin+1))
		upd, ok := updatesByReal[rk]
		if !ok {
			updatesByReal[rk] = &RealUpdate{
				Key:    rk,
				Weight: &w,
			}
			continue
		}
		upd.Weight = &w
	}

	updates := make([]RealUpdate, 0, len(updatesByReal))
	for _, rk := range reals {
		upd := updatesByReal[rk]
		if upd == nil {
			continue
		}
		updates = append(updates, *upd)
	}
	return updates
}

// enabledCountBounds returns the inclusive integer bounds for the enabled
// real count that correspond to 40-60% of total. For small totals where
// ceil(40%) > floor(60%), the interval collapses to ceil(40%).
func enabledCountBounds(total int) (int, int) {
	min := ceilPercent(total, minEnabledPercent)
	max := total * maxEnabledPercent / 100
	if max < min {
		max = min
	}
	if max > total {
		max = total
	}
	return min, max
}

func sameRealKeySet(a, b []RealKey) bool {
	if len(a) != len(b) {
		return false
	}
	set := map[RealKey]int{}
	for _, rk := range a {
		set[rk]++
	}
	for _, rk := range b {
		if set[rk] == 0 {
			return false
		}
		set[rk]--
	}
	for _, c := range set {
		if c != 0 {
			return false
		}
	}
	return true
}

// pickRealSubset returns a deterministic subset of size n from src,
// preserving src's original ordering. Selection uses a Fisher-Yates-style
// shuffle of indices so output order matches the input order of the
// chosen reals — that keeps log/UpdateVS output stable for a given seed.
func (m *OperationGenerator) pickRealSubset(src []RealKey, n int) []RealKey {
	if n >= len(src) {
		out := make([]RealKey, len(src))
		copy(out, src)
		return out
	}
	indices := make([]int, len(src))
	for idx := range indices {
		indices[idx] = idx
	}
	for idx := 0; idx < n; idx++ {
		j := idx + m.rng.Intn(len(indices)-idx)
		indices[idx], indices[j] = indices[j], indices[idx]
	}
	chosen := indices[:n]
	sort.Ints(chosen)
	out := make([]RealKey, 0, n)
	for _, ci := range chosen {
		out = append(out, src[ci])
	}
	return out
}

// pickVSSubset returns a deterministic subset of size n from src while
// preserving src order in the output.
func (m *OperationGenerator) pickVSSubset(src []VsKey, n int) []VsKey {
	if n >= len(src) {
		out := make([]VsKey, len(src))
		copy(out, src)
		return out
	}
	indices := make([]int, len(src))
	for idx := range indices {
		indices[idx] = idx
	}
	for idx := 0; idx < n; idx++ {
		j := idx + m.rng.Intn(len(indices)-idx)
		indices[idx], indices[j] = indices[j], indices[idx]
	}
	chosen := indices[:n]
	sort.Ints(chosen)
	out := make([]VsKey, 0, n)
	for _, ci := range chosen {
		out = append(out, src[ci])
	}
	return out
}

// Apply commits a successful operation to the model. The runner calls
// this only after the corresponding RPC and GetState validation succeed,
// so the model never drifts on failure. Apply is a no-op for
// OpDeleteVSNoop.
func (m *Model) Apply(op Operation) error {
	switch op.Type {
	case OpUpdate:
		return nil
	case OpUpdateVS:
		return m.applyUpdateVS(op.UpdateVS)
	case OpDeleteVS:
		return m.applyDeleteVS(op.DeleteVS)
	case OpDeleteVSNoop:
		return nil
	case OpUpdateReals:
		return m.applyUpdateReals(op.UpdateReals)
	default:
		return fmt.Errorf("apply operation: unknown type %d", op.Type)
	}
}

func (m *Model) applyUpdateVS(p *UpdateVSPayload) error {
	if p == nil {
		return fmt.Errorf("apply update_vs: nil payload")
	}
	if _, ok := m.originalVS[p.Key]; !ok {
		return fmt.Errorf("apply update_vs: unknown VS key %v", p.Key)
	}
	prev := m.ActiveVS(p.Key)
	state := &VSState{
		Scheduler:      p.Scheduler,
		Flags:          p.Flags,
		AllowedSources: cloneCIDRs(p.AllowedSources),
		realsOrder:     make([]RealKey, 0, len(p.Reals)),
		realsByKey:     make(map[RealKey]*RealState, len(p.Reals)),
	}
	for _, r := range p.Reals {
		if m.OriginalReal(p.Key, r.Key) == nil {
			return fmt.Errorf(
				"apply update_vs: real %v is not in the original set for VS %v",
				r.Key,
				p.Key,
			)
		}
		enabled := r.Enabled
		weight := r.Weight
		inherited := false
		if prev != nil {
			if prevReal := prev.Real(r.Key); prevReal != nil {
				enabled = prevReal.Enabled
				weight = prevReal.Weight
				inherited = true
			}
		}
		if !inherited && (weight < realWeightMin || weight > realWeightMax) {
			return fmt.Errorf(
				"apply update_vs: weight %d out of range %d..%d",
				weight,
				realWeightMin,
				realWeightMax,
			)
		}
		state.realsOrder = append(state.realsOrder, r.Key)
		state.realsByKey[r.Key] = &RealState{
			Enabled: enabled,
			Weight:  weight,
		}
	}
	m.SetActiveVS(p.Key, state)
	return nil
}

func (m *Model) applyDeleteVS(p *DeleteVSPayload) error {
	if p == nil {
		return fmt.Errorf("apply delete_vs: nil payload")
	}
	for _, k := range p.Keys {
		if m.IsFixed(k) {
			return fmt.Errorf("apply delete_vs: VS %v is fixed and cannot be removed", k)
		}
		if !m.RemoveActiveVS(k) {
			return fmt.Errorf("apply delete_vs: VS %v is not active", k)
		}
	}
	return nil
}

func (m *Model) applyUpdateReals(p *UpdateRealsPayload) error {
	if p == nil {
		return fmt.Errorf("apply update_reals: nil payload")
	}
	for _, batch := range p.Batches {
		vs := m.ActiveVS(batch.Key)
		if vs == nil {
			continue
		}
		for _, u := range batch.Updates {
			real := vs.realsByKey[u.Key]
			if real == nil {
				continue
			}
			if u.Enabled != nil {
				real.Enabled = *u.Enabled
			}
			if u.Weight != nil {
				if *u.Weight < realWeightMin || *u.Weight > realWeightMax {
					return fmt.Errorf(
						"apply update_reals: weight %d out of range %d..%d",
						*u.Weight,
						realWeightMin,
						realWeightMax,
					)
				}
				real.Weight = *u.Weight
			}
		}
	}
	return nil
}

// DeleteVSNoopSummary returns the operator-facing summary string the plan
// mandates for the lower-bound DeleteVS no-op. It is exported so the
// runner and tests can format the log line consistently.
func DeleteVSNoopSummary(active, min int) string {
	return fmt.Sprintf("delete_vs noop lower_bound active=%d min=%d", active, min)
}
