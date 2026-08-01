package gateway

import (
	"context"
	"fmt"
	"net"
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

	cfg := DefaultConfig()
	gw, err := NewGateway(cfg,
		WithBuiltinService(builtinSvc),
		WithService(inprocSvc),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = gw.Close() })

	entries := gw.registry.ListBackends()
	kinds := map[string]BackendKind{}
	for _, e := range entries {
		kinds[e.Service()] = e.Kind()
	}

	// Framework services registered with WithBuiltinService must be built-in.
	require.Equal(t, BackendKindBuiltin, kinds["controlplane.ynpb.v1.Gateway"], "controlplane.ynpb.v1.Gateway must be built-in")
	require.Equal(t, BackendKindBuiltin, kinds["controlplane.ynpb.v1.Auth"], "controlplane.ynpb.v1.Auth must be built-in")
	require.Equal(t, BackendKindBuiltin, kinds["test.BuiltinService"], "WithBuiltinService must yield built-in kind")

	// Module/device services registered with WithService must be in-process.
	require.Equal(t, BackendKindInProcess, kinds["test.InProcessService"], "WithService must yield in-process kind")
}

// freeTCPAddr reserves and immediately releases an ephemeral TCP port so a
// server that binds it later gets a concrete, otherwise-unused endpoint.
func freeTCPAddr(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := listener.Addr().String()
	require.NoError(t, listener.Close())

	return addr
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
func watchUntilOpen(t *testing.T, client ynpb.ReadinessServiceClient) {
	t.Helper()

	var stream ynpb.ReadinessService_WatchClient
	require.Eventually(t, func() bool {
		var watchErr error
		stream, watchErr = client.Watch(t.Context(), &readinesspb.ReadyRequest{})
		if watchErr != nil {
			return false
		}

		_, watchErr = stream.Recv()
		return watchErr == nil
	}, 5*time.Second, 50*time.Millisecond, "failed to open readiness watch stream")
}

// TestGateway_Run_ShutsDownWithOpenStream verifies that Gateway.Run returns
// within a bounded time after its context is canceled, even while a client
// keeps a server-streaming ReadinessService.Watch call open, reproducing the
// dpkg-upgrade hang a bare GracefulStop causes on the gateway's own server.
func TestGateway_Run_ShutsDownWithOpenStream(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.Server.Endpoint = freeTCPAddr(t)

	gw, err := NewGateway(cfg, WithLog(zap.NewNop()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = gw.Close() })

	ctx, cancel := context.WithCancel(t.Context())

	var group errgroup.Group
	group.Go(func() error {
		return gw.Run(ctx)
	})

	conn, err := grpc.NewClient(cfg.Server.Endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	// The stream is deliberately left open: the client never calls
	// CloseSend or cancels its context, matching the reproduction where an
	// open readiness watch wedges GracefulStop forever.
	watchUntilOpen(t, ynpb.NewReadinessServiceClient(conn))

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

// TestGateway_Director_RegistryMissCarriesReasonTrailer verifies that a
// NotFound for a service the registry has no backend for keeps the exact
// "unknown service" message and also carries the errorReasonMetadataKey
// trailer, so a client can classify the miss without parsing the message.
// The message assertion is deliberate, not incidental: CLIs released
// before the trailer existed classify on that exact text, so changing it
// would break them.
func TestGateway_Director_RegistryMissCarriesReasonTrailer(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.Server.Endpoint = freeTCPAddr(t)

	gw, err := NewGateway(cfg, WithLog(zap.NewNop()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = gw.Close() })

	ctx, cancel := context.WithCancel(t.Context())

	var group errgroup.Group
	group.Go(func() error {
		return gw.Run(ctx)
	})

	conn, err := grpc.NewClient(cfg.Server.Endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
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
	ynpb.RegisterGatewayServer(gatewayServer, NewGatewayService(NewBackendRegistry()))

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

	backendAddr := freeTCPAddr(t)
	runner := NewServiceRunner(
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

	conn, err := grpc.NewClient(backendAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	// As above, the stream is deliberately left open across shutdown.
	watchUntilOpen(t, ynpb.NewReadinessServiceClient(conn))

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

// TestGateway_RunRegistrySweeper_PreserveModeShortCircuits verifies that
// PreserveStaleBackends returns immediately without touching a long-stale
// external entry.
func TestGateway_RunRegistrySweeper_PreserveModeShortCircuits(t *testing.T) {
	t.Parallel()

	reg := NewBackendRegistry()
	b := &fakeBackend{endpoint: "127.0.0.1:9000"}
	reg.RegisterBackend("svc.Foo", b, BackendKindExternal)

	entry := reg.backends["svc.Foo"]
	entry.lastSeenAt = time.Now().UTC().Add(-24 * time.Hour)
	reg.backends["svc.Foo"] = entry

	gw := &Gateway{
		cfg: &Config{
			Registry: RegistryConfig{
				PreserveStaleBackends: true,
				TTL:                   xcfg.MustNonZero(time.Minute),
				SweepInterval:         xcfg.MustNonZero(time.Second),
			},
		},
		registry: reg,
		log:      zap.NewNop(),
	}

	done := make(chan error, 1)
	go func() { done <- gw.runRegistrySweeper(t.Context()) }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("runRegistrySweeper did not return promptly in preserve mode")
	}

	require.False(t, b.Closed(), "preserved backend must not be closed")

	_, ok := reg.backends["svc.Foo"]
	require.True(t, ok, "preserved entry must remain registered")
}

// TestGateway_RunRegistrySweeper_EvictsStaleExternal verifies that a stale
// external entry is evicted and its backend closed once the sweeper's
// ticker fires.
func TestGateway_RunRegistrySweeper_EvictsStaleExternal(t *testing.T) {
	t.Parallel()

	reg := NewBackendRegistry()
	b := &fakeBackend{endpoint: "127.0.0.1:9000"}
	reg.RegisterBackend("svc.Foo", b, BackendKindExternal)

	entry := reg.backends["svc.Foo"]
	entry.lastSeenAt = time.Now().UTC().Add(-time.Hour)
	reg.backends["svc.Foo"] = entry

	gw := &Gateway{
		cfg: &Config{
			Registry: RegistryConfig{
				TTL:           xcfg.MustNonZero(time.Millisecond),
				SweepInterval: xcfg.MustNonZero(5 * time.Millisecond),
			},
		},
		registry: reg,
		log:      zap.NewNop(),
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- gw.runRegistrySweeper(ctx) }()

	require.Eventually(t, func() bool {
		return b.Closed()
	}, time.Second, 5*time.Millisecond, "stale external backend must be evicted and closed")

	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("runRegistrySweeper did not return after cancel")
	}

	_, ok := reg.backends["svc.Foo"]
	require.False(t, ok, "evicted entry must be removed from the registry")
}

// TestGateway_RunRegistrySweeper_ZeroSweepIntervalFallsBack verifies that a
// programmatic RegistryConfig with a zero SweepInterval falls back to the
// default instead of panicking.
//
// A zero interval reaches time.NewTicker directly, which panics; the
// fallback is the guard against that.
func TestGateway_RunRegistrySweeper_ZeroSweepIntervalFallsBack(t *testing.T) {
	t.Parallel()

	reg := NewBackendRegistry()

	gw := &Gateway{
		cfg: &Config{
			Registry: RegistryConfig{
				TTL: xcfg.MustNonZero(time.Minute),
			},
		},
		registry: reg,
		log:      zap.NewNop(),
	}

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("runRegistrySweeper panicked: %v", r)
			}
		}()
		done <- gw.runRegistrySweeper(ctx)
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("runRegistrySweeper did not return after context timeout")
	}
}

// TestGateway_RunRegistrySweeper_ZeroTTLFallsBack verifies that a
// programmatic RegistryConfig with a zero TTL falls back to the default
// instead of evicting a live entry on the first sweep.
//
// The sweep interval is small and positive so a sweep genuinely runs before
// the test's deadline; the fallback TTL must still land far enough in the
// past to spare an entry seen moments ago, or the cutoff would land at or
// after time.Now and wipe every live backend on the first sweep.
func TestGateway_RunRegistrySweeper_ZeroTTLFallsBack(t *testing.T) {
	t.Parallel()

	reg := NewBackendRegistry()
	b := &fakeBackend{endpoint: "127.0.0.1:9000"}
	reg.RegisterBackend("svc.Foo", b, BackendKindExternal)

	gw := &Gateway{
		cfg: &Config{
			Registry: RegistryConfig{
				SweepInterval: xcfg.MustNonZero(5 * time.Millisecond),
			},
		},
		registry: reg,
		log:      zap.NewNop(),
	}

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- gw.runRegistrySweeper(ctx) }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("runRegistrySweeper did not return after context timeout")
	}

	require.False(t, b.Closed(), "live entry must survive the fallback ttl")

	_, ok := reg.backends["svc.Foo"]
	require.True(t, ok, "live entry must remain registered")
}
