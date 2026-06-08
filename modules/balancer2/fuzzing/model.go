// Package fuzzing — independent expected-state model.
//
// Model is the source of truth for what the controlplane should look like
// after every successfully applied operation. It is built once from a
// parsed Corpus and then mutated through Apply* methods that the operation
// runner (Task 7) invokes only after the corresponding RPC succeeds.
//
// The model is intentionally separate from balancerpb types: it stores only
// the fields the fuzzer reasons about (scheduler, flags, allowed sources,
// reals with enabled/weight) and exposes helpers that downstream comparison
// (Task 5) and runner (Task 7) code can use directly.
package fuzzing

import (
	"math"

	balancerpb "github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb/v1"
)

// CIDR is a single allowed-source network. The byte slices are independent
// copies so callers can store them without aliasing.
type CIDR struct {
	Addr []byte
	Mask []byte
}

// RealState tracks the mutable per-real fields the fuzzer cares about. The
// immutable identity lives in the parent VSState.realsOrder/realsByKey.
type RealState struct {
	Enabled bool
	Weight  uint32
}

// Clone returns a deep copy of the real state. There are no nested slices,
// so a value copy is sufficient.
func (m RealState) Clone() RealState {
	return m
}

// VsFlags mirrors the three booleans on balancerpb.VsFlags without
// dragging the protobuf message's embedded mutex into the model. The
// runner converts between the two at the RPC boundary.
type VsFlags struct {
	Gre    bool
	FixMss bool
	PureL3 bool
}

// VSState is the mutable per-VS state held in the active set. Identity
// fields (IP, port, proto) are immutable and live on the parent model's
// originalVSByKey snapshot; this struct only carries the fields that
// UpdateVS and UpdateReals may rewrite.
type VSState struct {
	Scheduler      balancerpb.VsScheduler
	Flags          VsFlags
	AllowedSources []CIDR
	// realsOrder is the deterministic iteration order used by snapshots
	// and downstream comparison; it is always a subset of the VS's
	// original real set.
	realsOrder []RealKey
	realsByKey map[RealKey]*RealState
}

// Reals returns the VS reals in deterministic order. The returned slice is
// safe for callers to iterate but the *RealState pointers alias model
// state; copy via RealState.Clone if you need to mutate.
func (m *VSState) Reals() []RealKey {
	return m.realsOrder
}

// Real returns the mutable state of a real by key, or nil if the real is
// not currently part of the active subset.
func (m *VSState) Real(key RealKey) *RealState {
	return m.realsByKey[key]
}

// HasReal reports whether the real is currently part of the active subset.
func (m *VSState) HasReal(key RealKey) bool {
	_, ok := m.realsByKey[key]
	return ok
}

// Clone returns a deep copy of the VS state, suitable for the
// copy/apply/commit dance the runner performs on every operation.
func (m *VSState) Clone() *VSState {
	out := &VSState{
		Scheduler:      m.Scheduler,
		Flags:          m.Flags,
		AllowedSources: cloneCIDRs(m.AllowedSources),
		realsOrder:     append([]RealKey(nil), m.realsOrder...),
		realsByKey:     make(map[RealKey]*RealState, len(m.realsByKey)),
	}
	for k, v := range m.realsByKey {
		clone := v.Clone()
		out.realsByKey[k] = &clone
	}
	return out
}

func cloneCIDRs(src []CIDR) []CIDR {
	if len(src) == 0 {
		return nil
	}
	out := make([]CIDR, len(src))
	for idx, c := range src {
		out[idx] = CIDR{
			Addr: append([]byte(nil), c.Addr...),
			Mask: append([]byte(nil), c.Mask...),
		}
	}
	return out
}

// Model is the expected-state container. It is not safe for concurrent
// use; the runner is single-threaded by design.
type Model struct {
	// originalOrder preserves corpus ordering of VS keys; downstream
	// consumers iterate it for stable logs and snapshots.
	originalOrder []VsKey
	// originalVS maps each immutable VS identity to its parser-built
	// VirtualServer (used to look up textual address, original reals,
	// etc.). The map itself is never mutated after NewModel.
	originalVS map[VsKey]*VirtualServer
	// originalRealsOrder is the deterministic real ordering for each VS,
	// preserved from the parser.
	originalRealsOrder map[VsKey][]RealKey
	// originalRealsByKey maps real identity to the parser real for O(1)
	// lookups during operation generation and bounds checks.
	originalRealsByKey map[VsKey]map[RealKey]*RealServer

	// activeOrder preserves the iteration order of currently-active VS
	// keys. Deletions remove entries; UpdateVS re-additions append.
	activeOrder []VsKey
	// activeVS holds mutable state for every currently-active VS.
	activeVS map[VsKey]*VSState

	// fixedSources maps each fixed VS key to its pinned allowed-source
	// CIDRs. Presence in the map marks the VS as fixed: the operation
	// generator never selects it for deletion and the model refuses to
	// remove one. A non-empty value additionally pins the allowed sources
	// so the generator keeps them constant instead of regenerating them on
	// every update. The map is populated once at construction and never
	// mutated afterwards.
	fixedSources map[VsKey][]CIDR
}

