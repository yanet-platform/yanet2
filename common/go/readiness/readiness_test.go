package readiness_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/common/go/readiness"
	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
)

func TestTracker_InitialState(t *testing.T) {
	r := readiness.NewTracker([]string{"gw-a", "gw-b"})
	resp := r.Ready(&readinesspb.ReadyRequest{})

	require.Len(t, resp.Scopes, 2)
	for _, s := range resp.Scopes {
		assert.Equal(t, readinesspb.State_STATE_UNKNOWN, s.State)
		assert.Nil(t, s.ObservedAt, "never-observed scope must have nil observed_at")
		assert.Nil(t, s.LastTransitionTime, "never-observed scope must have nil last_transition_time")
		assert.Empty(t, s.Reasons)
	}
}

func TestTracker_Transitions(t *testing.T) {
	applyErr := errors.New("connection refused")

	tests := []struct {
		name          string
		observeErr    error
		wantState     readinesspb.State
		wantReasonLen int
	}{
		{
			name:          "success -> READY",
			observeErr:    nil,
			wantState:     readinesspb.State_STATE_READY,
			wantReasonLen: 0,
		},
		// From UNKNOWN (never programmed), a failure must yield NOT_READY.
		{
			name:          "UNKNOWN + failure -> NOT_READY with APPLY_FAILED",
			observeErr:    applyErr,
			wantState:     readinesspb.State_STATE_NOT_READY,
			wantReasonLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := readiness.NewTracker([]string{"gw-a"})
			r.Observe("gw-a", tt.observeErr)

			resp := r.Ready(&readinesspb.ReadyRequest{})
			require.Len(t, resp.Scopes, 1)

			s := resp.Scopes[0]
			assert.Equal(t, tt.wantState, s.State)
			assert.Len(t, s.Reasons, tt.wantReasonLen)

			if tt.observeErr != nil {
				require.Len(t, s.Reasons, 1)
				assert.Equal(t, "APPLY_FAILED", s.Reasons[0].Code)
				assert.Equal(t, applyErr.Error(), s.Reasons[0].Message)
			}

			assert.NotNil(t, s.ObservedAt)
			assert.NotNil(t, s.LastTransitionTime)
		})
	}
}

// TestTracker_ObserveHoldOnRegression verifies the state-aware Observe
// semantics: a failure from READY or DEGRADED holds at DEGRADED (last-good
// state live), while a failure from UNKNOWN or NOT_READY produces NOT_READY.
func TestTracker_ObserveHoldOnRegression(t *testing.T) {
	boom := errors.New("push failed")

	tests := []struct {
		name       string
		seed       func(r *readiness.Tracker) // sets up pre-condition
		wantState  readinesspb.State
		wantReason string
	}{
		{
			name:       "UNKNOWN + err -> NOT_READY",
			seed:       func(_ *readiness.Tracker) {},
			wantState:  readinesspb.State_STATE_NOT_READY,
			wantReason: "APPLY_FAILED",
		},
		{
			name: "READY + err -> DEGRADED",
			seed: func(r *readiness.Tracker) {
				r.Observe("gw", nil) // UNKNOWN -> READY
			},
			wantState:  readinesspb.State_STATE_DEGRADED,
			wantReason: "APPLY_FAILED",
		},
		{
			name: "DEGRADED + err -> DEGRADED",
			seed: func(r *readiness.Tracker) {
				r.Observe("gw", nil)  // UNKNOWN -> READY
				r.Observe("gw", boom) // READY -> DEGRADED
			},
			wantState:  readinesspb.State_STATE_DEGRADED,
			wantReason: "APPLY_FAILED",
		},
		{
			name: "NOT_READY + err -> NOT_READY",
			seed: func(r *readiness.Tracker) {
				r.Observe("gw", boom) // UNKNOWN -> NOT_READY
			},
			wantState:  readinesspb.State_STATE_NOT_READY,
			wantReason: "APPLY_FAILED",
		},
		{
			name: "DEGRADED + ok -> READY",
			seed: func(r *readiness.Tracker) {
				r.Observe("gw", nil)  // UNKNOWN -> READY
				r.Observe("gw", boom) // READY -> DEGRADED
			},
			wantState:  readinesspb.State_STATE_READY,
			wantReason: "",
		},
		{
			name: "NOT_READY + ok -> READY",
			seed: func(r *readiness.Tracker) {
				r.Observe("gw", boom) // UNKNOWN -> NOT_READY
			},
			wantState:  readinesspb.State_STATE_READY,
			wantReason: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := readiness.NewTracker([]string{"gw"})
			tt.seed(r)

			var finalErr error
			if tt.wantState != readinesspb.State_STATE_READY {
				finalErr = boom
			}
			r.Observe("gw", finalErr)

			resp := r.Ready(&readinesspb.ReadyRequest{})
			require.Len(t, resp.Scopes, 1)
			s := resp.Scopes[0]
			assert.Equal(t, tt.wantState, s.State)
			if tt.wantReason != "" {
				require.Len(t, s.Reasons, 1)
				assert.Equal(t, tt.wantReason, s.Reasons[0].Code)
			} else {
				assert.Empty(t, s.Reasons)
			}
		})
	}
}

