package readiness

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
)

// readinessTransitionBuffer caps a subscriber's queue of undelivered state
// and reason transitions. Overflowing it drops the subscriber: silently
// discarding a promised transition would be worse than disconnecting.
const readinessTransitionBuffer = 1024

// ErrSlowConsumer is returned by Watch when the subscriber's transition
// queue overflows.
var ErrSlowConsumer = errors.New("readiness watch subscriber too slow")

// scopeState holds the mutable readiness state for one scope.
type scopeState struct {
	name                        string
	state                       readinesspb.State
	reason                      *readinesspb.Reason
	observedAt                  time.Time
	lastTransitionTime          time.Time
	expectedObservationInterval time.Duration
}

// Name returns the scope's name.
func (m *scopeState) Name() string {
	return m.name
}

// State returns the scope's current state.
func (m *scopeState) State() readinesspb.State {
	return m.state
}

// ToProto converts the scope to its wire representation.
func (m *scopeState) ToProto() *readinesspb.Scope {
	var reasons []*readinesspb.Reason
	if m.reason != nil {
		reasons = []*readinesspb.Reason{m.reason}
	}
	scope := &readinesspb.Scope{
		Name:    m.name,
		State:   m.state,
		Reasons: reasons,
	}
	if !m.observedAt.IsZero() {
		scope.ObservedAt = timestamppb.New(m.observedAt)
		scope.LastTransitionTime = timestamppb.New(m.lastTransitionTime)
	}
	if m.expectedObservationInterval > 0 {
		scope.ExpectedObservationInterval = durationpb.New(m.expectedObservationInterval)
	}
	return scope
}

// Touch advances observedAt to now without changing state, reason, or
// lastTransitionTime.
func (m *scopeState) Touch(now time.Time) {
	m.observedAt = now
}

// Apply writes next and reason onto the scope, advancing timestamps.
//
// observedAt always advances to now. lastTransitionTime advances only when
// the scope has never been observed before or when the state value changes.
// It reports the state the scope held prior to the call, and whether the
// call actually changed state or reason.
func (m *scopeState) Apply(
	next readinesspb.State,
	reason *readinesspb.Reason,
	now time.Time,
) (prevState readinesspb.State, changed bool) {
	prevState = m.state
	prevReason := m.reason
	if m.observedAt.IsZero() || m.state != next {
		m.lastTransitionTime = now
	}
	m.state = next
	m.reason = reason
	m.observedAt = now
	changed = prevState != next || !reasonEqual(prevReason, reason)
	return prevState, changed
}

// subscriber holds state for one active Watch call.
//
// Delivery has two paths with different durability rules. State and reason
// transitions go into a bounded FIFO queue: their order and completeness are
// the stream's contract, so a reader that falls more than
// readinessTransitionBuffer transitions behind is dropped with
// ErrSlowConsumer rather than silently losing a transition it was promised.
// Pure observation refreshes coalesce per scope name: only the newest
// snapshot per scope matters to a freshness consumer, so a burst costs
// memory proportional to the scope count, never to the event rate. signal
// (capacity one) wakes Watch; a token already queued guarantees the reader
// will drain both paths, including later updates.
type subscriber struct {
	// filter is the set of scope names this subscriber wants. Empty means all.
	filter map[string]struct{}
	// includeObservations reports whether the subscriber opted into pure
	// observation refreshes; without it only state or reason changes are
	// offered at all.
	includeObservations bool

	pendingMu      sync.Mutex
	transitions    []*readinesspb.Scope
	observations   map[string]*readinesspb.Scope
	signal         chan struct{}
	dropped        chan struct{}
	overflowedOnce bool
}

// WantsObservations reports whether the subscriber opted into pure
// observation refreshes.
func (m *subscriber) WantsObservations() bool {
	return m.includeObservations
}

// Matches reports whether the subscriber wants updates for the named scope.
//
// An empty filter matches every scope.
func (m *subscriber) Matches(name string) bool {
	if len(m.filter) == 0 {
		return true
	}
	_, ok := m.filter[name]
	return ok
}

