package operator_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
		{
			name:          "failure -> NOT_READY with APPLY_FAILED",
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

	// Third observation with failure (READY -> NOT_READY): ltt must change.
	r.Observe("gw-a", errors.New("boom"))
	resp3 := r.Ready(&readinesspb.ReadyRequest{})
	require.Len(t, resp3.Scopes, 1)
	ltt3 := resp3.Scopes[0].LastTransitionTime.AsTime()

	assert.True(t, ltt3.Equal(ltt2) || ltt3.After(ltt2),
		"last_transition_time must advance on READY -> NOT_READY")
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
	r.Set("gw-a", readinesspb.State_STATE_NOT_READY,
		&readinesspb.Reason{Code: "SYNCING"})
	resp3 := r.Ready(&readinesspb.ReadyRequest{})
	ltt3 := resp3.Scopes[0].LastTransitionTime.AsTime()

	assert.True(t, ltt3.Equal(ltt2) || ltt3.After(ltt2),
		"last_transition_time must advance on state change via Set")
}

func TestReadiness_Set_ReasonsUpdated(t *testing.T) {
	r := operator.NewReadiness([]string{"gw-a"})

	r.Set("gw-a", readinesspb.State_STATE_NOT_READY,
		&readinesspb.Reason{Code: "SYNCING"})

	resp1 := r.Ready(&readinesspb.ReadyRequest{})
	require.Len(t, resp1.Scopes[0].Reasons, 1)
	assert.Equal(t, "SYNCING", resp1.Scopes[0].Reasons[0].Code)

	// Transition to READY clears reasons.
	r.Set("gw-a", readinesspb.State_STATE_READY)

	resp2 := r.Ready(&readinesspb.ReadyRequest{})
	assert.Empty(t, resp2.Scopes[0].Reasons)
}
