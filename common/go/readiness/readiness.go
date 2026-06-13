package readiness

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
)

// readinessWatchBuffer is the per-subscriber channel capacity.
const readinessWatchBuffer = 16

// errSlowConsumer is returned by Watch when a subscriber's buffer overflows.
var errSlowConsumer = errors.New("readiness watch subscriber too slow")

// scopeState holds the mutable readiness state for one scope.
type scopeState struct {
	name               string
	state              readinesspb.State
	reason             *readinesspb.Reason
	observedAt         time.Time
	lastTransitionTime time.Time
}

// subscriber holds state for one active Watch call.
type subscriber struct {
	// filter is the set of scope names this subscriber wants; empty means all.
	filter  map[string]struct{}
	ch      chan *readinesspb.ReadyResponse
	dropped chan struct{}
}

// Tracker tracks readiness state across named scopes.
//
// Each scope is an independent readiness dimension (e.g. "neighbours", "rib",
// or a gateway ID). State transitions and observation timestamps are
// maintained independently per scope.
type Tracker struct {
	mu           sync.Mutex
	scopes       map[string]*scopeState
	subscribers  map[*subscriber]struct{}
	latchOnDrain bool
	drained      bool
	log          *zap.Logger
}

// Option configures NewTracker.
type Option func(*options)

type options struct {
	Log          *zap.Logger
	LatchOnDrain bool
}

func newOptions() *options {
	return &options{
		Log: zap.NewNop(),
	}
}

// WithLog sets the logger used by the Tracker.
//
// The caller is responsible for pre-tagging the logger with any
// operator or context attributes (e.g. via log.With(zap.String("operator",
// "route"))). The tracker itself adds only the scope field on each transition.
func WithLog(log *zap.Logger) Option {
	return func(o *options) {
		o.Log = log
	}
}

// WithDrainLatch enables the drain latch.
//
// When active, once Drain has been called any subsequent Set or Observe calls
// on the tracker are no-ops. This prevents a late Ready signal from flipping a
// drained tracker back to READY.
func WithDrainLatch() Option {
	return func(o *options) {
		o.LatchOnDrain = true
	}
}

// NewTracker creates a Tracker pre-seeded with the supplied scope names, each
// starting at STATE_UNKNOWN.
func NewTracker(scopeNames []string, options ...Option) *Tracker {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	scopes := map[string]*scopeState{}
	for _, id := range scopeNames {
		scopes[id] = &scopeState{
			name:  id,
			state: readinesspb.State_STATE_UNKNOWN,
		}
	}

	return &Tracker{
		scopes:       scopes,
		subscribers:  map[*subscriber]struct{}{},
		latchOnDrain: opts.LatchOnDrain,
		log:          opts.Log,
	}
}

// logTransition logs a state change for a scope at Info level when prev and
// next differ. The reason code and message are included when reason is non-nil.
func (m *Tracker) logTransition(name string, prev, next readinesspb.State, reason *readinesspb.Reason) {
	if prev == next {
		return
	}

	fields := []zap.Field{
		zap.String("scope", name),
		zap.String("from", prev.String()),
		zap.String("to", next.String()),
	}
	if reason != nil {
		fields = append(fields, zap.String("reason", reason.GetCode()))
		if reason.GetMessage() != "" {
			fields = append(fields, zap.String("reason_message", reason.GetMessage()))
		}
	}
	m.log.Info("readiness scope transitioned", fields...)
}

// reasonEqual reports whether two Reason values are equal by Code and Message.
func reasonEqual(a, b *readinesspb.Reason) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.GetCode() == b.GetCode() && a.GetMessage() == b.GetMessage()
}

// scopeToProto converts a scopeState to its proto representation.
func scopeToProto(s *scopeState) *readinesspb.Scope {
	var reasons []*readinesspb.Reason
	if s.reason != nil {
		reasons = []*readinesspb.Reason{s.reason}
	}
	scope := &readinesspb.Scope{
		Name:    s.name,
		State:   s.state,
		Reasons: reasons,
	}
	if !s.observedAt.IsZero() {
		scope.ObservedAt = timestamppb.New(s.observedAt)
		scope.LastTransitionTime = timestamppb.New(s.lastTransitionTime)
	}
	return scope
}

