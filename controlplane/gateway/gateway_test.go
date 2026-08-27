package gateway_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
	"github.com/yanet-platform/yanet2/controlplane/gateway"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// sharedServerService is a test Service whose Endpoint returns "" so it is
// hosted on the gateway's own gRPC server.
type sharedServerService struct {
	name     string
	svcNames []string
}

func (m *sharedServerService) Name() string                   { return m.name }
func (m *sharedServerService) Endpoint() string               { return "" }
func (m *sharedServerService) ServicesNames() []string        { return m.svcNames }
func (m *sharedServerService) RegisterService(_ *grpc.Server) {}

// TestNewGateway_DeclaredKindsWired verifies that WithBuiltinService records
// BackendKindBuiltin and WithService records BackendKindInProcess for services
// that share the gateway's own gRPC server, regardless of endpoint being empty
// in both cases.
func TestNewGateway_DeclaredKindsWired(t *testing.T) {
	t.Parallel()

	builtinSvc := &sharedServerService{
		name:     "builtin-framework",
		svcNames: []string{"test.BuiltinService"},
	}
	inprocSvc := &sharedServerService{
		name:     "inproc-module",
		svcNames: []string{"test.InProcessService"},
	}

	cfg := gateway.DefaultConfig()
	listener := NewTestListener(t)
	gw, err := gateway.NewGateway(cfg, gateway.WithListener(listener),
		gateway.WithBuiltinService(builtinSvc),
		gateway.WithService(inprocSvc),
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

	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := ynpb.NewGatewayClient(conn)
	var response *ynpb.ListServicesResponse
	require.Eventually(t, func() bool {
		response, err = client.ListServices(t.Context(), &ynpb.ListServicesRequest{})
		return err == nil
	}, 5*time.Second, 50*time.Millisecond, "gateway did not become reachable")

	kinds := map[string]ynpb.BackendKind{}
	for _, entry := range response.GetServices() {
		kinds[entry.GetBackend().GetName()] = entry.GetKind()
	}

	// Framework services registered with WithBuiltinService must be built-in.
	require.Equal(t, ynpb.BackendKind_BACKEND_KIND_BUILTIN, kinds["controlplane.ynpb.v1.Gateway"], "controlplane.ynpb.v1.Gateway must be built-in")
	require.Equal(t, ynpb.BackendKind_BACKEND_KIND_BUILTIN, kinds["controlplane.ynpb.v1.Auth"], "controlplane.ynpb.v1.Auth must be built-in")
	require.Equal(t, ynpb.BackendKind_BACKEND_KIND_BUILTIN, kinds["test.BuiltinService"], "WithBuiltinService must yield built-in kind")

	// Module/device services registered with WithService must be in-process.
	require.Equal(t, ynpb.BackendKind_BACKEND_KIND_IN_PROCESS, kinds["test.InProcessService"], "WithService must yield in-process kind")

	cancel()
	require.NoError(t, group.Wait())
}

// NewTestListener opens an ephemeral loopback TCP listener for a test to
// pass into WithListener or hold open to occupy a port, registering a
// cleanup that closes it.
//
// It hands back the open listener rather than closing it and returning a
// bare address, which would let another process grab that port first — the
// TOCTOU this helper avoids. Cleanup tolerates an already-closed listener.
func NewTestListener(t *testing.T) net.Listener {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	t.Cleanup(func() { _ = listener.Close() })

	return listener
}

// newTestUnixSocketPath returns a unique path short enough for Unix socket
// limits, independent of the configured temporary root and test name.
//
// Cleanup preserves the prior testing.TempDir behavior and runs after both
// success and ordinary test failure. The directory contains only the socket
// entry, not diagnostic state worth preserving after the test.
func newTestUnixSocketPath(t *testing.T) string {
	t.Helper()

	directory, err := os.MkdirTemp("/tmp", "y2-")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, os.RemoveAll(directory))
	})

	return filepath.Join(directory, "s")
}

// blockingReadinessService is a fake Service whose Watch handler blocks on
// the stream context instead of returning, reproducing a server-streaming
// RPC that only ends once the client disconnects.
type blockingReadinessService struct {
	ynpb.UnimplementedReadinessServiceServer

	endpoint string
}

func (m *blockingReadinessService) Name() string     { return "blocking-readiness" }
func (m *blockingReadinessService) Endpoint() string { return m.endpoint }

