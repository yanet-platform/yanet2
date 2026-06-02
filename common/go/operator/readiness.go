package operator

import (
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/yanet-platform/yanet2/common/readinesspb"
)

// scopeState holds the mutable readiness state for one gateway scope.
type scopeState struct {
	name               string
	state              readinesspb.State
	reasons            []*readinesspb.Reason
	observedAt         time.Time
	lastTransitionTime time.Time
}

// Readiness tracks readiness state across named scopes.
//
// Each scope is an independent readiness dimension (e.g. "neighbours", "rib",
// or a gateway ID). State transitions and observation timestamps are
// maintained independently per scope.
type Readiness struct {
	mu     sync.Mutex
	scopes map[string]*scopeState
}

// NewReadiness creates a Readiness tracker pre-seeded with the supplied
// gateway IDs, each starting at STATE_UNKNOWN.
func NewReadiness(gatewayIDs []string) *Readiness {
	scopes := map[string]*scopeState{}
	for _, id := range gatewayIDs {
		scopes[id] = &scopeState{
			name:  id,
			state: readinesspb.State_STATE_UNKNOWN,
		}
	}

	return &Readiness{scopes: scopes}
}

// Observe records the outcome of one apply attempt for the named gateway.
//
// A failed apply holds the scope at DEGRADED when it was previously applied,
// and drops to NOT_READY only when it was never applied. observed_at always
// advances; last_transition_time changes only on a state transition.
func (m *Readiness) Observe(gatewayID string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.scopes[gatewayID]
	if !ok {
		return
	}

	now := time.Now()

	var (
		next    readinesspb.State
		reasons []*readinesspb.Reason
	)

	if err == nil {
		next = readinesspb.State_STATE_READY
	} else {
		reasons = []*readinesspb.Reason{{Code: "APPLY_FAILED", Message: err.Error()}}
		switch s.state {
		case readinesspb.State_STATE_READY, readinesspb.State_STATE_DEGRADED:
			// Last successfully applied state is still live — hold at DEGRADED.
			next = readinesspb.State_STATE_DEGRADED
		default:
			// Never successfully applied — no valid state to fall back to.
			next = readinesspb.State_STATE_NOT_READY
		}
	}

	if s.observedAt.IsZero() || s.state != next {
		s.lastTransitionTime = now
	}
	s.state = next
	s.reasons = reasons
	s.observedAt = now
}

// Set transitions the named scope to the given state, creating the scope if it
// does not yet exist.
//
// last_transition_time is updated only when the state value changes. Supplied
// reasons replace any existing reasons on each call.
func (m *Readiness) Set(scope string, state readinesspb.State, reasons ...*readinesspb.Reason) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()

	s, ok := m.scopes[scope]
	if !ok {
		s = &scopeState{name: scope}
		m.scopes[scope] = s
	}

	if s.observedAt.IsZero() || s.state != state {
		s.lastTransitionTime = now
	}
	s.state = state
	s.reasons = reasons
	s.observedAt = now
}

// Drain marks every scope as STATE_NOT_READY with a SHUTTING_DOWN reason.
//
// last_transition_time is updated only for scopes that change state.
func (m *Readiness) Drain() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for _, s := range m.scopes {
		if s.state != readinesspb.State_STATE_NOT_READY {
			s.lastTransitionTime = now
		}
		s.state = readinesspb.State_STATE_NOT_READY
		s.reasons = []*readinesspb.Reason{{Code: "SHUTTING_DOWN"}}
		s.observedAt = now
	}
}

// Ready builds a ReadyResponse for the given request, honoring the scope
// filter. An empty scopes list in the request returns all known scopes.
func (m *Readiness) Ready(req *readinesspb.ReadyRequest) *readinesspb.ReadyResponse {
	m.mu.Lock()
	defer m.mu.Unlock()

	filter := map[string]struct{}{}
	for _, name := range req.GetScopes() {
		filter[name] = struct{}{}
	}

	var out []*readinesspb.Scope
	for _, s := range m.scopes {
		if len(filter) > 0 {
			if _, ok := filter[s.name]; !ok {
				continue
			}
		}

		scope := &readinesspb.Scope{
			Name:    s.name,
			State:   s.state,
			Reasons: s.reasons,
		}
		if !s.observedAt.IsZero() {
			scope.ObservedAt = timestamppb.New(s.observedAt)
			scope.LastTransitionTime = timestamppb.New(s.lastTransitionTime)
		}
		out = append(out, scope)
	}

	return &readinesspb.ReadyResponse{Scopes: out}
}