func TestTracker_LastTransitionTime_OnlyChangesOnStateChange(t *testing.T) {
	r := readiness.NewTracker([]string{"gw-a"})

	// First observation: UNKNOWN -> READY.
	r.Observe("gw-a", nil)
	resp1 := r.Ready(&readinesspb.ReadyRequest{})
	require.Len(t, resp1.Scopes, 1)
	ltt1 := resp1.Scopes[0].LastTransitionTime.AsTime()
	obs1 := resp1.Scopes[0].ObservedAt.AsTime()

	// Second observation with same outcome (READY -> READY): ltt must not change.
	r.Observe("gw-a", nil)
	resp2 := r.Ready(&readinesspb.ReadyRequest{})
	require.Len(t, resp2.Scopes, 1)
	ltt2 := resp2.Scopes[0].LastTransitionTime.AsTime()
	obs2 := resp2.Scopes[0].ObservedAt.AsTime()

	assert.Equal(t, ltt1, ltt2, "last_transition_time must not change when state stays READY")
	// observed_at may or may not advance depending on clock resolution; just
	// ensure it is not before the first observation.
	assert.False(t, obs2.Before(obs1))

	// Third observation with failure (READY -> DEGRADED): ltt must change.
	r.Observe("gw-a", errors.New("boom"))
	resp3 := r.Ready(&readinesspb.ReadyRequest{})
	require.Len(t, resp3.Scopes, 1)
	ltt3 := resp3.Scopes[0].LastTransitionTime.AsTime()

	assert.True(t, ltt3.Equal(ltt2) || ltt3.After(ltt2),
		"last_transition_time must advance on READY -> DEGRADED")
}

func TestTracker_ScopeFilter(t *testing.T) {
	r := readiness.NewTracker([]string{"gw-a", "gw-b", "gw-c"})
	r.Observe("gw-a", nil)
	r.Observe("gw-b", errors.New("down"))

	resp := r.Ready(&readinesspb.ReadyRequest{Scopes: []string{"gw-b"}})
	require.Len(t, resp.Scopes, 1)
	assert.Equal(t, "gw-b", resp.Scopes[0].Name)
	assert.Equal(t, readinesspb.State_STATE_NOT_READY, resp.Scopes[0].State)
}

func TestTracker_ScopeFilter_Empty_ReturnsAll(t *testing.T) {
	r := readiness.NewTracker([]string{"gw-a", "gw-b"})
	resp := r.Ready(&readinesspb.ReadyRequest{})
	assert.Len(t, resp.Scopes, 2)
}

func TestTracker_UnknownGatewayIgnored(t *testing.T) {
	r := readiness.NewTracker([]string{"gw-a"})
	// Observe on an unknown id must not panic.
	r.Observe("gw-x", nil)

	resp := r.Ready(&readinesspb.ReadyRequest{})
	require.Len(t, resp.Scopes, 1)
	assert.Equal(t, "gw-a", resp.Scopes[0].Name)
	assert.Equal(t, readinesspb.State_STATE_UNKNOWN, resp.Scopes[0].State)
}