func (m *blockingReadinessService) ServicesNames() []string {
	return []string{ynpb.ReadinessService_ServiceDesc.ServiceName}
}

func (m *blockingReadinessService) RegisterService(server *grpc.Server) {
	ynpb.RegisterReadinessServiceServer(server, m)
}

func (m *blockingReadinessService) Watch(
	req *readinesspb.ReadyRequest,
	stream ynpb.ReadinessService_WatchServer,
) error {
	if err := stream.Send(&readinesspb.ReadyResponse{}); err != nil {
		return err
	}

	<-stream.Context().Done()
	return stream.Context().Err()
}

// watchUntilOpen retries opening a ReadinessService.Watch stream and
// receiving its first message until both succeed, returning the live
// stream.
//
// A fresh gRPC server is not immediately reachable after Run starts, since
// the listener and the client registration both happen asynchronously, so
// the retry absorbs that startup race instead of requiring a fixed sleep.
// The caller's ctx selects the stream's lifetime: pass t.Context() to leave
// the stream open across shutdown, or a derived cancelable context to close
// the watch before the caller waits on shutdown to complete.
func watchUntilOpen(
	t *testing.T,
	ctx context.Context,
	client ynpb.ReadinessServiceClient,
) ynpb.ReadinessService_WatchClient {
	t.Helper()

	var stream ynpb.ReadinessService_WatchClient
	require.Eventually(t, func() bool {
		var watchErr error
		stream, watchErr = client.Watch(ctx, &readinesspb.ReadyRequest{})
		if watchErr != nil {
			return false
		}

		_, watchErr = stream.Recv()
		return watchErr == nil
	}, 5*time.Second, 50*time.Millisecond, "failed to open readiness watch stream")

	return stream
}

// TestGateway_Run_ShutsDownWithOpenStream verifies that Gateway.Run returns
// within a bounded time after its context is canceled, even while a client
// keeps a server-streaming ReadinessService.Watch call open, reproducing the
// dpkg-upgrade hang a bare GracefulStop causes on the gateway's own server.
func TestGateway_Run_ShutsDownWithOpenStream(t *testing.T) {
	t.Parallel()

	cfg := gateway.DefaultConfig()
	listener := NewTestListener(t)

	gw, err := gateway.NewGateway(cfg, gateway.WithLog(zap.NewNop()), gateway.WithListener(listener))
	require.NoError(t, err)
	t.Cleanup(func() { _ = gw.Close() })

	ctx, cancel := context.WithCancel(t.Context())

	var group errgroup.Group
	group.Go(func() error {
		return gw.Run(ctx)
	})

	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	// The stream is deliberately left open: the client never calls
	// CloseSend or cancels its context, matching the reproduction where an
	// open readiness watch wedges GracefulStop forever.
	_ = watchUntilOpen(t, t.Context(), ynpb.NewReadinessServiceClient(conn))

	cancel()

	runErr := make(chan error, 1)
	go func() { runErr <- group.Wait() }()

	select {
	case err := <-runErr:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Gateway.Run did not return within the shutdown grace period")
	}
}

// TestGateway_Run_DrainsReadinessOnShutdown verifies that Run drains the
// gateway's readiness tracker after its context is canceled, so an open
// readiness watch observes a shutting-down state before the server stops
// rather than only a dropped connection.
func TestGateway_Run_DrainsReadinessOnShutdown(t *testing.T) {
	t.Parallel()

	cfg := gateway.DefaultConfig()
	listener := NewTestListener(t)

	gw, err := gateway.NewGateway(cfg, gateway.WithLog(zap.NewNop()), gateway.WithListener(listener))
	require.NoError(t, err)
	t.Cleanup(func() { _ = gw.Close() })

	ctx, cancel := context.WithCancel(t.Context())

	var group errgroup.Group
	group.Go(func() error {
		return gw.Run(ctx)
	})

	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	readinessClient := ynpb.NewReadinessServiceClient(conn)
	require.Eventually(t, func() bool {
		resp, readyErr := readinessClient.Ready(t.Context(), &readinesspb.ReadyRequest{})
		if readyErr != nil {
			return false
		}
		if len(resp.GetScopes()) != 1 {
			return false
		}
		return resp.GetScopes()[0].GetState() == readinesspb.State_STATE_READY
	}, 5*time.Second, 50*time.Millisecond, "gateway did not become ready")

	watchCtx, watchCancel := context.WithCancel(t.Context())
	t.Cleanup(watchCancel)

	stream := watchUntilOpen(t, watchCtx, ynpb.NewReadinessServiceClient(conn))

	cancel()

	// A live watch client, not just the tracker's own state, proves the
	// drain reached subscribers while the server was still serving: with
	// the drain moved past the stop, this stream would instead only see
	// the connection drop once GracefulStop's grace period expires.
	drained := make(chan *readinesspb.ReadyResponse, 1)
	go func() {
		resp, recvErr := stream.Recv()
		if recvErr == nil {
			drained <- resp
		}
	}()

	select {
	case resp := <-drained:
		require.Len(t, resp.GetScopes(), 1)
		scope := resp.GetScopes()[0]
		require.Equal(t, readinesspb.State_STATE_NOT_READY, scope.GetState())
		require.Len(t, scope.GetReasons(), 1)
		require.Equal(t, "SHUTTING_DOWN", scope.GetReasons()[0].GetCode())
	case <-time.After(5 * time.Second):
		t.Fatal("readiness watch did not observe a shutting-down state before the server stopped")
	}

	watchCancel()

	runErr := make(chan error, 1)
	go func() { runErr <- group.Wait() }()

	select {
	case err := <-runErr:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Gateway.Run did not return within the shutdown grace period")
	}
}

