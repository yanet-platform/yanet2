package gateway

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
)

func newTestTracker() *readinessTracker {
	return newReadinessTracker(zap.NewNop())
}

func TestReadinessTracker_InitialState(t *testing.T) {
	tracker := newTestTracker()

	resp := tracker.Snapshot(&readinesspb.ReadyRequest{})
	require.Len(t, resp.GetScopes(), 1)

	scope := resp.GetScopes()[0]
	require.Equal(t, "gateway", scope.GetName())
	require.Equal(t, readinesspb.State_STATE_UNKNOWN, scope.GetState())
	require.Nil(t, scope.GetObservedAt())
	require.Nil(t, scope.GetLastTransitionTime())
}

func TestReadinessTracker_AfterReady(t *testing.T) {
	tracker := newTestTracker()
	tracker.Ready()

	resp := tracker.Snapshot(&readinesspb.ReadyRequest{})
	require.Len(t, resp.GetScopes(), 1)

	scope := resp.GetScopes()[0]
	require.Equal(t, readinesspb.State_STATE_READY, scope.GetState())
	require.NotNil(t, scope.GetObservedAt())
	require.NotNil(t, scope.GetLastTransitionTime())
	require.Empty(t, scope.GetReasons())
}

func TestReadinessTracker_AfterDrain(t *testing.T) {
	tracker := newTestTracker()
	tracker.Ready()
	tracker.Drain()

	resp := tracker.Snapshot(&readinesspb.ReadyRequest{})
	require.Len(t, resp.GetScopes(), 1)

	scope := resp.GetScopes()[0]
	require.Equal(t, readinesspb.State_STATE_NOT_READY, scope.GetState())
	require.Len(t, scope.GetReasons(), 1)
	require.Equal(t, "SHUTTING_DOWN", scope.GetReasons()[0].GetCode())
	require.NotNil(t, scope.GetObservedAt())
	require.NotNil(t, scope.GetLastTransitionTime())
}

func TestReadinessTracker_SnapshotFilter(t *testing.T) {
	tests := []struct {
		name        string
		filter      []string
		wantScopes  int
		wantGateway bool
	}{
		{
			name:        "empty filter returns gateway scope",
			filter:      nil,
			wantScopes:  1,
			wantGateway: true,
		},
		{
			name:        "explicit gateway filter returns gateway scope",
			filter:      []string{"gateway"},
			wantScopes:  1,
			wantGateway: true,
		},
		{
			name:       "unrelated filter returns empty scopes",
			filter:     []string{"other"},
			wantScopes: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tracker := newTestTracker()
			tracker.Ready()

			resp := tracker.Snapshot(&readinesspb.ReadyRequest{Scopes: tc.filter})
			require.Len(t, resp.GetScopes(), tc.wantScopes)

			if tc.wantGateway {
				require.Equal(t, "gateway", resp.GetScopes()[0].GetName())
			}
		})
	}
}

func TestReadinessTracker_ReadyAfterDrainIsNoop(t *testing.T) {
	tracker := newTestTracker()
	tracker.Ready()
	tracker.Drain()
	tracker.Ready()

	resp := tracker.Snapshot(&readinesspb.ReadyRequest{})
	require.Len(t, resp.GetScopes(), 1)

	scope := resp.GetScopes()[0]
	require.Equal(t, readinesspb.State_STATE_NOT_READY, scope.GetState())
	require.Len(t, scope.GetReasons(), 1)
	require.Equal(t, "SHUTTING_DOWN", scope.GetReasons()[0].GetCode())
}

func TestReadinessService_Ready(t *testing.T) {
	tracker := newTestTracker()
	tracker.Ready()

	svc := NewReadinessService(tracker)
	resp, err := svc.Ready(t.Context(), &readinesspb.ReadyRequest{})
	require.NoError(t, err)
	require.Len(t, resp.GetScopes(), 1)
	require.Equal(t, readinesspb.State_STATE_READY, resp.GetScopes()[0].GetState())
}