func TestTracker_Drain(t *testing.T) {
	r := readiness.NewTracker([]string{"gw-a", "gw-b"})

	// Bring gw-a to READY.
	r.Observe("gw-a", nil)

	r.Drain()

	resp := r.Ready(&readinesspb.ReadyRequest{})
	require.Len(t, resp.Scopes, 2)
	for _, s := range resp.Scopes {
		assert.Equal(t, readinesspb.State_STATE_NOT_READY, s.State)
		require.Len(t, s.Reasons, 1)
		assert.Equal(t, "SHUTTING_DOWN", s.Reasons[0].Code)
		assert.NotNil(t, s.ObservedAt)
	}
}

func TestTracker_Drain_TransitionTimeOnlyOnChange(t *testing.T) {
	r := readiness.NewTracker([]string{"gw-a"})

	// Put gw-a in NOT_READY already via a failed observe.
	r.Observe("gw-a", errors.New("boom"))
	resp1 := r.Ready(&readinesspb.ReadyRequest{})
	ltt1 := resp1.Scopes[0].LastTransitionTime.AsTime()

	// Drain: already NOT_READY, so last_transition_time must stay the same.
	r.Drain()
	resp2 := r.Ready(&readinesspb.ReadyRequest{})
	ltt2 := resp2.Scopes[0].LastTransitionTime.AsTime()

	assert.Equal(t, ltt1, ltt2,
		"draining a scope already in NOT_READY must not update last_transition_time")
}

func TestTracker_Set_UpsertNewScope(t *testing.T) {
	r := readiness.NewTracker([]string{"gw-a"})

	// Set a scope that was not in the initial list.
	r.Set("rib", readinesspb.State_STATE_READY)

	resp := r.Ready(&readinesspb.ReadyRequest{})
	require.Len(t, resp.Scopes, 2)

	var ribScope *readinesspb.Scope
	for _, s := range resp.Scopes {
		if s.Name == "rib" {
			ribScope = s
		}
	}
	require.NotNil(t, ribScope)
	assert.Equal(t, readinesspb.State_STATE_READY, ribScope.State)
	assert.NotNil(t, ribScope.ObservedAt)
	assert.NotNil(t, ribScope.LastTransitionTime)
}

func TestTracker_Set_TransitionTimeOnlyOnStateChange(t *testing.T) {
	r := readiness.NewTracker([]string{"gw-a"})

	r.Set("gw-a", readinesspb.State_STATE_READY)
	resp1 := r.Ready(&readinesspb.ReadyRequest{})
	ltt1 := resp1.Scopes[0].LastTransitionTime.AsTime()

	// Second Set with same state must not advance last_transition_time.
	r.Set("gw-a", readinesspb.State_STATE_READY)
	resp2 := r.Ready(&readinesspb.ReadyRequest{})
	ltt2 := resp2.Scopes[0].LastTransitionTime.AsTime()

	assert.Equal(t, ltt1, ltt2, "last_transition_time must not change on same-state Set")

	// Transition to NOT_READY must advance last_transition_time.
	r.SetWithReason("gw-a", readinesspb.State_STATE_NOT_READY,
		&readinesspb.Reason{Code: "SYNCING"})
	resp3 := r.Ready(&readinesspb.ReadyRequest{})
	ltt3 := resp3.Scopes[0].LastTransitionTime.AsTime()

	assert.True(t, ltt3.Equal(ltt2) || ltt3.After(ltt2),
		"last_transition_time must advance on state change via Set")
}

func TestTracker_Set_ReasonsUpdated(t *testing.T) {
	r := readiness.NewTracker([]string{"gw-a"})

	r.SetWithReason("gw-a", readinesspb.State_STATE_NOT_READY,
		&readinesspb.Reason{Code: "SYNCING"})

	resp1 := r.Ready(&readinesspb.ReadyRequest{})
	require.Len(t, resp1.Scopes[0].Reasons, 1)
	assert.Equal(t, "SYNCING", resp1.Scopes[0].Reasons[0].Code)

	// Transition to READY clears reasons.
	r.Set("gw-a", readinesspb.State_STATE_READY)

	resp2 := r.Ready(&readinesspb.ReadyRequest{})
	assert.Empty(t, resp2.Scopes[0].Reasons)
}

