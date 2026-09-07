package gateway_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/gateway"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// bothScopes is a gateway-hosted service reporting one port metric and one
// metric of the dataplane instance behind the gateway, the shape the
// counters service has.
type bothScopes struct{}

func (bothScopes) Name() string                   { return "both-scopes" }
func (bothScopes) Endpoint() string               { return "" }
func (bothScopes) ServicesNames() []string        { return nil }
func (bothScopes) RegisterService(_ *grpc.Server) {}

func (bothScopes) Collect() []*commonpb.Metric {
	return []*commonpb.Metric{commonpb.NewMetricCounter("worker_rx_packets", 1)}
}

func (bothScopes) CollectPortMetrics() []*commonpb.Metric {
	return []*commonpb.Metric{
		commonpb.NewMetricCounter(
			"port_counter_value",
			2,
			commonpb.NewLabel("port_name", "port0"),
		),
	}
}

// Test_NewGateway_PortMetricsServiceReachable verifies that a gateway
// answers port metrics over the wire and lists the service in its
// registry.
func Test_NewGateway_PortMetricsServiceReachable(t *testing.T) {
	t.Parallel()

	listener := NewTestListener(t)
	gw, err := gateway.NewGateway(
		gateway.DefaultConfig(),
		gateway.WithListener(listener),
		gateway.WithBuiltinService(bothScopes{}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = gw.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	var group errgroup.Group
	group.Go(func() error { return gw.Run(ctx) })
	t.Cleanup(func() {
		cancel()
		require.NoError(t, group.Wait())
	})

	conn, err := grpc.NewClient(
		listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	registered := ynpb.NewGatewayClient(conn)
	kinds := map[string]ynpb.BackendKind{}
	require.Eventually(t, func() bool {
		response, listErr := registered.ListServices(t.Context(), &ynpb.ListServicesRequest{})
		if listErr != nil {
			return false
		}

		kinds = map[string]ynpb.BackendKind{}
		for _, entry := range response.GetServices() {
			kinds[entry.GetBackend().GetName()] = entry.GetKind()
		}

		_, seen := kinds[ynpb.PortMetricsService_ServiceDesc.ServiceName]
		return seen
	}, 5*time.Second, 50*time.Millisecond, "gateway did not register the port metrics service")

	require.Equal(
		t,
		ynpb.BackendKind_BACKEND_KIND_BUILTIN,
		kinds[ynpb.PortMetricsService_ServiceDesc.ServiceName],
	)

	callCtx, callCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer callCancel()

	response, err := ynpb.NewPortMetricsServiceClient(conn).GetMetrics(
		callCtx, &commonpb.GetMetricsRequest{},
	)
	require.NoError(t, err)
	require.Equal(t, []string{"port_counter_value"}, metricNames(response.GetMetrics()))
}

// metricNames returns the name of each metric in order, so a response is
// compared by the families it carries rather than by value.
func metricNames(metrics []*commonpb.Metric) []string {
	names := make([]string, 0, len(metrics))
	for _, metric := range metrics {
		names = append(names, metric.GetName())
	}
	return names
}
