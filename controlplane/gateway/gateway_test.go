package gateway_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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

// kindProbeService is a test Service with no endpoint and no gRPC services
// of its own, registered only to observe the kind each option assigns it.
type kindProbeService struct {
	name     string
	svcNames []string
}

func (m *kindProbeService) Name() string                   { return m.name }
func (m *kindProbeService) Endpoint() string               { return "" }
func (m *kindProbeService) ServicesNames() []string        { return m.svcNames }
func (m *kindProbeService) RegisterService(_ *grpc.Server) {}

// TestNewGateway_DeclaredKindsWired verifies that WithBuiltinService records
// BackendKindBuiltin and WithService records BackendKindInProcess.
//
// The kind follows the registration option alone: both probes report an
// empty endpoint, and the framework one shares the gateway's server while
// the module one gets an in-memory server of its own.
func TestNewGateway_DeclaredKindsWired(t *testing.T) {
	t.Parallel()

	builtinSvc := &kindProbeService{
		name:     "builtin-framework",
		svcNames: []string{"test.BuiltinService"},
	}
	inprocSvc := &kindProbeService{
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

	// The module probe is registered by its runner after Run starts, so the
	// listing is polled until both probes are present, not merely until the
	// gateway answers.
	client := ynpb.NewGatewayClient(conn)
	kinds := map[string]ynpb.BackendKind{}
	require.Eventually(t, func() bool {
		response, listErr := client.ListServices(t.Context(), &ynpb.ListServicesRequest{})
		if listErr != nil {
			return false
		}

		kinds = map[string]ynpb.BackendKind{}
		for _, entry := range response.GetServices() {
			kinds[entry.GetBackend().GetName()] = entry.GetKind()
		}

		_, builtinSeen := kinds["test.BuiltinService"]
		_, inprocSeen := kinds["test.InProcessService"]
		return builtinSeen && inprocSeen
	}, 5*time.Second, 50*time.Millisecond, "gateway did not register both probes")

	// Framework services registered with WithBuiltinService must be built-in.
	require.Equal(t, ynpb.BackendKind_BACKEND_KIND_BUILTIN, kinds["controlplane.ynpb.v1.Gateway"], "controlplane.ynpb.v1.Gateway must be built-in")
	require.Equal(t, ynpb.BackendKind_BACKEND_KIND_BUILTIN, kinds["controlplane.ynpb.v1.Auth"], "controlplane.ynpb.v1.Auth must be built-in")
	require.Equal(t, ynpb.BackendKind_BACKEND_KIND_BUILTIN, kinds["test.BuiltinService"], "WithBuiltinService must yield built-in kind")

	// Module/device services registered with WithService must be in-process.
	require.Equal(t, ynpb.BackendKind_BACKEND_KIND_IN_PROCESS, kinds["test.InProcessService"], "WithService must yield in-process kind")

	cancel()
	require.NoError(t, group.Wait())
}

// newFreeAddress returns a loopback address that was free when checked, for
// a server that has to bind a listener of its own.
func newFreeAddress(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	address := listener.Addr().String()
	require.NoError(t, listener.Close())

	return address
}

// newTestServerTLS issues a throwaway self-signed certificate for 127.0.0.1,
// writes the PEM pair under the test's temporary directory and returns the
// file paths with a pool trusting that certificate.
func newTestServerTLS(t *testing.T) (certFile, keyFile string, pool *x509.CertPool) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "gateway.test"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)

	dir := t.TempDir()
	certFile = filepath.Join(dir, "server.pem")
	keyFile = filepath.Join(dir, "server.key")
	require.NoError(t, os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600))

	leaf, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	pool = x509.NewCertPool()
	pool.AddCert(leaf)

	return certFile, keyFile, pool
}