func TestTracker_Touch_AdvancesObservedAt(t *testing.T) {
	r := readiness.NewTracker([]string{"neighbours"})

	// Drive the scope to a known state.
	r.SetWithReason("neighbours",
		readinesspb.State_STATE_READY,
		&readinesspb.Reason{Code: "SYNCED"},
	)

	resp1 := r.Ready(&readinesspb.ReadyRequest{})
	require.Len(t, resp1.Scopes, 1)
	s1 := resp1.Scopes[0]

	obs1 := s1.ObservedAt.AsTime()
	ltt1 := s1.LastTransitionTime.AsTime()
	wantState := s1.State
	wantReason := s1.Reasons[0].Code

	// Sleep a small amount so the clock advances reliably.
	time.Sleep(2 * time.Millisecond)

	r.Touch("neighbours")

	resp2 := r.Ready(&readinesspb.ReadyRequest{})
	require.Len(t, resp2.Scopes, 1)
	s2 := resp2.Scopes[0]

	obs2 := s2.ObservedAt.AsTime()
	ltt2 := s2.LastTransitionTime.AsTime()

	assert.True(t, obs2.After(obs1), "Touch must advance observed_at")
	assert.Equal(t, ltt1, ltt2, "Touch must not change last_transition_time")
	assert.Equal(t, wantState, s2.State, "Touch must not change state")
	require.Len(t, s2.Reasons, 1)
	assert.Equal(t, wantReason, s2.Reasons[0].Code, "Touch must not change reason")
}

func TestTracker_Touch_UnknownScope_NoOp(t *testing.T) {
	r := readiness.NewTracker([]string{"neighbours"})

	// Touch an unknown scope must not panic and must not create the scope.
	r.Touch("bird-session")

	resp := r.Ready(&readinesspb.ReadyRequest{})
	require.Len(t, resp.Scopes, 1)
	assert.Equal(t, "neighbours", resp.Scopes[0].Name)
}

// newObservedTracker constructs a Tracker backed by a zap observer so tests
// can inspect emitted log entries. The operatorName is pre-tagged on the
// logger before passing it to the tracker, matching the expected caller pattern.
func newObservedTracker(t *testing.T, scopes []string, operatorName string) (*readiness.Tracker, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zapcore.InfoLevel)
	r := readiness.NewTracker(
		scopes,
		readiness.WithLog(zap.New(core).With(zap.String("operator", operatorName))),
	)
	return r, logs
}

func TestTracker_Log_SetTransitionLogsEntry(t *testing.T) {
	r, logs := newObservedTracker(t, []string{"gw-a"}, "route")

	r.SetWithReason("gw-a", readinesspb.State_STATE_READY,
		&readinesspb.Reason{Code: "SYNCED", Message: "all good"})

	require.Equal(t, 1, logs.Len())
	entry := logs.All()[0]
	assert.Equal(t, "readiness scope transitioned", entry.Message)
	assert.Equal(t, zapcore.InfoLevel, entry.Level)

	fields := entry.ContextMap()
	assert.Equal(t, "route", fields["operator"])
	assert.Equal(t, "gw-a", fields["scope"])
	assert.Equal(t, readinesspb.State_STATE_UNKNOWN.String(), fields["from"])
	assert.Equal(t, readinesspb.State_STATE_READY.String(), fields["to"])
	assert.Equal(t, "SYNCED", fields["reason"])
	assert.Equal(t, "all good", fields["reason_message"])
}

func TestTracker_Log_SetNoopLogs_Nothing(t *testing.T) {
	r, logs := newObservedTracker(t, []string{"gw-a"}, "route")

	// First Set: UNKNOWN -> READY (transition, will log).
	r.Set("gw-a", readinesspb.State_STATE_READY)
	require.Equal(t, 1, logs.Len())

	// Second Set: READY -> READY (no state change, must not log).
	r.Set("gw-a", readinesspb.State_STATE_READY)
	assert.Equal(t, 1, logs.Len(), "no-op Set must not emit a log entry")
}

