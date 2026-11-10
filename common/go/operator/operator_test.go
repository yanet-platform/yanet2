package operator_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/readiness"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
)

// constantSource always offers the same state and never wakes the loop.
type constantSource struct {
	wake chan struct{}
}

func (m *constantSource) Snapshot() (int, bool) {
	return 1, true
}

func (m *constantSource) Wake() <-chan struct{} {
	return m.wake
}

func (m *constantSource) Advance(int) {}

// probeResult is what an apply cut off by the shutdown read from the
// operator's own readiness service.
type probeResult struct {
	Reasons []string
	Err     error
}

// probingActuator holds every apply until its context ends, then asks the
// operator's own readiness service which reasons its scopes carry.
type probingActuator struct {
	conn     *grpc.ClientConn
	service  string
	applying chan struct{}
	results  chan probeResult
}

func (m *probingActuator) Apply(ctx context.Context, state int) error {
	m.applying <- struct{}{}
	<-ctx.Done()

	probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	response := &readinesspb.ReadyResponse{}
	err := m.conn.Invoke(probeCtx, "/"+m.service+"/Ready", &readinesspb.ReadyRequest{}, response)
	m.results <- probeResult{Reasons: reasonCodes(response), Err: err}

	return ctx.Err()
}

func (m *probingActuator) Close() error {
	return nil
}

// reasonCodes lists the reason codes of every scope in the response.
func reasonCodes(response *readinesspb.ReadyResponse) []string {
	codes := []string{}
	for _, scope := range response.GetScopes() {
		for _, reason := range scope.GetReasons() {
			codes = append(codes, reason.GetCode())
		}
	}
	return codes
}

// releasedWorker fails with its error once released and stops quietly when
// cancelled before that.
type releasedWorker struct {
	release chan struct{}
	err     error
}

func (m *releasedWorker) Run(ctx context.Context) error {
	select {
	case <-m.release:
		return m.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Test_Operator_Run_StopsInOrder verifies that an apply cut off by the
// shutdown still reads the drained readiness from the operator's own server,
// whether a cancelled context or a failing worker starts the shutdown.
func Test_Operator_Run_StopsInOrder(t *testing.T) {
	errWorker := errors.New("worker failed")
	cases := []struct {
		name    string
		failing bool
		wantErr error
	}{
		{
			name:    "cancelled context",
			wantErr: context.Canceled,
		},
		{
			name:    "failing worker",
			failing: true,
			wantErr: errWorker,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := freeEndpoint(t)
			conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
			require.NoError(t, err)
			t.Cleanup(func() { _ = conn.Close() })

			tracker := readiness.NewTracker(
				[]readiness.ScopeSpec{{Name: "apply"}},
				readiness.WithDrainLatch(),
			)
			actuator := &probingActuator{
				conn:     conn,
				service:  operator.ReadinessServiceName("test"),
				applying: make(chan struct{}, 1),
				results:  make(chan probeResult, 1),
			}
			worker := &releasedWorker{release: make(chan struct{}), err: errWorker}
			op := operator.NewOperator(
				actuator,
				&constantSource{wake: make(chan struct{})},
				operator.WithGRPCServer(operator.GRPCServerConfig{
					Endpoint: xcfg.MustNonEmptyString(endpoint),
				}),
				operator.WithReadiness("test", tracker),
				operator.WithWorkers(worker.Run),
			)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var group errgroup.Group
			group.Go(func() error {
				return op.Run(ctx)
			})
			<-actuator.applying
			if tc.failing {
				close(worker.release)
			} else {
				cancel()
			}

			result := <-actuator.results
			require.NoError(t, result.Err, "the server stopped before the work")
			require.Equal(t, []string{"SHUTTING_DOWN"}, result.Reasons, "the work stopped before the drain")
			require.ErrorIs(t, group.Wait(), tc.wantErr)
		})
	}
}