// ModelOption configures a Model at construction time.
type ModelOption func(*Model)

// WithFixedVS marks a set of VSes as fixed: they must remain present in
// every config the fuzzer produces. Each entry may pin allowed-source
// CIDRs, which the operation generator emits verbatim instead of random
// sources. Pinned sources are also seeded into the VS's initial active
// state so the expected model matches the bootstrap config the runner
// pushes, rather than only appearing once a later update happens to
// target the VS. Keys absent from the corpus are ignored here; the runner
// validates corpus membership before building the model.
func WithFixedVS(entries []FixedVSEntry) ModelOption {
	return func(m *Model) {
		for _, entry := range entries {
			sources := cloneCIDRs(entry.AllowedSources)
			m.fixedSources[entry.Key] = sources
			if state, ok := m.activeVS[entry.Key]; ok && len(sources) > 0 {
				state.AllowedSources = cloneCIDRs(sources)
			}
		}
	}
}

// NewModel builds an expected-state model from a parsed corpus. Every VS
// starts active with the parser's scheduler, all reals enabled at their
// parsed weight, and zero-valued flags/allowed sources. The parser already
// guarantees unique VS identities and at least one real per VS, so this
// constructor performs no extra validation. WithFixedVS pins a subset of
// VSes so they are never deleted during fuzzing.
func NewModel(corpus *Corpus, options ...ModelOption) *Model {
	m := &Model{
		originalOrder:      make([]VsKey, 0, len(corpus.VSs)),
		originalVS:         make(map[VsKey]*VirtualServer, len(corpus.VSs)),
		originalRealsOrder: make(map[VsKey][]RealKey, len(corpus.VSs)),
		originalRealsByKey: make(map[VsKey]map[RealKey]*RealServer, len(corpus.VSs)),
		activeOrder:        make([]VsKey, 0, len(corpus.VSs)),
		activeVS:           make(map[VsKey]*VSState, len(corpus.VSs)),
		fixedSources:       map[VsKey][]CIDR{},
	}
	for _, vs := range corpus.VSs {
		key := vs.Key
		m.originalOrder = append(m.originalOrder, key)
		m.originalVS[key] = vs

		order := make([]RealKey, 0, len(vs.Reals))
		byKey := make(map[RealKey]*RealServer, len(vs.Reals))
		realsState := make(map[RealKey]*RealState, len(vs.Reals))
		for _, real := range vs.Reals {
			order = append(order, real.Key)
			byKey[real.Key] = real
			// Preserve the parser/corpus weight verbatim. Task 6
			// startup sends this same value through UpdateConfig, so
			// the expected model must mirror it exactly until a
			// generated UpdateVS/UpdateReals overwrites the real.
			// Generated operations are independently constrained to
			// 1..30 (see operations.go), so the envelope is enforced
			// at the mutation boundary, not at load time.
			realsState[real.Key] = &RealState{
				Enabled: true,
				Weight:  real.Weight,
			}
		}
		m.originalRealsOrder[key] = order
		m.originalRealsByKey[key] = byKey

		m.activeOrder = append(m.activeOrder, key)
		m.activeVS[key] = &VSState{
			Scheduler:      vs.Scheduler,
			Flags:          VsFlags{},
			AllowedSources: nil,
			realsOrder:     append([]RealKey(nil), order...),
			realsByKey:     realsState,
		}
	}
	for _, o := range options {
		o(m)
	}
	return m
}

// OriginalOrder returns the corpus-ordered VS keys. Callers must treat the
// slice as read-only.
func (m *Model) OriginalOrder() []VsKey {
	return m.originalOrder
}

// OriginalCount returns the total number of VSes parsed from the corpus.
func (m *Model) OriginalCount() int {
	return len(m.originalOrder)
}

// OriginalReals returns the original real ordering for a VS. The result is
// nil if the VS is not part of the corpus.
func (m *Model) OriginalReals(key VsKey) []RealKey {
	return m.originalRealsOrder[key]
}

// OriginalRealCount returns the number of reals the corpus declared for
// the given VS.
func (m *Model) OriginalRealCount(key VsKey) int {
	return len(m.originalRealsOrder[key])
}

// OriginalReal returns the parser entry for a real, or nil if the real is
// not part of the VS's original set.
func (m *Model) OriginalReal(vs VsKey, real RealKey) *RealServer {
	reals, ok := m.originalRealsByKey[vs]
	if !ok {
		return nil
	}
	return reals[real]
}

// OriginalVS returns the parser-built VS, or nil if the key is unknown.
func (m *Model) OriginalVS(key VsKey) *VirtualServer {
	return m.originalVS[key]
}