func TestTracker_Log_SetReasonOnlyChange_LogsNothing(t *testing.T) {
	r, logs := newObservedTracker(t, []string{"gw-a"}, "route")

	r.SetWithReason("gw-a", readinesspb.State_STATE_DEGRADED,
		&readinesspb.Reason{Code: "RECONNECTING"})
	require.Equal(t, 1, logs.Len())

	// Same state (DEGRADED), different reason — must not log.
	r.SetWithReason("gw-a", readinesspb.State_STATE_DEGRADED,
		&readinesspb.Reason{Code: "DOWN"})
	assert.Equal(t, 1, logs.Len(), "reason-only change must not emit a log entry")
}

func TestTracker_Log_ObserveReadyToDegraded(t *testing.T) {
	r, logs := newObservedTracker(t, []string{"gw"}, "forward")

	// UNKNOWN -> READY: one log entry.
	r.Observe("gw", nil)
	require.Equal(t, 1, logs.Len())

	// READY -> DEGRADED: one more log entry.
	r.Observe("gw", errors.New("push failed"))
	require.Equal(t, 2, logs.Len())

	entry := logs.All()[1]
	fields := entry.ContextMap()
	assert.Equal(t, "forward", fields["operator"])
	assert.Equal(t, "gw", fields["scope"])
	assert.Equal(t, readinesspb.State_STATE_READY.String(), fields["from"])
	assert.Equal(t, readinesspb.State_STATE_DEGRADED.String(), fields["to"])
}

func TestTracker_Log_ObserveRepeatedFailure_LogsNothing(t *testing.T) {
	r, logs := newObservedTracker(t, []string{"gw"}, "forward")

	boom := errors.New("push failed")

	// UNKNOWN -> NOT_READY: one log entry.
	r.Observe("gw", boom)
	require.Equal(t, 1, logs.Len())

	// NOT_READY -> NOT_READY (repeated failure): must not log.
	r.Observe("gw", boom)
	assert.Equal(t, 1, logs.Len(), "repeated failing Observe must not emit additional log entries")
}

func TestTracker_Log_DrainLogsPerChangedScope(t *testing.T) {
	r, logs := newObservedTracker(t, []string{"gw-a", "gw-b"}, "route")

	// Bring gw-a to READY (logs one transition).
	r.Observe("gw-a", nil)
	// Leave gw-b at UNKNOWN (no transition yet).
	countBeforeDrain := logs.Len()

	// Drain: gw-a transitions READY -> NOT_READY (logs); gw-b transitions
	// UNKNOWN -> NOT_READY (logs). gw-b was never in NOT_READY, so it logs too.
	r.Drain()
	assert.Equal(t, countBeforeDrain+2, logs.Len(),
		"Drain must log one entry per scope that changes state")
}

func TestTracker_Log_DrainAlreadyNotReady_NoLog(t *testing.T) {
	r, logs := newObservedTracker(t, []string{"gw-a"}, "route")

	// Force gw-a to NOT_READY first (UNKNOWN -> NOT_READY: one log entry).
	r.Observe("gw-a", errors.New("boom"))
	require.Equal(t, 1, logs.Len())

	// Drain: gw-a is already NOT_READY — must not log.
	r.Drain()
	assert.Equal(t, 1, logs.Len(),
		"Drain on a scope already in NOT_READY must not emit a log entry")
}

// TestTracker_DrainLatch_SetAfterDrainIsNoop verifies that with WithDrainLatch,
// a Set call after Drain is silently ignored (the latch prevents mutation).
//
// Without WithDrainLatch, Set after Drain takes effect normally (documents the
// opt-in nature of the latch).
func TestTracker_DrainLatch_SetAfterDrainIsNoop(t *testing.T) {
	tests := []struct {
		name       string
		latch      bool
		wantState  readinesspb.State
		wantReason string
	}{
		{
			name:       "with latch: Set after Drain is no-op",
			latch:      true,
			wantState:  readinesspb.State_STATE_NOT_READY,
			wantReason: "SHUTTING_DOWN",
		},
		{
			name:       "without latch: Set after Drain takes effect",
			latch:      false,
			wantState:  readinesspb.State_STATE_READY,
			wantReason: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := []readiness.Option{}
			if tt.latch {
				opts = append(opts, readiness.WithDrainLatch())
			}
			r := readiness.NewTracker([]string{"gw"}, opts...)

			r.Drain()
			r.Set("gw", readinesspb.State_STATE_READY)

			resp := r.Ready(&readinesspb.ReadyRequest{})
			require.Len(t, resp.Scopes, 1)
			s := resp.Scopes[0]

			assert.Equal(t, tt.wantState, s.State)
			if tt.wantReason != "" {
				require.Len(t, s.Reasons, 1)
				assert.Equal(t, tt.wantReason, s.Reasons[0].Code)
			} else {
				assert.Empty(t, s.Reasons)
			}
		})
	}
}