// snapshotLocked builds a ReadyResponse from the current state of all scopes
// matching filter. An empty filter returns all scopes. Caller must hold m.mu.
func (m *Tracker) snapshotLocked(filter map[string]struct{}) *readinesspb.ReadyResponse {
	var out []*readinesspb.Scope
	for _, s := range m.scopes {
		if len(filter) > 0 {
			if _, ok := filter[s.name]; !ok {
				continue
			}
		}
		out = append(out, scopeToProto(s))
	}
	return &readinesspb.ReadyResponse{Scopes: out}
}

// notifySubscribersLocked fans out changed scopes to all registered subscribers.
//
// Each subscriber receives only the subset of changed scopes that match its
// filter. The send is non-blocking: a subscriber whose channel is full is
// dropped (its dropped channel is closed and it is removed from the registry).
// Caller must hold m.mu.
func (m *Tracker) notifySubscribersLocked(changed []*readinesspb.Scope) {
	if len(m.subscribers) == 0 || len(changed) == 0 {
		return
	}

	for sub := range m.subscribers {
		var matching []*readinesspb.Scope
		for _, sc := range changed {
			if len(sub.filter) == 0 {
				matching = append(matching, sc)
				continue
			}
			if _, ok := sub.filter[sc.Name]; ok {
				matching = append(matching, sc)
			}
		}
		if len(matching) == 0 {
			continue
		}
		resp := &readinesspb.ReadyResponse{Scopes: matching}
		select {
		case sub.ch <- resp:
		default:
			close(sub.dropped)
			delete(m.subscribers, sub)
		}
	}
}

// subscribe registers a new subscriber and atomically captures the initial
// snapshot. It returns the subscriber and the snapshot to send as the first
// streamed message.
func (m *Tracker) subscribe(filter map[string]struct{}) (*subscriber, *readinesspb.ReadyResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sub := &subscriber{
		filter:  filter,
		ch:      make(chan *readinesspb.ReadyResponse, readinessWatchBuffer),
		dropped: make(chan struct{}),
	}
	m.subscribers[sub] = struct{}{}
	snapshot := m.snapshotLocked(filter)
	return sub, snapshot
}

// unsubscribe removes a subscriber from the registry. It is idempotent.
func (m *Tracker) unsubscribe(sub *subscriber) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.subscribers, sub)
}

// applyLocked writes next and reason onto s, advancing timestamps, logging
// the transition, and notifying Watch subscribers.
//
// observed_at always advances; last_transition_time changes only on a state
// change; subscribers are notified only when state or reason changed. Must
// be called with m.mu held.
func (m *Tracker) applyLocked(s *scopeState, next readinesspb.State, reason *readinesspb.Reason) {
	now := time.Now()
	prevState := s.state
	prevReason := s.reason
	if s.observedAt.IsZero() || s.state != next {
		s.lastTransitionTime = now
	}
	s.state = next
	s.reason = reason
	s.observedAt = now
	m.logTransition(s.name, prevState, next, reason)
	if prevState != next || !reasonEqual(prevReason, reason) {
		m.notifySubscribersLocked([]*readinesspb.Scope{scopeToProto(s)})
	}
}

// observeOutcome derives the next state and reason for an apply attempt
// whose result is err, given the scope's current state.
//
// A nil err yields READY. A failure holds a previously-applied scope at
// DEGRADED and drops a never-applied scope to NOT_READY.
func observeOutcome(current readinesspb.State, err error) (readinesspb.State, *readinesspb.Reason) {
	if err == nil {
		return readinesspb.State_STATE_READY, nil
	}
	reason := &readinesspb.Reason{Code: "APPLY_FAILED", Message: err.Error()}
	switch current {
	case readinesspb.State_STATE_READY, readinesspb.State_STATE_DEGRADED:
		return readinesspb.State_STATE_DEGRADED, reason
	default:
		return readinesspb.State_STATE_NOT_READY, reason
	}
}

// Observe records the outcome of one apply attempt for the named gateway.
//
// A failed apply holds the scope at DEGRADED when it was previously applied,
// and drops to NOT_READY only when it was never applied. observed_at always
// advances; last_transition_time changes only on a state transition.
func (m *Tracker) Observe(gatewayID string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.drained {
		return
	}

	s, ok := m.scopes[gatewayID]
	if !ok {
		return
	}

	next, reason := observeOutcome(s.state, err)
	m.applyLocked(s, next, reason)
}

