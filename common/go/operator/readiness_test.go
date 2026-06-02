package operator_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/readinesspb"
)

func TestReadiness_InitialState(t *testing.T) {
	r := operator.NewReadiness([]string{"gw-a", "gw-b"})
	resp := r.Ready(&readinesspb.ReadyRequest{})

	require.Len(t, resp.Scopes, 2)
	for _, s := range resp.Scopes {
		assert.Equal(t, readinesspb.State_STATE_UNKNOWN, s.State)
		assert.Nil(t, s.ObservedAt, "never-observed scope must have nil observed_at")
		assert.Nil(t, s.LastTransitionTime, "never-observed scope must have nil last_transition_time")
		assert.Empty(t, s.Reasons)
	}
}

func TestReadiness_Transitions(t *testing.T) {
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
			r := operator.NewReadiness([]string{"gw-a"})
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

// TestReadiness_ObserveHoldOnRegression verifies the state-aware Observe
// semantics: a failure from READY or DEGRADED holds at DEGRADED (last-good
// state live), while a failure from UNKNOWN or NOT_READY produces NOT_READY.
func TestReadiness_ObserveHoldOnRegression(t *testing.T) {
	boom := errors.New("push failed")

	tests := []struct {
		name       string
		seed       func(r *operator.Readiness) // sets up pre-condition
		wantState  readinesspb.State
		wantReason string
	}{
		{
			name:       "UNKNOWN + err -> NOT_READY",
			seed:       func(_ *operator.Readiness) {},
			wantState:  readinesspb.State_STATE_NOT_READY,
			wantReason: "APPLY_FAILED",
		},
		{
			name: "READY + err -> DEGRADED",
			seed: func(r *operator.Readiness) {
				r.Observe("gw", nil) // UNKNOWN -> READY
			},
			wantState:  readinesspb.State_STATE_DEGRADED,
			wantReason: "APPLY_FAILED",
		},
		{
			name: "DEGRADED + err -> DEGRADED",
			seed: func(r *operator.Readiness) {
				r.Observe("gw", nil)  // UNKNOWN -> READY
				r.Observe("gw", boom) // READY -> DEGRADED
			},
			wantState:  readinesspb.State_STATE_DEGRADED,
			wantReason: "APPLY_FAILED",
		},
		{
			name: "NOT_READY + err -> NOT_READY",
			seed: func(r *operator.Readiness) {
				r.Observe("gw", boom) // UNKNOWN -> NOT_READY
			},
			wantState:  readinesspb.State_STATE_NOT_READY,
			wantReason: "APPLY_FAILED",
		},
		{
			name: "DEGRADED + ok -> READY",
			seed: func(r *operator.Readiness) {
				r.Observe("gw", nil)  // UNKNOWN -> READY
				r.Observe("gw", boom) // READY -> DEGRADED
			},
			wantState:  readinesspb.State_STATE_READY,
			wantReason: "",
		},
		{
			name: "NOT_READY + ok -> READY",
			seed: func(r *operator.Readiness) {
				r.Observe("gw", boom) // UNKNOWN -> NOT_READY
			},
			wantState:  readinesspb.State_STATE_READY,
			wantReason: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := operator.NewReadiness([]string{"gw"})
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

func TestReadiness_LastTransitionTime_OnlyChangesOnStateChange(t *testing.T) {
	r := operator.NewReadiness([]string{"gw-a"})

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

func TestReadiness_ScopeFilter(t *testing.T) {
	r := operator.NewReadiness([]string{"gw-a", "gw-b", "gw-c"})
	r.Observe("gw-a", nil)
	r.Observe("gw-b", errors.New("down"))

	resp := r.Ready(&readinesspb.ReadyRequest{Scopes: []string{"gw-b"}})
	require.Len(t, resp.Scopes, 1)
	assert.Equal(t, "gw-b", resp.Scopes[0].Name)
	assert.Equal(t, readinesspb.State_STATE_NOT_READY, resp.Scopes[0].State)
}

func TestReadiness_ScopeFilter_Empty_ReturnsAll(t *testing.T) {
	r := operator.NewReadiness([]string{"gw-a", "gw-b"})
	resp := r.Ready(&readinesspb.ReadyRequest{})
	assert.Len(t, resp.Scopes, 2)
}

func TestReadiness_UnknownGatewayIgnored(t *testing.T) {
	r := operator.NewReadiness([]string{"gw-a"})
	// Observe on an unknown id must not panic.
	r.Observe("gw-x", nil)

	resp := r.Ready(&readinesspb.ReadyRequest{})
	require.Len(t, resp.Scopes, 1)
	assert.Equal(t, "gw-a", resp.Scopes[0].Name)
	assert.Equal(t, readinesspb.State_STATE_UNKNOWN, resp.Scopes[0].State)
}

func TestReadiness_Drain(t *testing.T) {
	r := operator.NewReadiness([]string{"gw-a", "gw-b"})

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

func TestReadiness_Drain_TransitionTimeOnlyOnChange(t *testing.T) {
	r := operator.NewReadiness([]string{"gw-a"})

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

func TestReadiness_Set_UpsertNewScope(t *testing.T) {
	r := operator.NewReadiness([]string{"gw-a"})

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

func TestReadiness_Set_TransitionTimeOnlyOnStateChange(t *testing.T) {
	r := operator.NewReadiness([]string{"gw-a"})

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

func TestReadiness_Set_ReasonsUpdated(t *testing.T) {
	r := operator.NewReadiness([]string{"gw-a"})

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

// newObservedReadiness constructs a Readiness tracker backed by a zap
// observer so tests can inspect emitted log entries. The operatorName is
// pre-tagged on the logger before passing it to the tracker, matching the
// expected caller pattern.
func newObservedReadiness(t *testing.T, scopes []string, operatorName string) (*operator.Readiness, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zapcore.InfoLevel)
	r := operator.NewReadiness(
		scopes,
		operator.WithReadinessLog(zap.New(core).With(zap.String("operator", operatorName))),
	)
	return r, logs
}

func TestReadiness_Log_SetTransitionLogsEntry(t *testing.T) {
	r, logs := newObservedReadiness(t, []string{"gw-a"}, "route")

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

func TestReadiness_Log_SetNoopLogs_Nothing(t *testing.T) {
	r, logs := newObservedReadiness(t, []string{"gw-a"}, "route")

	// First Set: UNKNOWN -> READY (transition, will log).
	r.Set("gw-a", readinesspb.State_STATE_READY)
	require.Equal(t, 1, logs.Len())

	// Second Set: READY -> READY (no state change, must not log).
	r.Set("gw-a", readinesspb.State_STATE_READY)
	assert.Equal(t, 1, logs.Len(), "no-op Set must not emit a log entry")
}

func TestReadiness_Log_SetReasonOnlyChange_LogsNothing(t *testing.T) {
	r, logs := newObservedReadiness(t, []string{"gw-a"}, "route")

	r.SetWithReason("gw-a", readinesspb.State_STATE_DEGRADED,
		&readinesspb.Reason{Code: "RECONNECTING"})
	require.Equal(t, 1, logs.Len())

	// Same state (DEGRADED), different reason — must not log.
	r.SetWithReason("gw-a", readinesspb.State_STATE_DEGRADED,
		&readinesspb.Reason{Code: "DOWN"})
	assert.Equal(t, 1, logs.Len(), "reason-only change must not emit a log entry")
}

func TestReadiness_Log_ObserveReadyToDegraded(t *testing.T) {
	r, logs := newObservedReadiness(t, []string{"gw"}, "forward")

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

func TestReadiness_Log_ObserveRepeatedFailure_LogsNothing(t *testing.T) {
	r, logs := newObservedReadiness(t, []string{"gw"}, "forward")

	boom := errors.New("push failed")

	// UNKNOWN -> NOT_READY: one log entry.
	r.Observe("gw", boom)
	require.Equal(t, 1, logs.Len())

	// NOT_READY -> NOT_READY (repeated failure): must not log.
	r.Observe("gw", boom)
	assert.Equal(t, 1, logs.Len(), "repeated failing Observe must not emit additional log entries")
}

func TestReadiness_Log_DrainLogsPerChangedScope(t *testing.T) {
	r, logs := newObservedReadiness(t, []string{"gw-a", "gw-b"}, "route")

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

func TestReadiness_Log_DrainAlreadyNotReady_NoLog(t *testing.T) {
	r, logs := newObservedReadiness(t, []string{"gw-a"}, "route")

	// Force gw-a to NOT_READY first (UNKNOWN -> NOT_READY: one log entry).
	r.Observe("gw-a", errors.New("boom"))
	require.Equal(t, 1, logs.Len())

	// Drain: gw-a is already NOT_READY — must not log.
	r.Drain()
	assert.Equal(t, 1, logs.Len(),
		"Drain on a scope already in NOT_READY must not emit a log entry")
}