// ActiveOrder returns the current ordered slice of active VS keys. Callers
// must treat the slice as read-only.
func (m *Model) ActiveOrder() []VsKey {
	return m.activeOrder
}

// ActiveCount returns the number of currently-active VSes.
func (m *Model) ActiveCount() int {
	return len(m.activeOrder)
}

// IsActive reports whether the VS is currently in the active set.
func (m *Model) IsActive(key VsKey) bool {
	_, ok := m.activeVS[key]
	return ok
}

// IsFixed reports whether the VS is pinned and must remain present in
// every config the fuzzer produces.
func (m *Model) IsFixed(key VsKey) bool {
	_, ok := m.fixedSources[key]
	return ok
}

// FixedSources returns the pinned allowed-source CIDRs for a fixed VS, or
// nil when the VS is not fixed or has no pinned sources. Callers must treat
// the slice as read-only.
func (m *Model) FixedSources(key VsKey) []CIDR {
	return m.fixedSources[key]
}

// DeletableActiveOrder returns the active VS keys that may be removed, in
// active order, excluding fixed VSes. When no VS is fixed the active order
// is returned directly so callers see no extra allocation.
func (m *Model) DeletableActiveOrder() []VsKey {
	if len(m.fixedSources) == 0 {
		return m.activeOrder
	}
	out := make([]VsKey, 0, len(m.activeOrder))
	for _, key := range m.activeOrder {
		if _, ok := m.fixedSources[key]; ok {
			continue
		}
		out = append(out, key)
	}
	return out
}

// ActiveVS returns the mutable state for an active VS, or nil if the VS is
// not currently active.
func (m *Model) ActiveVS(key VsKey) *VSState {
	return m.activeVS[key]
}

// MinActive returns the lower bound on the active VS count: ceil(80% of
// the original count). The model and operation generator must keep the
// active set at or above this value.
func (m *Model) MinActive() int {
	return ceilPercent(len(m.originalOrder), 80)
}

// MinActiveReals returns the lower bound on the active real count for a
// given VS: ceil(80% of its original real count).
func (m *Model) MinActiveReals(key VsKey) int {
	return ceilPercent(len(m.originalRealsOrder[key]), 80)
}

// InactiveKeys returns the original VS keys that are currently not part of
// the active set, in corpus order. It is used by the UpdateVS path to
// re-add deleted VSes when the active count is below target.
func (m *Model) InactiveKeys() []VsKey {
	out := make([]VsKey, 0, len(m.originalOrder)-len(m.activeOrder))
	for _, k := range m.originalOrder {
		if _, ok := m.activeVS[k]; !ok {
			out = append(out, k)
		}
	}
	return out
}

// SetActiveVS replaces (or installs) the active state for a VS. It is the
// commit point for an UpdateVS operation: if the VS was inactive, it is
// appended to activeOrder; otherwise the existing state is replaced.
func (m *Model) SetActiveVS(key VsKey, state *VSState) {
	if _, ok := m.activeVS[key]; !ok {
		m.activeOrder = append(m.activeOrder, key)
	}
	m.activeVS[key] = state
}

// RemoveActiveVS deletes a VS from the active set. Returns false if the VS
// was not active. The original entry remains available via OriginalVS so a
// later UpdateVS can re-add it.
func (m *Model) RemoveActiveVS(key VsKey) bool {
	if _, ok := m.activeVS[key]; !ok {
		return false
	}
	delete(m.activeVS, key)
	for idx, k := range m.activeOrder {
		if k == key {
			m.activeOrder = append(m.activeOrder[:idx], m.activeOrder[idx+1:]...)
			break
		}
	}
	return true
}

// Clone returns a deep copy of the model's mutable state. The immutable
// corpus snapshots (originalOrder, originalVS, originalRealsOrder,
// originalRealsByKey) are shared by reference because they are never
// mutated after NewModel; only the active set is duplicated so the runner
// can apply an operation to the candidate, validate against GetState,
// and either commit the candidate or discard it without disturbing the
// committed model.
func (m *Model) Clone() *Model {
	out := &Model{
		originalOrder:      m.originalOrder,
		originalVS:         m.originalVS,
		originalRealsOrder: m.originalRealsOrder,
		originalRealsByKey: m.originalRealsByKey,
		activeOrder:        append([]VsKey(nil), m.activeOrder...),
		activeVS:           make(map[VsKey]*VSState, len(m.activeVS)),
		fixedSources:       m.fixedSources,
	}
	for k, v := range m.activeVS {
		out.activeVS[k] = v.Clone()
	}
	return out
}

// ceilPercent returns ceil(total * percent / 100), bounded below by zero
// when total is zero. It is used for both the VS lower bound and the per-VS
// real subset lower bound.
func ceilPercent(total, percent int) int {
	if total <= 0 {
		return 0
	}
	return int(math.Ceil(float64(total) * float64(percent) / 100.0))
}
