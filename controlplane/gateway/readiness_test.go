package gateway_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/yanet-platform/yanet2/common/go/readiness"
	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
	"github.com/yanet-platform/yanet2/controlplane/gateway"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

const testGatewayReadinessScope = "gateway"

func newTestTracker() *readiness.Tracker {
	return readiness.NewTracker(
		[]readiness.ScopeSpec{{Name: testGatewayReadinessScope}},
		readiness.WithDrainLatch(),
		readiness.WithLog(zap.NewNop()),
	)
}

func TestReadinessTracker_InitialState(t *testing.T) {
	tracker := newTestTracker()

	resp := tracker.Ready(&readinesspb.ReadyRequest{})
	require.Len(t, resp.GetScopes(), 1)

	scope := resp.GetScopes()[0]
	require.Equal(t, testGatewayReadinessScope, scope.GetName())
	require.Equal(t, readinesspb.State_STATE_UNKNOWN, scope.GetState())
	require.Nil(t, scope.GetObservedAt())
	require.Nil(t, scope.GetLastTransitionTime())
}

func TestReadinessTracker_AfterReady(t *testing.T) {
	tracker := newTestTracker()
	tracker.Set(testGatewayReadinessScope, readinesspb.State_STATE_READY)

	resp := tracker.Ready(&readinesspb.ReadyRequest{})
	require.Len(t, resp.GetScopes(), 1)

	scope := resp.GetScopes()[0]
	require.Equal(t, readinesspb.State_STATE_READY, scope.GetState())
	require.NotNil(t, scope.GetObservedAt())
	require.NotNil(t, scope.GetLastTransitionTime())
	require.Empty(t, scope.GetReasons())
}

func TestReadinessTracker_AfterDrain(t *testing.T) {
	tracker := newTestTracker()
	tracker.Set(testGatewayReadinessScope, readinesspb.State_STATE_READY)
	tracker.Drain()

	resp := tracker.Ready(&readinesspb.ReadyRequest{})
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
			filter:      []string{testGatewayReadinessScope},
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
			tracker.Set(testGatewayReadinessScope, readinesspb.State_STATE_READY)

			resp := tracker.Ready(&readinesspb.ReadyRequest{Scopes: tc.filter})
			require.Len(t, resp.GetScopes(), tc.wantScopes)

			if tc.wantGateway {
				require.Equal(t, testGatewayReadinessScope, resp.GetScopes()[0].GetName())
			}
		})
	}
}

func TestReadinessTracker_ReadyAfterDrainIsNoop(t *testing.T) {
	tracker := newTestTracker()
	tracker.Set(testGatewayReadinessScope, readinesspb.State_STATE_READY)
	tracker.Drain()
	tracker.Set(testGatewayReadinessScope, readinesspb.State_STATE_READY)

	resp := tracker.Ready(&readinesspb.ReadyRequest{})
	require.Len(t, resp.GetScopes(), 1)

	scope := resp.GetScopes()[0]
	require.Equal(t, readinesspb.State_STATE_NOT_READY, scope.GetState())
	require.Len(t, scope.GetReasons(), 1)
	require.Equal(t, "SHUTTING_DOWN", scope.GetReasons()[0].GetCode())
}

func TestReadinessService_Ready(t *testing.T) {
	tracker := newTestTracker()
	tracker.Set(testGatewayReadinessScope, readinesspb.State_STATE_READY)

	svc := gateway.NewReadinessService(tracker)
	resp, err := svc.Ready(t.Context(), &readinesspb.ReadyRequest{})
	require.NoError(t, err)
	require.Len(t, resp.GetScopes(), 1)
	require.Equal(t, readinesspb.State_STATE_READY, resp.GetScopes()[0].GetState())
}

type readinessLatchService struct {
	endpoint string
}

func (m *readinessLatchService) Name() string {
	return "readiness-latch-service"
}

func (m *readinessLatchService) Endpoint() string {
	return m.endpoint
}

func (m *readinessLatchService) ServicesNames() []string {
	return []string{"test.ReadinessLatchService"}
}

func (m *readinessLatchService) RegisterService(_ *grpc.Server) {}

// verifies that shutdown drain blocks late readiness while the gateway's
// set-once scope declares no freshness contract.
func Test_GatewayRun_ReadinessDrainLatchesLateReady(t *testing.T) {
	t.Parallel()

	core, observed := observer.New(zap.InfoLevel)
	readyLogEntered := make(chan struct{})
	releaseReadyLog := make(chan struct{})
	var readyLogOnce sync.Once
	var releaseReadyLogOnce sync.Once
	releaseReadyLogFunc := func() {
		releaseReadyLogOnce.Do(func() { close(releaseReadyLog) })
	}
	t.Cleanup(releaseReadyLogFunc)
	log := zap.New(core, zap.Hooks(func(entry zapcore.Entry) error {
		if entry.Message == "all built-in modules ready" {
			readyLogOnce.Do(func() { close(readyLogEntered) })
			<-releaseReadyLog
		}
		return nil
	}))

	listener := NewTestListener(t)
	service := &readinessLatchService{
		endpoint: filepath.Join(t.TempDir(), "readiness-latch.sock"),
	}
	gw, err := gateway.NewGateway(
		gateway.DefaultConfig(),
		gateway.WithLog(log),
		gateway.WithListener(listener),
		gateway.WithService(service),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = gw.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	var group errgroup.Group
	group.Go(func() error { return gw.Run(ctx) })
	t.Cleanup(func() {
		cancel()
		releaseReadyLogFunc()
		_ = group.Wait()
	})

	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	client := ynpb.NewGatewayClient(conn)
	require.Eventually(t, func() bool {
		response, listErr := client.ListServices(t.Context(), &ynpb.ListServicesRequest{})
		if listErr != nil {
			return false
		}
		for _, entry := range response.GetServices() {
			if entry.GetBackend().GetName() == "test.ReadinessLatchService" {
				return true
			}
		}
		return false
	}, 5*time.Second, 50*time.Millisecond, "service runner did not register")

	select {
	case <-readyLogEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("gateway readiness publication did not reach the log hook")
	}

	watchCtx, watchCancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(watchCancel)
	stream := watchUntilOpen(t, watchCtx, ynpb.NewReadinessServiceClient(conn))

	cancel()
	response, err := stream.Recv()
	require.NoError(t, err)
	require.Len(t, response.GetScopes(), 1)
	scope := response.GetScopes()[0]
	require.Nil(t, scope.GetExpectedObservationInterval())
	require.Equal(t, readinesspb.State_STATE_NOT_READY, scope.GetState())
	require.Equal(t, "SHUTTING_DOWN", scope.GetReasons()[0].GetCode())

	releaseReadyLogFunc()
	watchCancel()
	require.NoError(t, group.Wait())

	transitions := observed.FilterMessage("readiness scope transitioned").All()
	require.Len(t, transitions, 1, "late READY must not produce a post-drain transition")
	fields := transitions[0].ContextMap()
	require.Equal(t, readinesspb.State_STATE_NOT_READY.String(), fields["to"])
	require.Equal(t, "SHUTTING_DOWN", fields["reason"])
}