// TestGateway_Run_BindsOwnListenerWhenNoneInjected verifies that Run falls
// back to binding cfg.Server.Endpoint itself when constructed without
// WithListener, by pointing the endpoint at an address a held-open
// NewTestListener keeps occupied for the whole test, so the bind
// deterministically fails rather than racing another process for the port.
func TestGateway_Run_BindsOwnListenerWhenNoneInjected(t *testing.T) {
	t.Parallel()

	occupied := NewTestListener(t)

	cfg := gateway.DefaultConfig()
	cfg.Server.Endpoint = occupied.Addr().String()

	gw, err := gateway.NewGateway(cfg, gateway.WithLog(zap.NewNop()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = gw.Close() })

	require.ErrorContains(t, gw.Run(t.Context()), "failed to initialize gRPC listener")
}

// TestGateway_Director_RegistryMissCarriesReasonTrailer verifies that a
// NotFound for a service the registry has no backend for keeps the exact
// "unknown service" message and also carries the errorReasonMetadataKey
// trailer, so a client can classify the miss without parsing the message.
// The message assertion is deliberate, not incidental: CLIs released
// before the trailer existed classify on that exact text, so changing it
// would break them.
func TestGateway_Director_RegistryMissCarriesReasonTrailer(t *testing.T) {
	t.Parallel()

	cfg := gateway.DefaultConfig()
	listener := NewTestListener(t)

	gw, err := gateway.NewGateway(cfg, gateway.WithLog(zap.NewNop()), gateway.WithListener(listener))
	require.NoError(t, err)
	t.Cleanup(func() { _ = gw.Close() })

	ctx, cancel := context.WithCancel(t.Context())

	var group errgroup.Group
	group.Go(func() error {
		return gw.Run(ctx)
	})

	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	var invokeErr error
	var trailer metadata.MD
	require.Eventually(t, func() bool {
		trailer = metadata.MD{}
		invokeErr = conn.Invoke(t.Context(), "/some.unknown.Service/Method", &emptypb.Empty{}, &emptypb.Empty{}, grpc.Trailer(&trailer))
		// A fresh gRPC server is not immediately reachable after Run
		// starts (see watchUntilOpen), so retry past a transient
		// Unavailable from the listener not being up yet instead of
		// requiring a fixed sleep.
		statusErr, ok := status.FromError(invokeErr)
		return ok && statusErr.Code() != codes.Unavailable
	}, 5*time.Second, 50*time.Millisecond, "failed to reach the gateway's director")

	require.Error(t, invokeErr)
	statusErr, ok := status.FromError(invokeErr)
	require.True(t, ok)
	require.Equal(t, codes.NotFound, statusErr.Code())
	require.Equal(t, "unknown service", statusErr.Message())

	require.Equal(t, []string{"service-unregistered"}, trailer.Get("x-yanet-error-reason"))

	cancel()

	runErr := make(chan error, 1)
	go func() { runErr <- group.Wait() }()

	select {
	case err := <-runErr:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Gateway.Run did not return within the shutdown grace period")
	}
}