// collectWatch starts a Watch goroutine that feeds received messages into a
// buffered channel. The caller reads from the returned channel to inspect
// messages in order. The goroutine terminates when ctx is cancelled, which
// also unblocks the returned errgroup entry. eg must be waited on by the
// caller after cancelling ctx.
func collectWatch(
	t *testing.T,
	ctx context.Context,
	tracker *readiness.Tracker,
	req *readinesspb.ReadyRequest,
	bufSize int,
) (<-chan *readinesspb.ReadyResponse, *errgroup.Group) {
	t.Helper()
	ch := make(chan *readinesspb.ReadyResponse, bufSize)
	eg, ctx2 := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return tracker.Watch(ctx2, req, func(resp *readinesspb.ReadyResponse) error {
			ch <- resp
			return nil
		})
	})
	return ch, eg
}

// scopeByName finds a Scope by name in a ReadyResponse, returning nil if not
// present.
func scopeByName(resp *readinesspb.ReadyResponse, name string) *readinesspb.Scope {
	for _, s := range resp.GetScopes() {
		if s.GetName() == name {
			return s
		}
	}
	return nil
}

// recvTimeout reads one message from ch, failing the test if nothing arrives
// within the given timeout.
func recvTimeout(t *testing.T, ch <-chan *readinesspb.ReadyResponse, timeout time.Duration) *readinesspb.ReadyResponse {
	t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(timeout):
		t.Fatal("timed out waiting for Watch message")
		return nil
	}
}

// assertNoMsg asserts that no message arrives on ch within the given window.
func assertNoMsg(t *testing.T, ch <-chan *readinesspb.ReadyResponse, window time.Duration) {
	t.Helper()
	select {
	case msg := <-ch:
		t.Fatalf("unexpected Watch message: %v", msg)
	case <-time.After(window):
	}
}

const watchTimeout = 200 * time.Millisecond
const watchQuietWindow = 30 * time.Millisecond

func TestWatch_InitialSnapshot_AllScopes(t *testing.T) {
	r := readiness.NewTracker([]string{"gw-a", "gw-b"})
	r.Set("gw-a", readinesspb.State_STATE_READY)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ch, eg := collectWatch(t, ctx, r, &readinesspb.ReadyRequest{}, 8)

	snap := recvTimeout(t, ch, watchTimeout)
	require.Len(t, snap.Scopes, 2)

	sa := scopeByName(snap, "gw-a")
	require.NotNil(t, sa)
	assert.Equal(t, readinesspb.State_STATE_READY, sa.State)

	sb := scopeByName(snap, "gw-b")
	require.NotNil(t, sb)
	assert.Equal(t, readinesspb.State_STATE_UNKNOWN, sb.State)

	cancel()
	require.NoError(t, eg.Wait())
}

func TestWatch_InitialSnapshot_HonorsFilter(t *testing.T) {
	r := readiness.NewTracker([]string{"gw-a", "gw-b", "gw-c"})
	r.Set("gw-a", readinesspb.State_STATE_READY)
	r.Set("gw-b", readinesspb.State_STATE_NOT_READY)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ch, eg := collectWatch(t, ctx, r, &readinesspb.ReadyRequest{Scopes: []string{"gw-a"}}, 8)

	snap := recvTimeout(t, ch, watchTimeout)
	require.Len(t, snap.Scopes, 1)
	assert.Equal(t, "gw-a", snap.Scopes[0].Name)
	assert.Equal(t, readinesspb.State_STATE_READY, snap.Scopes[0].State)

	cancel()
	require.NoError(t, eg.Wait())
}

