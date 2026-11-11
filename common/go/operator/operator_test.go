package operator_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/readiness"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
)

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
			desired := operator.NewLatest[int]()
			desired.Publish(1)
			op := operator.NewOperator(
				[]operator.Target[int]{{Name: "gw0", Actuator: actuator}},
				desired,
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

// failingActuator fails every apply with its error.
type failingActuator struct {
	err error
}

func (m *failingActuator) Apply(ctx context.Context, state int) error {
	return m.err
}

func (m *failingActuator) Close() error {
	return nil
}

// fixedCollector serves one metric of the operator's own.
type fixedCollector struct{}

func (fixedCollector) Collect() []*commonpb.Metric {
	return []*commonpb.Metric{commonpb.NewMetricCounter("test_operator_own_total", 1)}
}

// metricLabels flattens the labels of a metric for comparison.
func metricLabels(metric *commonpb.Metric) map[string]string {
	labels := map[string]string{}
	for _, label := range metric.GetLabels() {
		labels[label.GetName()] = label.GetValue()
	}
	return labels
}

// Test_Operator_WithMetrics_ServesFamilies verifies that the metrics service
// serves the reconcile families under the prefix and labels, every gateway's
// apply families with its name as the gateway label, counting a failed apply
// as an error, and the operator's own families.
func Test_Operator_WithMetrics_ServesFamilies(t *testing.T) {
	endpoint := freeEndpoint(t)
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	desired := operator.NewLatest[int]()
	desired.Publish(1)
	op := operator.NewOperator(
		[]operator.Target[int]{{Name: "gw0", Actuator: &failingActuator{err: errors.New("refused")}}},
		desired,
		operator.WithGRPCServer(operator.GRPCServerConfig{
			Endpoint: xcfg.MustNonEmptyString(endpoint),
		}),
		operator.WithMetrics(
			operator.MetricsConfig{
				Name:   "test",
				Prefix: "test_operator",
				Labels: []*commonpb.Label{commonpb.NewLabel("operator", "test")},
			},
			fixedCollector{},
		),
	)
	ctx, cancel := context.WithCancel(t.Context())
	var group errgroup.Group
	group.Go(func() error {
		return op.Run(ctx)
	})
	t.Cleanup(func() {
		cancel()
		_ = group.Wait()
	})

	own := map[string]string{"operator": "test"}
	gateway := map[string]string{"operator": "test", "gateway": "gw0"}
	require.EventuallyWithT(t, func(collector *assert.CollectT) {
		response := &commonpb.GetMetricsResponse{}
		err := conn.Invoke(
			t.Context(),
			"/"+operator.MetricsServiceName("test")+"/GetMetrics",
			&commonpb.GetMetricsRequest{},
			response,
		)
		require.NoError(collector, err)
		served := map[string]map[string]string{}
		failures := uint64(0)
		for _, metric := range response.GetMetrics() {
			served[metric.GetName()] = metricLabels(metric)
			if metric.GetName() == "test_operator_gateway_apply_errors_total" {
				failures = metric.GetCounter()
			}
		}
		require.Equal(collector, own, served["test_operator_reconcile_total"])
		require.Equal(collector, own, served["test_operator_reconcile_errors_total"])
		require.Equal(collector, own, served["test_operator_backoff_seconds"])
		require.Equal(collector, gateway, served["test_operator_gateway_apply_total"])
		require.Equal(collector, gateway, served["test_operator_gateway_apply_errors_total"])
		require.Equal(collector, gateway, served["test_operator_gateway_apply_duration_seconds"])
		require.Contains(collector, served, "test_operator_own_total")
		require.NotZero(collector, failures)
	}, 10*time.Second, 20*time.Millisecond)
}