// TestServiceRunner_Run_ShutsDownWithOpenStream verifies that
// ServiceRunner.Run returns within a bounded time after its context is
// canceled, even while a client keeps a server-streaming RPC open on the
// runner's own out-of-process gRPC server, reproducing the same hang for an
// operator's readiness stream proxied through the gateway.
func TestServiceRunner_Run_ShutsDownWithOpenStream(t *testing.T) {
	t.Parallel()

	gatewayListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	gatewayServer := grpc.NewServer()
	ynpb.RegisterGatewayServer(gatewayServer, gateway.NewGatewayService(gateway.NewBackendRegistry()))

	var gatewayGroup errgroup.Group
	gatewayGroup.Go(func() error {
		return gatewayServer.Serve(gatewayListener)
	})
	// Stop before waiting: Serve only returns once the server is stopped, and
	// t.Cleanup runs in LIFO order, so registering both steps in a single
	// cleanup keeps the ordering correct regardless of what else is
	// registered around it.
	t.Cleanup(func() {
		gatewayServer.Stop()
		_ = gatewayGroup.Wait()
	})

	backendAddr := newTestUnixSocketPath(t)
	runner := gateway.NewServiceRunner(
		&blockingReadinessService{endpoint: backendAddr},
		gatewayListener.Addr().String(),
		nil,
	)

	ctx, cancel := context.WithCancel(t.Context())

	var runnerGroup errgroup.Group
	runnerGroup.Go(func() error {
		return runner.Run(ctx)
	})

	select {
	case <-runner.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("service runner did not become ready")
	}

	conn, err := grpc.NewClient("unix://"+backendAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	// As above, the stream is deliberately left open across shutdown.
	_ = watchUntilOpen(t, t.Context(), ynpb.NewReadinessServiceClient(conn))

	cancel()

	runErr := make(chan error, 1)
	go func() { runErr <- runnerGroup.Wait() }()

	select {
	case err := <-runErr:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("ServiceRunner.Run did not return within the shutdown grace period")
	}
}

// TestGateway_RunRegistrySweeper_PreserveModeKeepsStaleExternal verifies that
// PreserveStaleBackends keeps a registered external service visible.
func TestGateway_RunRegistrySweeper_PreserveModeKeepsStaleExternal(t *testing.T) {
	t.Parallel()

	cfg := gateway.DefaultConfig()
	cfg.Registry.PreserveStaleBackends = true
	cfg.Registry.TTL = xcfg.MustNonZero(time.Millisecond)
	cfg.Registry.SweepInterval = xcfg.MustNonZero(5 * time.Millisecond)
	listener := NewTestListener(t)

	gw, err := gateway.NewGateway(cfg, gateway.WithLog(zap.NewNop()), gateway.WithListener(listener))
	require.NoError(t, err)
	t.Cleanup(func() { _ = gw.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var group errgroup.Group
	group.Go(func() error { return gw.Run(ctx) })
	t.Cleanup(func() {
		cancel()
		require.NoError(t, group.Wait())
	})

	backendAddr, entered, results := newCancelProbeServer(t)

	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	client := ynpb.NewGatewayClient(conn)
	require.Eventually(t, func() bool {
		_, err = client.ListServices(t.Context(), &ynpb.ListServicesRequest{})
		return err == nil
	}, 5*time.Second, 50*time.Millisecond, "gateway did not become reachable")

	_, err = client.Register(t.Context(), &ynpb.RegisterRequest{Backend: &ynpb.BackendDesc{
		Name: cancelProbeServiceName, Endpoint: backendAddr,
	}})
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	response, err := client.ListServices(t.Context(), &ynpb.ListServicesRequest{})
	require.NoError(t, err)
	services := response.GetServices()
	require.True(t, hasService(services, cancelProbeServiceName), "preserved entry must remain registered")

	invokeCtx, invokeCancel := context.WithTimeout(t.Context(), 5*time.Second)
	invokeDone := make(chan error, 1)
	go func() {
		invokeDone <- conn.Invoke(invokeCtx, cancelProbeFullMethod, &emptypb.Empty{}, &emptypb.Empty{})
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("preserved backend did not receive the proxied request")
	}

	invokeCancel()
	select {
	case observation := <-results:
		require.ErrorIs(t, observation.err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("preserved backend did not observe request cancellation")
	}
	select {
	case invokeErr := <-invokeDone:
		require.Equal(t, codes.Canceled, status.Code(invokeErr))
	case <-time.After(5 * time.Second):
		t.Fatal("proxied request did not complete after cancellation")
	}

	cancel()
	require.NoError(t, group.Wait())
}

// TestGateway_RunRegistrySweeper_EvictsStaleExternal verifies that the
// gateway removes an external service after its TTL expires.
func TestGateway_RunRegistrySweeper_EvictsStaleExternal(t *testing.T) {
	t.Parallel()

	cfg := gateway.DefaultConfig()
	cfg.Registry.TTL = xcfg.MustNonZero(time.Millisecond)
	cfg.Registry.SweepInterval = xcfg.MustNonZero(5 * time.Millisecond)
	listener := NewTestListener(t)

	gw, err := gateway.NewGateway(cfg, gateway.WithLog(zap.NewNop()), gateway.WithListener(listener))
	require.NoError(t, err)
	t.Cleanup(func() { _ = gw.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	var group errgroup.Group
	group.Go(func() error { return gw.Run(ctx) })
	t.Cleanup(func() {
		cancel()
		require.NoError(t, group.Wait())
	})

	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	client := ynpb.NewGatewayClient(conn)
	require.Eventually(t, func() bool {
		_, err = client.ListServices(t.Context(), &ynpb.ListServicesRequest{})
		return err == nil
	}, 5*time.Second, 50*time.Millisecond, "gateway did not become reachable")

	_, err = client.Register(t.Context(), &ynpb.RegisterRequest{Backend: &ynpb.BackendDesc{
		Name: "svc.Foo", Endpoint: "127.0.0.1:9000",
	}})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		response, listErr := client.ListServices(t.Context(), &ynpb.ListServicesRequest{})
		return listErr == nil && !hasService(response.GetServices(), "svc.Foo")
	}, time.Second, 5*time.Millisecond, "stale external backend must be evicted")

	cancel()
	require.NoError(t, group.Wait())
}

// TestGateway_RunRegistrySweeper_ZeroSweepIntervalFallsBack verifies that a
// zero sweep interval does not panic when Gateway.Run starts the sweeper.
func TestGateway_RunRegistrySweeper_ZeroSweepIntervalFallsBack(t *testing.T) {
	t.Parallel()

	cfg := gateway.DefaultConfig()
	cfg.Registry.SweepInterval = xcfg.NonZero[time.Duration]{}
	listener := NewTestListener(t)

	gw, err := gateway.NewGateway(cfg, gateway.WithLog(zap.NewNop()), gateway.WithListener(listener))
	require.NoError(t, err)
	t.Cleanup(func() { _ = gw.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	var group errgroup.Group
	group.Go(func() error { return gw.Run(ctx) })

	require.NoError(t, group.Wait())
}

// TestGateway_RunRegistrySweeper_ZeroTTLFallsBack verifies that a zero TTL
// does not evict a live external service on the first sweep.
func TestGateway_RunRegistrySweeper_ZeroTTLFallsBack(t *testing.T) {
	t.Parallel()

	cfg := gateway.DefaultConfig()
	cfg.Registry.TTL = xcfg.NonZero[time.Duration]{}
	cfg.Registry.SweepInterval = xcfg.MustNonZero(5 * time.Millisecond)
	listener := NewTestListener(t)

	gw, err := gateway.NewGateway(cfg, gateway.WithLog(zap.NewNop()), gateway.WithListener(listener))
	require.NoError(t, err)
	t.Cleanup(func() { _ = gw.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	var group errgroup.Group
	group.Go(func() error { return gw.Run(ctx) })
	t.Cleanup(func() {
		cancel()
		require.NoError(t, group.Wait())
	})

	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	client := ynpb.NewGatewayClient(conn)
	require.Eventually(t, func() bool {
		_, err = client.ListServices(t.Context(), &ynpb.ListServicesRequest{})
		return err == nil
	}, 5*time.Second, 50*time.Millisecond, "gateway did not become reachable")

	_, err = client.Register(t.Context(), &ynpb.RegisterRequest{Backend: &ynpb.BackendDesc{
		Name: "svc.Foo", Endpoint: "127.0.0.1:9000",
	}})
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	response, err := client.ListServices(t.Context(), &ynpb.ListServicesRequest{})
	require.NoError(t, err)
	require.True(t, hasService(response.GetServices(), "svc.Foo"), "live entry must survive the fallback ttl")

	cancel()
	require.NoError(t, group.Wait())
}

func hasService(services []*ynpb.RegisteredBackend, name string) bool {
	for _, service := range services {
		if service.GetBackend().GetName() == name {
			return true
		}
	}

	return false
}
