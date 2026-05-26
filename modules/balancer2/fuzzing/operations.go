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
	"sort"

	"github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb"
)

// OperationType identifies the operation flavour. Callers switch on this
// to dispatch the appropriate RPC and model commit path.
type OperationType int

const (
	// OpUpdateVS replaces the desired state of a VS: scheduler, flags,
	// allowed sources, and the active subset of reals.
	OpUpdateVS OperationType = iota
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

// UpdateRealsPayload contains the real-state diffs to apply against a
// single active VS. Updates is non-empty.
type UpdateRealsPayload struct {
	Key     VsKey
	Updates []RealUpdate
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
	switch {
	case m.n > 0 && opNum%(2*m.n) == 0:
		return m.generateDelete(opNum)
	case m.n > 0 && opNum%m.n == 0:
		return m.generateUpdateVS(opNum)
	default:
		return m.generateUpdateReals(opNum)
	}
}

// generateDelete picks a deletable VS key. If the active count is already
// at the lower bound, it emits a no-op DeleteVS (empty key list) so the
// runner still issues the RPC and GetState pair.
func (m *OperationGenerator) generateDelete(opNum uint64) Operation {
	active := m.model.ActiveCount()
	min := m.model.MinActive()
	if active <= min {
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

	// Pick one key from the active set; deleting a single VS per
	// cadence keeps the bound easy to reason about and gives the
	// runner a clear target for the GetState pairing.
	idx := m.rng.Intn(active)
	key := m.model.ActiveOrder()[idx]
	return Operation{
		Type:  OpDeleteVS,
		OpNum: opNum,
		DeleteVS: &DeleteVSPayload{
			Keys:        []VsKey{key},
			ActiveCount: active - 1,
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
	return Operation{
		Type:  OpUpdateVS,
		OpNum: opNum,
		UpdateVS: &UpdateVSPayload{
			Key:            key,
			Scheduler:      m.randomScheduler(),
			Flags:          m.randomFlags(),
			AllowedSources: m.randomAllowedSources(),
			Reals:          m.randomRealSubset(key),
		},
	}
}

// generateUpdateReals picks an active VS at random and emits enabled and
// weight diffs against a random non-empty subset of its current reals.
// The generator skips emitting an UpdateReals when the active set is
// empty; that path is unreachable as long as MinActive >= 1, which the
// parser guarantees for any non-empty corpus.
func (m *OperationGenerator) generateUpdateReals(opNum uint64) Operation {
	order := m.model.ActiveOrder()
	if len(order) == 0 {
		// Defensive fallback: emit an empty-update operation against a
		// zero VsKey. The runner treats len(Updates)==0 as a no-op.
		return Operation{Type: OpUpdateReals, OpNum: opNum, UpdateReals: &UpdateRealsPayload{}}
	}
	key := order[m.rng.Intn(len(order))]
	vs := m.model.ActiveVS(key)
	reals := vs.Reals()
	if len(reals) == 0 {
		return Operation{Type: OpUpdateReals, OpNum: opNum, UpdateReals: &UpdateRealsPayload{Key: key}}
	}

	// Touch between 1 and len(reals) reals.
	count := 1 + m.rng.Intn(len(reals))
	picks := m.pickRealSubset(reals, count)

	updates := make([]RealUpdate, 0, len(picks))
	for _, rk := range picks {
		upd := RealUpdate{Key: rk}
		// Each pick mutates Enabled, Weight, or both. Always emit at
		// least one field so the update is observable.
		mode := m.rng.Intn(3)
		if mode == 0 || mode == 2 {
			enabled := m.rng.Intn(2) == 0
			upd.Enabled = &enabled
		}
		if mode == 1 || mode == 2 {
			w := uint32(1 + m.rng.Intn(10))
			upd.Weight = &w
		}
		updates = append(updates, upd)
	}
	return Operation{
		Type:  OpUpdateReals,
		OpNum: opNum,
		UpdateReals: &UpdateRealsPayload{
			Key:     key,
			Updates: updates,
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

// randomAllowedSources emits 1..5 deterministic IPv4 networks. The
// generator uses /24 networks rooted at the RNG-derived first three
// octets; this keeps the encoding simple and avoids any reliance on the
// parser to interpret CIDRs.
func (m *OperationGenerator) randomAllowedSources() []CIDR {
	count := 1 + m.rng.Intn(5)
	out := make([]CIDR, 0, count)
	for idx := 0; idx < count; idx++ {
		addr := make([]byte, 4)
		addr[0] = byte(m.rng.Intn(224)) // avoid multicast/reserved high ranges.
		addr[1] = byte(m.rng.Intn(256))
		addr[2] = byte(m.rng.Intn(256))
		addr[3] = 0
		mask := []byte{0xff, 0xff, 0xff, 0x00}
		out = append(out, CIDR{Addr: addr, Mask: mask})
	}
	return out
}

// randomRealSubset returns between ceil(80%) and 100% of the VS's
// original reals, in deterministic order. Each returned member carries an
// Enabled flag and a Weight in 1..10. Membership is always a subset of
// the parser's original real set for the VS.
func (m *OperationGenerator) randomRealSubset(key VsKey) []RealMember {
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
	out := make([]RealMember, 0, len(picks))
	for _, rk := range picks {
		out = append(out, RealMember{
			Key:     rk,
			Enabled: m.rng.Intn(2) == 0,
			Weight:  uint32(1 + m.rng.Intn(10)),
		})
	}
	return out
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

// Apply commits a successful operation to the model. The runner calls
// this only after the corresponding RPC and GetState validation succeed,
// so the model never drifts on failure. Apply is a no-op for
// OpDeleteVSNoop.
func (m *Model) Apply(op Operation) error {
	switch op.Type {
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
	state := &VSState{
		Scheduler:      p.Scheduler,
		Flags:          p.Flags,
		AllowedSources: cloneCIDRs(p.AllowedSources),
		realsOrder:     make([]RealKey, 0, len(p.Reals)),
		realsByKey:     make(map[RealKey]*RealState, len(p.Reals)),
	}
	for _, r := range p.Reals {
		if m.OriginalReal(p.Key, r.Key) == nil {
			return fmt.Errorf("apply update_vs: real %v is not in the original set for VS %v", r.Key, p.Key)
		}
		if r.Weight < 1 || r.Weight > 10 {
			return fmt.Errorf("apply update_vs: weight %d out of range 1..10", r.Weight)
		}
		state.realsOrder = append(state.realsOrder, r.Key)
		state.realsByKey[r.Key] = &RealState{
			Enabled: r.Enabled,
			Weight:  r.Weight,
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
	vs := m.ActiveVS(p.Key)
	if vs == nil {
		return fmt.Errorf("apply update_reals: VS %v is not active", p.Key)
	}
	for _, u := range p.Updates {
		real := vs.realsByKey[u.Key]
		if real == nil {
			return fmt.Errorf("apply update_reals: real %v is not in the active subset for VS %v", u.Key, p.Key)
		}
		if u.Enabled != nil {
			real.Enabled = *u.Enabled
		}
		if u.Weight != nil {
			if *u.Weight < 1 || *u.Weight > 10 {
				return fmt.Errorf("apply update_reals: weight %d out of range 1..10", *u.Weight)
			}
			real.Weight = *u.Weight
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