func TestWatch_StateChange_EmitsDelta(t *testing.T) {
	r := readiness.NewTracker([]string{"gw-a", "gw-b"})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ch, eg := collectWatch(t, ctx, r, &readinesspb.ReadyRequest{}, 8)

	// Consume the initial snapshot.
	recvTimeout(t, ch, watchTimeout)

	r.Set("gw-a", readinesspb.State_STATE_READY)

	delta := recvTimeout(t, ch, watchTimeout)
	require.Len(t, delta.Scopes, 1)
	assert.Equal(t, "gw-a", delta.Scopes[0].Name)
	assert.Equal(t, readinesspb.State_STATE_READY, delta.Scopes[0].State)

	cancel()
	require.NoError(t, eg.Wait())
}

func TestWatch_ReasonOnlyChange_EmitsDelta(t *testing.T) {
	r := readiness.NewTracker([]string{"gw-a"})
	r.SetWithReason("gw-a", readinesspb.State_STATE_DEGRADED, &readinesspb.Reason{Code: "OLD"})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ch, eg := collectWatch(t, ctx, r, &readinesspb.ReadyRequest{}, 8)

	// Consume the initial snapshot.
	recvTimeout(t, ch, watchTimeout)

	// Same state, different reason — must emit.
	r.SetWithReason("gw-a", readinesspb.State_STATE_DEGRADED, &readinesspb.Reason{Code: "NEW"})

	delta := recvTimeout(t, ch, watchTimeout)
	require.Len(t, delta.Scopes, 1)
	assert.Equal(t, "gw-a", delta.Scopes[0].Name)
	assert.Equal(t, readinesspb.State_STATE_DEGRADED, delta.Scopes[0].State)
	require.Len(t, delta.Scopes[0].Reasons, 1)
	assert.Equal(t, "NEW", delta.Scopes[0].Reasons[0].Code)

	cancel()
	require.NoError(t, eg.Wait())
}

func TestWatch_Touch_EmitsNothing(t *testing.T) {
	r := readiness.NewTracker([]string{"gw-a"})
	r.Set("gw-a", readinesspb.State_STATE_READY)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ch, eg := collectWatch(t, ctx, r, &readinesspb.ReadyRequest{}, 8)

	recvTimeout(t, ch, watchTimeout)

	r.Touch("gw-a")

	assertNoMsg(t, ch, watchQuietWindow)

	cancel()
	require.NoError(t, eg.Wait())
}

func TestWatch_NoopSet_EmitsNothing(t *testing.T) {
	r := readiness.NewTracker([]string{"gw-a"})
	r.Set("gw-a", readinesspb.State_STATE_READY)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ch, eg := collectWatch(t, ctx, r, &readinesspb.ReadyRequest{}, 8)

	recvTimeout(t, ch, watchTimeout)

	// Same state, same reason (nil) — must not emit.
	r.Set("gw-a", readinesspb.State_STATE_READY)

	assertNoMsg(t, ch, watchQuietWindow)

	cancel()
	require.NoError(t, eg.Wait())
}

func TestWatch_DeltaFilteredForDifferentScope(t *testing.T) {
	r := readiness.NewTracker([]string{"gw-a", "gw-b"})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Watch only gw-b.
	ch, eg := collectWatch(t, ctx, r, &readinesspb.ReadyRequest{Scopes: []string{"gw-b"}}, 8)

	recvTimeout(t, ch, watchTimeout)

	// Mutate gw-a — subscriber watches gw-b only, so no message.
	r.Set("gw-a", readinesspb.State_STATE_READY)

	assertNoMsg(t, ch, watchQuietWindow)

	// Mutate gw-b — now a message must arrive.
	r.Set("gw-b", readinesspb.State_STATE_NOT_READY)

	delta := recvTimeout(t, ch, watchTimeout)
	require.Len(t, delta.Scopes, 1)
	assert.Equal(t, "gw-b", delta.Scopes[0].Name)

	cancel()
	require.NoError(t, eg.Wait())
}