// Offer files one updated scope and wakes Watch.
//
// A state or reason change is appended to the transition queue and retires
// any coalesced observation of the same scope: the observation is
// guaranteed older, because the tracker serializes updates under its mutex,
// and delivering it after the transition would leave the reader on a stale
// snapshot. A pure observation refresh replaces the newest coalesced
// snapshot of its scope. Waking is non-blocking: a token already queued
// guarantees the reader will drain the mailbox, including this update. It
// reports false when the transition queue is full — the caller must drop
// the subscriber then.
func (m *subscriber) Offer(scope *readinesspb.Scope, changed bool) bool {
	m.pendingMu.Lock()
	if changed {
		if len(m.transitions) >= readinessTransitionBuffer {
			if !m.overflowedOnce {
				m.overflowedOnce = true
				close(m.dropped)
			}
			m.pendingMu.Unlock()
			return false
		}
		m.transitions = append(m.transitions, scope)
		delete(m.observations, scope.Name)
	} else {
		m.observations[scope.Name] = scope
	}
	m.pendingMu.Unlock()

	select {
	case m.signal <- struct{}{}:
	default:
	}
	return true
}

// Drain returns the queued transitions in order, followed by the coalesced
// observation snapshots in name order, and empties both paths.
func (m *subscriber) Drain() []*readinesspb.Scope {
	m.pendingMu.Lock()
	scopes := make([]*readinesspb.Scope, 0, len(m.transitions)+len(m.observations))
	scopes = append(scopes, m.transitions...)
	transitionCount := len(m.transitions)
	m.transitions = m.transitions[:0]
	for _, scope := range m.observations {
		scopes = append(scopes, scope)
	}
	clear(m.observations)
	m.pendingMu.Unlock()

	observed := scopes[transitionCount:]
	sort.Slice(observed, func(idx, jdx int) bool {
		return observed[idx].Name < observed[jdx].Name
	})
	return scopes
}

// Dropped returns the channel that is closed when the transition queue
// overflowed and the subscriber must be dropped.
func (m *subscriber) Dropped() <-chan struct{} {
	return m.dropped
}

