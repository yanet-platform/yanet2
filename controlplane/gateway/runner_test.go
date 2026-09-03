package gateway_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/yanet-platform/yanet2/controlplane/gateway"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// healthService is a Service exposing the standard gRPC health checker
// under a module name, with an endpoint of its own the runner must ignore.
type healthService struct{}

func (m *healthService) Name() string     { return "health-module" }
func (m *healthService) Endpoint() string { return "127.0.0.1:0" }

func (m *healthService) ServicesNames() []string {
	return []string{grpc_health_v1.Health_ServiceDesc.ServiceName}
}

func (m *healthService) RegisterService(server *grpc.Server) {
	grpc_health_v1.RegisterHealthServer(server, health.NewServer())
}

// startServiceRunner runs runner until the test ends, failing the test if
// it does not register within a bounded time or exits with an error.
func startServiceRunner(t *testing.T, runner *gateway.InProcessServiceRunner) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	var group errgroup.Group
	group.Go(func() error { return runner.Run(ctx) })
	t.Cleanup(func() {
		cancel()
		require.NoError(t, group.Wait())
	})

	select {
	case <-runner.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("service runner did not become ready")
	}
}

// Test_ServiceRunner_Run_ServesThroughRegistry verifies that a runner answers
// RPCs through the registry's connection to it.
//
// The services are recorded as in-process backends labeled with the
// gateway's endpoint, the address they are reachable at from outside.
func Test_ServiceRunner_Run_ServesThroughRegistry(t *testing.T) {
	t.Parallel()

	const gatewayEndpoint = "gateway.test:8080"
	serviceName := grpc_health_v1.Health_ServiceDesc.ServiceName

	registry := gateway.NewBackendRegistry()
	t.Cleanup(func() { _ = registry.Close() })

	startServiceRunner(t, gateway.NewInProcessServiceRunner(&healthService{}, registry, gatewayEndpoint))

	entry := getBackendEntry(t, registry, serviceName)
	require.Equal(t, gateway.BackendKindInProcess, entry.Kind())
	require.Equal(t, gatewayEndpoint, entry.Endpoint())

	backend, release, ok := registry.GetBackend(serviceName)
	require.True(t, ok)
	defer release()

	ctx, conn, err := backend.GetConnection(t.Context(), "/"+serviceName+"/Check")
	require.NoError(t, err)

	response, err := grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	require.NoError(t, err)
	require.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, response.GetStatus())
}

// Test_ServiceRunner_Run_ShutsDownWithOpenStream verifies that the runner
// returns within a bounded time after its context is canceled.
//
// A client keeps a server-streaming RPC open on the runner's own gRPC server
// meanwhile, reproducing the hang an unattended readiness watch causes on
// shutdown.
func Test_ServiceRunner_Run_ShutsDownWithOpenStream(t *testing.T) {
	t.Parallel()

	registry := gateway.NewBackendRegistry()
	t.Cleanup(func() { _ = registry.Close() })

	runner := gateway.NewInProcessServiceRunner(&blockingReadinessService{}, registry, "gateway.test:8080")

	ctx, cancel := context.WithCancel(t.Context())
	var group errgroup.Group
	group.Go(func() error { return runner.Run(ctx) })

	select {
	case <-runner.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("service runner did not become ready")
	}

	backend, release, ok := registry.GetBackend(ynpbReadinessServiceName)
	require.True(t, ok)
	defer release()

	streamCtx, conn, err := backend.GetConnection(t.Context(), "/"+ynpbReadinessServiceName+"/Watch")
	require.NoError(t, err)

	// The stream is deliberately left open across shutdown.
	_ = watchUntilOpen(t, streamCtx, ynpb.NewReadinessServiceClient(conn))

	cancel()

	runErr := make(chan error, 1)
	go func() { runErr <- group.Wait() }()

	select {
	case err := <-runErr:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("service runner did not return within the shutdown grace period")
	}
}