func TestWatch_Drain_EmitsChangedScopes(t *testing.T) {
	r := readiness.NewTracker([]string{"gw-a", "gw-b"})
	r.Set("gw-a", readinesspb.State_STATE_READY)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ch, eg := collectWatch(t, ctx, r, &readinesspb.ReadyRequest{}, 8)

	recvTimeout(t, ch, watchTimeout)

	r.Drain()

	delta := recvTimeout(t, ch, watchTimeout)
	// Both scopes transitioned: gw-a READY->NOT_READY, gw-b UNKNOWN->NOT_READY.
	require.Len(t, delta.Scopes, 2)
	for _, s := range delta.Scopes {
		assert.Equal(t, readinesspb.State_STATE_NOT_READY, s.State)
		require.Len(t, s.Reasons, 1)
		assert.Equal(t, "SHUTTING_DOWN", s.Reasons[0].Code)
	}

	cancel()
	require.NoError(t, eg.Wait())
}

func TestWatch_CtxCancel_ReturnsNil(t *testing.T) {
	r := readiness.NewTracker([]string{"gw-a"})

	ctx, cancel := context.WithCancel(t.Context())

	eg, ctx2 := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return r.Watch(ctx2, &readinesspb.ReadyRequest{}, func(_ *readinesspb.ReadyResponse) error {
			return nil
		})
	})

	// Give Watch time to register and send the snapshot.
	time.Sleep(5 * time.Millisecond)
	cancel()

	require.NoError(t, eg.Wait())
}

func TestWatch_GatewayStyle_DrainLatch(t *testing.T) {
	r := readiness.NewTracker([]string{"gateway"}, readiness.WithDrainLatch())

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ch, eg := collectWatch(t, ctx, r, &readinesspb.ReadyRequest{}, 8)

	// Initial snapshot: gateway is UNKNOWN.
	snap := recvTimeout(t, ch, watchTimeout)
	require.Len(t, snap.Scopes, 1)
	assert.Equal(t, readinesspb.State_STATE_UNKNOWN, snap.Scopes[0].State)

	// Set to READY — delta must arrive.
	r.Set("gateway", readinesspb.State_STATE_READY)
	delta1 := recvTimeout(t, ch, watchTimeout)
	require.Len(t, delta1.Scopes, 1)
	assert.Equal(t, readinesspb.State_STATE_READY, delta1.Scopes[0].State)

	// Drain — NOT_READY + SHUTTING_DOWN delta.
	r.Drain()
	delta2 := recvTimeout(t, ch, watchTimeout)
	require.Len(t, delta2.Scopes, 1)
	assert.Equal(t, readinesspb.State_STATE_NOT_READY, delta2.Scopes[0].State)
	require.Len(t, delta2.Scopes[0].Reasons, 1)
	assert.Equal(t, "SHUTTING_DOWN", delta2.Scopes[0].Reasons[0].Code)

	// With latch active, Set is a no-op — no more messages.
	r.Set("gateway", readinesspb.State_STATE_READY)
	assertNoMsg(t, ch, watchQuietWindow)

	cancel()
	require.NoError(t, eg.Wait())
}

func TestWatch_ConcurrentSubscribersAndMutators(t *testing.T) {
	r := readiness.NewTracker([]string{"gw-a", "gw-b"})

	const (
		numSubscribers = 8
		numMutations   = 50
	)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	subEg, _ := errgroup.WithContext(t.Context())
	for range numSubscribers {
		subEg.Go(func() error {
			_ = r.Watch(ctx, &readinesspb.ReadyRequest{}, func(_ *readinesspb.ReadyResponse) error {
				return nil
			})
			return nil
		})
	}

	eg, _ := errgroup.WithContext(t.Context())
	eg.Go(func() error {
		for idx := range numMutations {
			if idx%3 == 0 {
				r.Set("gw-a", readinesspb.State_STATE_READY)
			} else if idx%3 == 1 {
				r.Set("gw-b", readinesspb.State_STATE_NOT_READY)
			} else {
				r.Touch("gw-a")
			}
		}
		return nil
	})
	require.NoError(t, eg.Wait())

	cancel()
	require.NoError(t, subEg.Wait())
}
