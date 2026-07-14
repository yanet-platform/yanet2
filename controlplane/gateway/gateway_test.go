package gateway

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

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
	ynpb.RegisterGatewayServer(gatewayServer, NewGatewayService(NewBackendRegistry(), zap.NewNop()))

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
		zap.NewNop(),
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
