package gateway

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// readinessTracker holds the mutable readiness state for the gateway scope.
type readinessTracker struct {
	mu                 sync.Mutex
	state              readinesspb.State
	reason             *readinesspb.Reason
	observedAt         time.Time
	lastTransitionTime time.Time
	drained            bool
	log                *zap.Logger
}

// newReadinessTracker creates a readinessTracker seeded at STATE_UNKNOWN.
func newReadinessTracker(log *zap.Logger) *readinessTracker {
	return &readinessTracker{
		state: readinesspb.State_STATE_UNKNOWN,
		log:   log,
	}
}

// Ready transitions the tracker to STATE_READY.
//
// It is a no-op once Drain has been called; a late Ready must not flip a
// drained tracker back to READY.
func (m *readinessTracker) Ready() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.drained {
		return
	}

	now := time.Now()
	prev := m.state
	next := readinesspb.State_STATE_READY

	if m.observedAt.IsZero() || m.state != next {
		m.lastTransitionTime = now
	}
	m.state = next
	m.reason = nil
	m.observedAt = now

	m.logTransition(prev, next, nil)
}

// Drain transitions the tracker to STATE_NOT_READY with a SHUTTING_DOWN reason
// and latches it so that subsequent Ready calls are no-ops.
func (m *readinessTracker) Drain() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	prev := m.state
	next := readinesspb.State_STATE_NOT_READY
	reason := &readinesspb.Reason{Code: "SHUTTING_DOWN"}

	if m.observedAt.IsZero() || m.state != next {
		m.lastTransitionTime = now
	}
	m.state = next
	m.reason = reason
	m.observedAt = now
	m.drained = true

	m.logTransition(prev, next, reason)
}

// Snapshot builds a ReadyResponse for the given request, honoring the scope
// filter.
//
// An empty scopes list returns the gateway scope. A non-empty filter that
// does not include "gateway" returns an empty scopes list.
func (m *readinessTracker) Snapshot(req *readinesspb.ReadyRequest) *readinesspb.ReadyResponse {
	m.mu.Lock()
	defer m.mu.Unlock()

	filter := req.GetScopes()
	if len(filter) > 0 {
		found := false
		for _, name := range filter {
			if name == "gateway" {
				found = true
				break
			}
		}
		if !found {
			return &readinesspb.ReadyResponse{}
		}
	}

	var reasons []*readinesspb.Reason
	if m.reason != nil {
		reasons = []*readinesspb.Reason{m.reason}
	}

	scope := &readinesspb.Scope{
		Name:    "gateway",
		State:   m.state,
		Reasons: reasons,
	}
	if !m.observedAt.IsZero() {
		scope.ObservedAt = timestamppb.New(m.observedAt)
		scope.LastTransitionTime = timestamppb.New(m.lastTransitionTime)
	}

	return &readinesspb.ReadyResponse{Scopes: []*readinesspb.Scope{scope}}
}

// logTransition logs a state change at Info level when prev and next differ.
// Must be called with m.mu held.
func (m *readinessTracker) logTransition(prev, next readinesspb.State, reason *readinesspb.Reason) {
	if prev == next {
		return
	}

	fields := []zap.Field{
		zap.String("from", prev.String()),
		zap.String("to", next.String()),
	}
	if reason != nil {
		fields = append(fields, zap.String("reason", reason.GetCode()))
		if reason.GetMessage() != "" {
			fields = append(fields, zap.String("reason_message", reason.GetMessage()))
		}
	}
	m.log.Info("readiness state transitioned", fields...)
}

// ReadinessService implements ynpb.ReadinessServiceServer by delegating to a
// readinessTracker.
type ReadinessService struct {
	ynpb.UnimplementedReadinessServiceServer

	tracker *readinessTracker
}

// NewReadinessService constructs a ReadinessService backed by tracker.
func NewReadinessService(tracker *readinessTracker) *ReadinessService {
	return &ReadinessService{tracker: tracker}
}

// Ready returns the current readiness state of the gateway scope.
func (m *ReadinessService) Ready(
	ctx context.Context,
	req *readinesspb.ReadyRequest,
) (*readinesspb.ReadyResponse, error) {
	return m.tracker.Snapshot(req), nil
}