// Signal returns the channel that receives a token whenever the mailbox
// gains content.
func (m *subscriber) Signal() <-chan struct{} {
	return m.signal
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

// ScopeSpec declares one scope at tracker construction.
type ScopeSpec struct {
	Name string
	// ExpectedObservationInterval is the scope's freshness contract: the
	// nominal cadence at which the scope's source re-observes it, i.e. the
	// source's own ticker or heartbeat interval. Consumers judge staleness
	// per scope against this cadence instead of a single global threshold;
	// retries and slow applies can legitimately exceed it. Leave it zero
	// for scopes with no natural heartbeat — such scopes are never judged
	// stale. A zero or negative value is treated as none, and the contract
	// is fixed for the tracker's lifetime.
	ExpectedObservationInterval time.Duration
}

// NewTracker creates a Tracker pre-seeded with the supplied scopes, each
// starting at STATE_UNKNOWN.
func NewTracker(specs []ScopeSpec, options ...Option) *Tracker {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	scopes := map[string]*scopeState{}
	for _, spec := range specs {
		interval := max(spec.ExpectedObservationInterval, 0)
		scopes[spec.Name] = &scopeState{
			name:                        spec.Name,
			state:                       readinesspb.State_STATE_UNKNOWN,
			expectedObservationInterval: interval,
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

// snapshotLocked builds a ReadyResponse from the current state of all scopes
// matching filter. An empty filter returns all scopes. Caller must hold m.mu.
func (m *Tracker) snapshotLocked(filter map[string]struct{}) *readinesspb.ReadyResponse {
	var out []*readinesspb.Scope
	for _, s := range m.scopes {
		if len(filter) > 0 {
			if _, ok := filter[s.Name()]; !ok {
				continue
			}
		}
		out = append(out, s.ToProto())
	}
	return &readinesspb.ReadyResponse{Scopes: out}
}

// notifySubscribersLocked fans out updated scopes to all registered
// subscribers.
//
// Each subscriber's mailbox receives only the subset of updated scopes that
// matches its filter; a subscriber that did not opt into observation
// refreshes receives only the updates that changed state or reason. A
// transition-queue overflow drops the subscriber — its Watch returns
// ErrSlowConsumer. Caller must hold m.mu.
func (m *Tracker) notifySubscribersLocked(updated []*readinesspb.Scope, changed bool) {
	if len(m.subscribers) == 0 || len(updated) == 0 {
		return
	}

	for sub := range m.subscribers {
		if !changed && !sub.WantsObservations() {
			continue
		}
		for _, sc := range updated {
			if sub.Matches(sc.Name) {
				if !sub.Offer(sc, changed) {
					delete(m.subscribers, sub)
					break
				}
			}
		}
	}
}

// subscribe registers a new subscriber and atomically captures the initial
// snapshot. It returns the subscriber and the snapshot to send as the first
// streamed message.
func (m *Tracker) subscribe(
	filter map[string]struct{},
	includeObservations bool,
) (*subscriber, *readinesspb.ReadyResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sub := &subscriber{
		filter:              filter,
		includeObservations: includeObservations,
		observations:        map[string]*readinesspb.Scope{},
		transitions:         make([]*readinesspb.Scope, 0, 16),
		signal:              make(chan struct{}, 1),
		dropped:             make(chan struct{}),
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

// applyLocked applies next and reason to s, logs any state transition, and
// notifies Watch subscribers.
//
// A subscriber that opted into observation updates receives the scope's
// current snapshot on every apply — a repeated identical outcome still
// streams the advanced observation timestamp that freshness consumers
// depend on. A legacy subscriber receives it only when state or reason
// actually changed. Must be called with m.mu held.
func (m *Tracker) applyLocked(s *scopeState, next readinesspb.State, reason *readinesspb.Reason) {
	prevState, changed := s.Apply(next, reason, time.Now())
	m.logTransition(s.Name(), prevState, next, reason)
	m.notifySubscribersLocked([]*readinesspb.Scope{s.ToProto()}, changed)
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
// advances. last_transition_time changes only on a state transition.
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

	next, reason := observeOutcome(s.State(), err)
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
// was long ago. Only Watch subscribers that opted into observation updates
// receive the refreshed snapshot, since the advanced timestamp is what they
// consume for staleness. Touch is a no-op for unknown scopes — it never
// creates a scope. state, reason, and lastTransitionTime are left unchanged.
func (m *Tracker) Touch(scope string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.scopes[scope]
	if !ok {
		return
	}

	s.Touch(time.Now())
	m.notifySubscribersLocked([]*readinesspb.Scope{s.ToProto()}, false)
}

// Drain marks every scope as STATE_NOT_READY with a SHUTTING_DOWN reason.
//
// last_transition_time is updated only for scopes that change state, but
// every scope is re-observed. A subscriber that opted into observation
// updates receives every scope; a legacy subscriber receives only the ones
// whose state or reason actually changed. When the Tracker was constructed
// with WithDrainLatch, subsequent Set and Observe calls become no-ops after
// Drain returns.
func (m *Tracker) Drain() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	reason := &readinesspb.Reason{Code: "SHUTTING_DOWN"}

	// One fan-out pass over all scopes per change class: a legacy
	// subscriber must not see an unchanged scope, an opted-in one must see
	// even the unchanged ones re-observed. A changed scope travels through
	// the transition queue only, so its delivery is never duplicated.
	changedScopes := make([]*readinesspb.Scope, 0, len(m.scopes))
	unchangedScopes := make([]*readinesspb.Scope, 0, len(m.scopes))
	for _, s := range m.scopes {
		prevState, changed := s.Apply(readinesspb.State_STATE_NOT_READY, reason, now)
		m.logTransition(s.Name(), prevState, readinesspb.State_STATE_NOT_READY, reason)
		proto := s.ToProto()
		if changed {
			changedScopes = append(changedScopes, proto)
		} else {
			unchangedScopes = append(unchangedScopes, proto)
		}
	}

	m.notifySubscribersLocked(changedScopes, true)
	m.notifySubscribersLocked(unchangedScopes, false)

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

// Watch streams readiness updates to the caller via send.
//
// The first message carries the current state of every scope matching the
// request filter (same semantics as Ready). Without
// include_observation_updates in the request, each subsequent message
// carries only the scopes whose state or reason changed since the previous
// message, in order. With it, pure observed_at refreshes are delivered too,
// coalesced per scope: a burst of refreshes between two reads yields the
// newest snapshot of each affected scope. Transitions are never coalesced
// away — a subscriber that falls more than a bounded number of transitions
// behind is dropped with an error instead of losing one.
//
// Watch returns nil on ctx cancellation, the send error on stream write
// failure, and ErrSlowConsumer if the subscriber's transition queue
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

	sub, snapshot := m.subscribe(filter, req.GetIncludeObservationUpdates())
	defer m.unsubscribe(sub)

	if err := send(snapshot); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-sub.Dropped():
			return ErrSlowConsumer
		case <-sub.Signal():
			scopes := sub.Drain()
			if len(scopes) == 0 {
				continue
			}
			if err := send(&readinesspb.ReadyResponse{Scopes: scopes}); err != nil {
				return err
			}
		}
	}
}