// Set transitions the named scope to the given state with no reason.
//
// It creates the scope if absent. last_transition_time is updated only when
// the state value changes.
func (m *Tracker) Set(scope string, state readinesspb.State) {
	m.set(scope, state, nil)
}

// SetWithReason transitions the named scope to the given state with the
// supplied reason.
//
// It creates the scope if absent. last_transition_time is updated only when
// the state value changes.
func (m *Tracker) SetWithReason(scope string, state readinesspb.State, reason *readinesspb.Reason) {
	m.set(scope, state, reason)
}

// set is the shared implementation for Set and SetWithReason.
func (m *Tracker) set(scope string, state readinesspb.State, reason *readinesspb.Reason) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.drained {
		return
	}

	s, ok := m.scopes[scope]
	if !ok {
		s = &scopeState{name: scope}
		m.scopes[scope] = s
	}

	m.applyLocked(s, state, reason)
}

// Touch advances observed_at for an existing scope without changing its state.
//
// It records that the underlying source was successfully re-evaluated, so
// freshness consumers can distinguish a live scope from one whose last event
// was long ago. Touch is a no-op for unknown scopes — it never creates a
// scope. state, reason, and lastTransitionTime are left unchanged.
func (m *Tracker) Touch(scope string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.scopes[scope]
	if !ok {
		return
	}

	s.observedAt = time.Now()
}

// Drain marks every scope as STATE_NOT_READY with a SHUTTING_DOWN reason.
//
// last_transition_time is updated only for scopes that change state. When
// the Tracker was constructed with WithDrainLatch, subsequent Set and Observe
// calls become no-ops after Drain returns.
func (m *Tracker) Drain() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	reason := &readinesspb.Reason{Code: "SHUTTING_DOWN"}

	var changed []*readinesspb.Scope
	for _, s := range m.scopes {
		prevState := s.state
		prevReason := s.reason
		if s.state != readinesspb.State_STATE_NOT_READY {
			s.lastTransitionTime = now
		}
		s.state = readinesspb.State_STATE_NOT_READY
		s.reason = reason
		s.observedAt = now

		m.logTransition(s.name, prevState, readinesspb.State_STATE_NOT_READY, reason)

		if prevState != readinesspb.State_STATE_NOT_READY || !reasonEqual(prevReason, reason) {
			changed = append(changed, scopeToProto(s))
		}
	}

	m.notifySubscribersLocked(changed)

	if m.latchOnDrain {
		m.drained = true
	}
}

// Ready builds a ReadyResponse for the given request, honoring the scope
// filter. An empty scopes list in the request returns all known scopes.
func (m *Tracker) Ready(req *readinesspb.ReadyRequest) *readinesspb.ReadyResponse {
	m.mu.Lock()
	defer m.mu.Unlock()

	filter := map[string]struct{}{}
	for _, name := range req.GetScopes() {
		filter[name] = struct{}{}
	}

	return m.snapshotLocked(filter)
}

// Watch streams readiness state changes to the caller via send.
//
// The first message carries the current state of every scope matching the
// request filter (same semantics as Ready). Each subsequent message carries
// only the scopes whose state or reason changed since the previous message.
// A pure observed_at refresh (Touch) never emits. A no-op mutation (same
// state and same reason) never emits.
//
// Watch returns nil on ctx cancellation, returns the send error on stream
// write failure, and returns errSlowConsumer if the subscriber's buffer
// overflows.
func (m *Tracker) Watch(
	ctx context.Context,
	req *readinesspb.ReadyRequest,
	send func(*readinesspb.ReadyResponse) error,
) error {
	filter := map[string]struct{}{}
	for _, name := range req.GetScopes() {
		filter[name] = struct{}{}
	}

	sub, snapshot := m.subscribe(filter)
	defer m.unsubscribe(sub)

	if err := send(snapshot); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-sub.dropped:
			return errSlowConsumer
		case resp, ok := <-sub.ch:
			if !ok {
				return errSlowConsumer
			}
			if err := send(resp); err != nil {
				return err
			}
		}
	}
}