// Test_Gateway_HTTPProxy_ReachesBuiltinOverTLS verifies that with server TLS
// configured the HTTP surface still reaches gateway-hosted services.
//
// Server credentials apply to every listener, the in-memory one included, so
// the in-process loopback has to complete the handshake they impose.
func Test_Gateway_HTTPProxy_ReachesBuiltinOverTLS(t *testing.T) {
	t.Parallel()

	certFile, keyFile, pool := newTestServerTLS(t)

	cfg := gateway.DefaultConfig()
	cfg.Server.HTTPEndpoint = newFreeAddress(t)
	cfg.Server.TLS = &gateway.TLSConfig{
		CertFile: xcfg.MustNonEmptyString(certFile),
		KeyFile:  xcfg.MustNonEmptyString(keyFile),
	}

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

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
		Timeout: 5 * time.Second,
	}
	url := "https://" + cfg.Server.HTTPEndpoint + "/api/controlplane.ynpb.v1.Gateway/ListServices"

	var body []byte
	var statusCode int
	require.Eventually(t, func() bool {
		response, postErr := client.Post(url, "application/json", strings.NewReader("{}"))
		if postErr != nil {
			return false
		}
		defer func() { _ = response.Body.Close() }()

		body, postErr = io.ReadAll(response.Body)
		statusCode = response.StatusCode
		return postErr == nil
	}, 5*time.Second, 50*time.Millisecond, "HTTP proxy did not answer")

	require.Equal(t, http.StatusOK, statusCode, string(body))
	require.Contains(t, string(body), "controlplane.ynpb.v1.Gateway")
}

// lifecycleService is a Service recording that its background job ran and
// that it was closed, under a unique gRPC service name.
type lifecycleService struct {
	name   string
	ran    chan struct{}
	closed chan struct{}
}

func newLifecycleService(name string) *lifecycleService {
	return &lifecycleService{
		name:   name,
		ran:    make(chan struct{}),
		closed: make(chan struct{}),
	}
}

func (m *lifecycleService) Name() string                   { return m.name }
func (m *lifecycleService) Endpoint() string               { return "" }
func (m *lifecycleService) ServicesNames() []string        { return []string{"test." + m.name} }
func (m *lifecycleService) RegisterService(_ *grpc.Server) {}

func (m *lifecycleService) Run(ctx context.Context) error {
	close(m.ran)
	<-ctx.Done()
	return nil
}

func (m *lifecycleService) Close() error {
	close(m.closed)
	return nil
}

// Test_Gateway_HostsRunAndCloseEveryService verifies that the gateway runs
// the background job of a framework service and of a module service alike,
// and closes both on shutdown.
func Test_Gateway_HostsRunAndCloseEveryService(t *testing.T) {
	t.Parallel()

	builtin := newLifecycleService("builtin")
	module := newLifecycleService("module")

	gw, err := gateway.NewGateway(gateway.DefaultConfig(),
		gateway.WithListener(NewTestListener(t)),
		gateway.WithBuiltinService(builtin),
		gateway.WithService(module),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	var group errgroup.Group
	group.Go(func() error { return gw.Run(ctx) })

	for _, service := range []*lifecycleService{builtin, module} {
		select {
		case <-service.ran:
		case <-time.After(5 * time.Second):
			t.Fatalf("background job of %s did not start", service.name)
		}
	}

	cancel()
	require.NoError(t, group.Wait())
	require.NoError(t, gw.Close())

	for _, service := range []*lifecycleService{builtin, module} {
		select {
		case <-service.closed:
		default:
			t.Fatalf("%s was not closed", service.name)
		}
	}
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

// ynpbReadinessServiceName is the full name the readiness service is
// registered under.
var ynpbReadinessServiceName = ynpb.ReadinessService_ServiceDesc.ServiceName

// blockingReadinessService is a fake Service whose Watch handler blocks on
// the stream context instead of returning, reproducing a server-streaming
// RPC that only ends once the client disconnects.
type blockingReadinessService struct {
	ynpb.UnimplementedReadinessServiceServer
}

func (m *blockingReadinessService) Name() string     { return "blocking-readiness" }
func (m *blockingReadinessService) Endpoint() string { return "" }

func (m *blockingReadinessService) ServicesNames() []string {
	return []string{ynpbReadinessServiceName}
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
