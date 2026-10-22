package gateway_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/common/go/testutils/tlscert"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/common/go/xgrpc"
	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
	"github.com/yanet-platform/yanet2/controlplane/gateway"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/sshcert"
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
	require.Equal(t, ynpb.BackendKind_BACKEND_KIND_BUILTIN, kinds[ynpb.Gateway_ServiceDesc.ServiceName], "Gateway must be built-in")
	require.Equal(t, ynpb.BackendKind_BACKEND_KIND_BUILTIN, kinds[ynpb.AuthService_ServiceDesc.ServiceName], "AuthService must be built-in")
	require.NotContains(t, kinds, "controlplane.ynpb.v1.Auth", "stale Auth identity must be absent")
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

// newTestServerTLS issues a throwaway server certificate for 127.0.0.1 from
// a private CA and returns its PEM files with a pool trusting that CA.
func newTestServerTLS(t *testing.T) (certFile, keyFile string, pool *x509.CertPool) {
	t.Helper()

	ca := tlscert.NewCA(t)
	server := ca.IssueServer(t, "127.0.0.1")

	return server.CertFile, server.KeyFile, ca.Pool()
}

// Test_Gateway_HTTPProxy_ReachesBuiltinOverTLS verifies that with server TLS
// configured the HTTP surface still reaches gateway-hosted services.
func Test_Gateway_HTTPProxy_ReachesBuiltinOverTLS(t *testing.T) {
	t.Parallel()

	certFile, keyFile, pool := newTestServerTLS(t)

	cfg := gateway.DefaultConfig()
	cfg.Server.HTTPEndpoint = newFreeAddress(t)
	cfg.Server.TLS = &gateway.TLSConfig{
		KeyPairs: []xgrpc.KeyPairConfig{tlscert.FileKeyPair(t, certFile, keyFile)},
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

// newClientTLSCredentials returns client transport credentials trusting
// only the certificates in pool.
func newClientTLSCredentials(pool *x509.CertPool) credentials.TransportCredentials {
	return credentials.NewTLS(&tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12})
}

// runGateway runs a gateway built from cfg and the given options until the
// test ends, failing the test if the run ends with an error.
//
// A silent log applies before the given options, so they may replace it.
func runGateway(t *testing.T, cfg gateway.Config, options ...gateway.GatewayOption) {
	t.Helper()

	defaults := []gateway.GatewayOption{gateway.WithLog(zap.NewNop())}
	gw, err := gateway.NewGateway(cfg, append(defaults, options...)...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = gw.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	var group errgroup.Group
	group.Go(func() error { return gw.Run(ctx) })
	t.Cleanup(func() {
		cancel()
		require.NoError(t, group.Wait())
	})
}

// newTLSListenerConfig returns a TLS listener config at endpoint serving the
// given PEM pair.
func newTLSListenerConfig(t *testing.T, endpoint, certFile, keyFile string) *gateway.TLSListenerConfig {
	t.Helper()

	return &gateway.TLSListenerConfig{
		Endpoint: xcfg.MustNonEmptyString(endpoint),
		TLSConfig: gateway.TLSConfig{
			KeyPairs: []xgrpc.KeyPairConfig{tlscert.FileKeyPair(t, certFile, keyFile)},
		},
	}
}

// startDedicatedTLSGateway runs a gateway whose gRPC section serves
// plaintext on one listener and TLS with the given PEM pair on another until
// the test ends, returning the plaintext listener and then the TLS one.
//
// Both listeners are pre-bound and injected in place of the section's
// endpoints. The given options apply after the listeners and a silent log.
func startDedicatedTLSGateway(
	t *testing.T,
	cfg gateway.Config,
	certFile, keyFile string,
	options ...gateway.GatewayOption,
) (net.Listener, net.Listener) {
	t.Helper()

	plaintext := NewTestListener(t)
	secure := NewTestListener(t)
	cfg.Server.GRPC = &gateway.ListenerConfig{
		Endpoint: plaintext.Addr().String(),
		TLS:      newTLSListenerConfig(t, secure.Addr().String(), certFile, keyFile),
	}

	listeners := []gateway.GatewayOption{
		gateway.WithListener(plaintext),
		gateway.WithTLSListener(secure),
	}
	runGateway(t, cfg, append(listeners, options...)...)

	return plaintext, secure
}

// dialGateway opens a client connection to address with the given transport
// credentials, closed when the test ends.
func dialGateway(t *testing.T, address string, transportCredentials credentials.TransportCredentials) *grpc.ClientConn {
	t.Helper()

	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(transportCredentials))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

// requireListServices fails the test unless conn reaches the gateway's own
// service listing within a bounded wait.
func requireListServices(t *testing.T, conn *grpc.ClientConn) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	_, err := ynpb.NewGatewayClient(conn).ListServices(ctx, &ynpb.ListServicesRequest{}, grpc.WaitForReady(true))
	require.NoError(t, err)
}

// requireHTTPListServices fails the test unless the HTTP surface at baseURL
// answers the gateway's own service listing within a bounded wait.
func requireHTTPListServices(t *testing.T, client *http.Client, baseURL string) {
	t.Helper()

	url := baseURL + "/api/controlplane.ynpb.v1.Gateway/ListServices"

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

// Test_Gateway_DedicatedTLS_ServesEveryBackendKindOnBothListeners verifies
// that with a gRPC section serving plaintext and TLS side by side, a
// plaintext and a TLS client alike reach a built-in, an in-process and an
// external service.
func Test_Gateway_DedicatedTLS_ServesEveryBackendKindOnBothListeners(t *testing.T) {
	t.Parallel()

	certFile, keyFile, pool := newTestServerTLS(t)
	plaintextListener, tlsListener := startDedicatedTLSGateway(
		t, gateway.DefaultConfig(), certFile, keyFile, gateway.WithService(&healthService{}),
	)

	backendListener := NewTestListener(t)
	backend := grpc.NewServer()
	backend.RegisterService(&cancelProbeServiceDesc, &auditProbeServer{})
	go func() { _ = backend.Serve(backendListener) }()
	t.Cleanup(backend.Stop)

	plaintext := dialGateway(t, plaintextListener.Addr().String(), insecure.NewCredentials())
	secure := dialGateway(t, tlsListener.Addr().String(), newClientTLSCredentials(pool))

	request := &ynpb.RegisterRequest{Backend: &ynpb.BackendDesc{
		Name:     cancelProbeServiceName,
		Endpoint: backendListener.Addr().String(),
	}}
	registerCtx, cancelRegister := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelRegister()
	_, err := ynpb.NewGatewayClient(plaintext).Register(registerCtx, request, grpc.WaitForReady(true))
	require.NoError(t, err)

	clients := []struct {
		name string
		conn *grpc.ClientConn
	}{
		{name: "plaintext", conn: plaintext},
		{name: "TLS", conn: secure},
	}
	cases := []struct {
		name  string
		reach func(t *testing.T, conn *grpc.ClientConn)
	}{
		{
			name:  "built-in",
			reach: requireListServices,
		},
		{
			name: "in-process",
			reach: func(t *testing.T, conn *grpc.ClientConn) {
				// The runner registers the module after Run starts, so the
				// check is retried while the service is still unknown, each
				// attempt under a deadline of its own.
				var response *grpc_health_v1.HealthCheckResponse
				require.Eventually(t, func() bool {
					ctx, cancel := context.WithTimeout(t.Context(), time.Second)
					defer cancel()

					var checkErr error
					response, checkErr = grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
					return checkErr == nil
				}, 5*time.Second, 50*time.Millisecond, "in-process service did not answer")
				require.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, response.GetStatus())
			},
		},
		{
			name: "external",
			reach: func(t *testing.T, conn *grpc.ClientConn) {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()

				err := conn.Invoke(ctx, cancelProbeFullMethod, &emptypb.Empty{}, &emptypb.Empty{})
				require.Equal(t, codes.FailedPrecondition, status.Code(err), "backend rejection must come back: %v", err)
			},
		},
	}

	for _, client := range clients {
		for _, tc := range cases {
			t.Run(client.name+" client reaches "+tc.name+" service", func(t *testing.T) {
				tc.reach(t, client.conn)
			})
		}
	}
}

// Test_Gateway_DedicatedTLS_TLSListenerRefusesPlaintextClient verifies that
// a plaintext client is not served on the dedicated TLS listener, so the TLS
// port never serves plaintext.
func Test_Gateway_DedicatedTLS_TLSListenerRefusesPlaintextClient(t *testing.T) {
	t.Parallel()

	certFile, keyFile, pool := newTestServerTLS(t)
	plaintextListener, tlsListener := startDedicatedTLSGateway(t, gateway.DefaultConfig(), certFile, keyFile)
	tlsAddress := tlsListener.Addr().String()

	// Both listeners already serve their own protocol, so the refusal below
	// comes from the mismatch rather than from a gateway still starting.
	requireListServices(t, dialGateway(t, plaintextListener.Addr().String(), insecure.NewCredentials()))
	requireListServices(t, dialGateway(t, tlsAddress, newClientTLSCredentials(pool)))

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	client := ynpb.NewGatewayClient(dialGateway(t, tlsAddress, insecure.NewCredentials()))
	_, err := client.ListServices(ctx, &ynpb.ListServicesRequest{})
	require.Equal(t, codes.Unavailable, status.Code(err), "%v", err)
}

// Test_Gateway_SectionsFromYAML_ReplaceDeprecatedEndpoint verifies that Run
// binds the gRPC and HTTP sections decoded from YAML, plaintext and TLS
// alike, and ignores the deprecated endpoint beside them, which points at an
// occupied address.
func Test_Gateway_SectionsFromYAML_ReplaceDeprecatedEndpoint(t *testing.T) {
	t.Parallel()

	certFile, keyFile, pool := newTestServerTLS(t)
	occupied := NewTestListener(t)
	plaintextAddress := newFreeAddress(t)
	tlsAddress := newFreeAddress(t)
	httpAddress := newFreeAddress(t)

	text := "endpoint: " + strconv.Quote(occupied.Addr().String()) + "\n" +
		"grpc:\n" +
		"  endpoint: " + strconv.Quote(plaintextAddress) + "\n" +
		"  tls:\n" +
		"    endpoint: " + strconv.Quote(tlsAddress) + "\n" +
		"    key_pairs:\n" +
		"      - type: file\n" +
		"        config:\n" +
		"          cert_file: " + strconv.Quote(certFile) + "\n" +
		"          key_file: " + strconv.Quote(keyFile) + "\n" +
		"http:\n" +
		"  endpoint: " + strconv.Quote(httpAddress) + "\n"
	cfg := gateway.DefaultConfig()
	require.NoError(t, xcfg.Decode([]byte(text), &cfg.Server, xcfg.WithKnownFields()))

	runGateway(t, cfg)

	requireListServices(t, dialGateway(t, plaintextAddress, insecure.NewCredentials()))
	requireListServices(t, dialGateway(t, tlsAddress, newClientTLSCredentials(pool)))
	requireHTTPListServices(t, &http.Client{Timeout: 5 * time.Second}, "http://"+httpAddress)
}

// Test_Gateway_TLSOnly_HTTPSReachesBuiltin verifies that with gRPC served
// over TLS alone, the HTTPS listener of the HTTP section still reaches a
// built-in service through the in-memory loopback.
func Test_Gateway_TLSOnly_HTTPSReachesBuiltin(t *testing.T) {
	t.Parallel()

	certFile, keyFile, pool := newTestServerTLS(t)
	secure := NewTestListener(t)
	httpsAddress := newFreeAddress(t)

	cfg := gateway.DefaultConfig()
	cfg.Server.GRPC = &gateway.ListenerConfig{
		TLS: newTLSListenerConfig(t, secure.Addr().String(), certFile, keyFile),
	}
	cfg.Server.HTTP = &gateway.ListenerConfig{
		TLS: newTLSListenerConfig(t, httpsAddress, certFile, keyFile),
	}
	runGateway(t, cfg, gateway.WithTLSListener(secure))

	requireListServices(t, dialGateway(t, secure.Addr().String(), newClientTLSCredentials(pool)))

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
		Timeout: 5 * time.Second,
	}
	requireHTTPListServices(t, client, "https://"+httpsAddress)
}

// lifecycleService is a Service recording that its background job ran and
// that it was closed, under a unique gRPC service name.
type lifecycleService struct {
	name   string
	ran    chan struct{}
	closed chan struct{}
}

// newLifecycleService returns a service whose run and close markers fire
// exactly once, so it expects one gateway run and one close.
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

// validateProbeGatewayService is a builtin Service that registers the probe
// service directly on the gateway's own gRPC server.
//
// It exercises the gateway's own validate interceptor chain.
type validateProbeGatewayService struct {
	probe *recordingValidateProbeServer
}

func (m *validateProbeGatewayService) Name() string     { return "validate-probe-builtin" }
func (m *validateProbeGatewayService) Endpoint() string { return "" }

func (m *validateProbeGatewayService) ServicesNames() []string {
	return []string{validateProbeServiceName}
}

func (m *validateProbeGatewayService) RegisterService(server *grpc.Server) {
	server.RegisterService(&validateProbeServiceDesc, m.probe)
}

// startValidateProbeGateway starts a gateway hosting the probe service
// through the given registration option, returning a client connection.
func startValidateProbeGateway(t *testing.T, serviceOption gateway.GatewayOption) *grpc.ClientConn {
	t.Helper()

	listener := NewTestListener(t)
	gw, err := gateway.NewGateway(gateway.DefaultConfig(), gateway.WithListener(listener), serviceOption)
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

	return conn
}

// Test_Gateway_RejectsInvalidProbeUnary verifies that an invalid unary
// request to a builtin service never reaches the handler.
func Test_Gateway_RejectsInvalidProbeUnary(t *testing.T) {
	t.Parallel()

	probe := &recordingValidateProbeServer{}
	conn := startValidateProbeGateway(t, gateway.WithBuiltinService(&validateProbeGatewayService{probe: probe}))

	invokeErr := conn.Invoke(t.Context(), "/"+validateProbeServiceName+"/Unary", &wrapperspb.StringValue{}, &emptypb.Empty{})

	statusErr, ok := status.FromError(invokeErr)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, statusErr.Code())
	require.Equal(t, "value is required", statusErr.Message())
	require.False(t, probe.unaryCalled.Load(), "handler must not run for an invalid probe request")
}

// Test_Gateway_RejectsInvalidProbeStream verifies that an invalid streamed
// request to a builtin service never reaches the handler.
func Test_Gateway_RejectsInvalidProbeStream(t *testing.T) {
	t.Parallel()

	probe := &recordingValidateProbeServer{}
	conn := startValidateProbeGateway(t, gateway.WithBuiltinService(&validateProbeGatewayService{probe: probe}))

	stream, err := conn.NewStream(t.Context(), &grpc.StreamDesc{StreamName: "Stream", ServerStreams: true}, "/"+validateProbeServiceName+"/Stream")
	require.NoError(t, err)
	require.NoError(t, stream.SendMsg(&wrapperspb.StringValue{}))
	require.NoError(t, stream.CloseSend())

	recvErr := stream.RecvMsg(&emptypb.Empty{})
	statusErr, ok := status.FromError(recvErr)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, statusErr.Code())
	require.Equal(t, "value is required", statusErr.Message())
	require.False(t, probe.streamCalled.Load(), "handler must not run for an invalid probe request")
}

// Test_Gateway_ProxiedRPC_RejectsInvalidProbe verifies that an invalid
// request proxied to an in-process module never reaches the handler.
//
// The module's own interceptor observes the rejection, so the request
// crossed the gateway untouched and the module runner rejected it.
func Test_Gateway_ProxiedRPC_RejectsInvalidProbe(t *testing.T) {
	t.Parallel()

	probe := &recordingValidateProbeServer{}
	module := &validateProbeModuleService{probe: probe, observedCodes: make(chan codes.Code, 1)}

	conn := startValidateProbeGateway(t, gateway.WithService(module))

	// The runner registers the module asynchronously, so the call is retried
	// while the service is still unknown.
	var invokeErr error
	require.Eventually(t, func() bool {
		invokeErr = conn.Invoke(t.Context(), "/"+validateProbeServiceName+"/Unary", &wrapperspb.StringValue{}, &emptypb.Empty{})
		return status.Code(invokeErr) != codes.NotFound
	}, 5*time.Second, 50*time.Millisecond, "probe request did not reach the module")

	statusErr, ok := status.FromError(invokeErr)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, statusErr.Code())
	require.Equal(t, "value is required", statusErr.Message())
	require.False(t, probe.unaryCalled.Load(), "handler must not run for an invalid probe request")

	select {
	case observed := <-module.observedCodes:
		require.Equal(t, codes.InvalidArgument, observed, "module interceptor must observe the rejected request")
	case <-time.After(5 * time.Second):
		t.Fatal("module interceptor did not observe the request")
	}
}

// Test_GatewayService_Register_RejectsInvalidRequest verifies that malformed
// registration requests receive InvalidArgument before reaching the handler.
func Test_GatewayService_Register_RejectsInvalidRequest(t *testing.T) {
	t.Parallel()

	conn := startValidateProbeGateway(t, gateway.WithBuiltinService(&validateProbeGatewayService{
		probe: &recordingValidateProbeServer{},
	}))
	client := ynpb.NewGatewayClient(conn)

	cases := []struct {
		name    string
		request *ynpb.RegisterRequest
		message string
	}{
		{
			name:    "missing backend",
			request: &ynpb.RegisterRequest{},
			message: "backend is required",
		},
		{
			name: "empty backend name",
			request: &ynpb.RegisterRequest{Backend: &ynpb.BackendDesc{
				Endpoint: "passthrough:test-endpoint",
			}},
			message: "backend: name is required",
		},
		{
			name: "empty backend endpoint",
			request: &ynpb.RegisterRequest{Backend: &ynpb.BackendDesc{
				Name: "svc.InvalidEndpoint",
			}},
			message: "backend: endpoint is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.Register(t.Context(), tc.request)
			statusErr, ok := status.FromError(err)
			require.True(t, ok)
			require.Equal(t, codes.InvalidArgument, statusErr.Code())
			require.Equal(t, tc.message, statusErr.Message())
		})
	}
}

// auditProbeServer returns a backend failure so proxy logs must retain its status.
type auditProbeServer struct{}

func (m *auditProbeServer) Probe(ctx context.Context, request *emptypb.Empty) (*emptypb.Empty, error) {
	return nil, status.Error(codes.FailedPrecondition, "operation rejected")
}

// newAuditSigner returns an ephemeral P-256 signer usable for SSH certificates.
func newAuditSigner(t *testing.T) ssh.Signer {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(key)
	require.NoError(t, err)
	return signer
}

// signAuditToken binds a certificate identity to one RPC and timestamp.
func signAuditToken(t *testing.T, authority, signer ssh.Signer, method string) string {
	t.Helper()
	certificate := &ssh.Certificate{
		Key:             signer.PublicKey(),
		CertType:        ssh.UserCert,
		ValidPrincipals: []string{"verified-operator"},
		ValidAfter:      uint64(time.Now().Add(-time.Hour).Unix()),
		ValidBefore:     uint64(time.Now().Add(time.Hour).Unix()),
	}
	require.NoError(t, certificate.SignCert(rand.Reader, authority))
	token := sshcert.Token{
		Version:     1,
		Certificate: base64.StdEncoding.EncodeToString(certificate.Marshal()),
		Timestamp:   time.Now().UnixNano(),
		Nonce:       "audit-test-nonce",
		Method:      method,
	}
	signature, err := signer.Sign(rand.Reader, token.CanonicalSignedData())
	require.NoError(t, err)
	token.Signature = base64.StdEncoding.EncodeToString(ssh.Marshal(signature))
	payload, err := json.Marshal(token)
	require.NoError(t, err)
	return "sshcert " + base64.StdEncoding.EncodeToString(payload)
}

// Test_Gateway_Audit_LogsVerifiedAndAnonymousRPCOutcomes verifies that audit logs
// record verified or anonymous identities with the original RPC outcome.
//
// Account stores are absent and both built-in and proxy requests are exercised.
func Test_Gateway_Audit_LogsVerifiedAndAnonymousRPCOutcomes(t *testing.T) {
	authority := newAuditSigner(t)
	signer := newAuditSigner(t)
	trustPath := writeTestFile(t, "ssh-ca.pub", string(ssh.MarshalAuthorizedKey(authority.PublicKey())))
	config := gateway.DefaultConfig()
	require.NoError(t, yaml.Unmarshal([]byte("mode: audit\nauthenticators:\n  - type: sshcert\n    config:\n      ca_sources: ["+trustPath+"]\n"), &config.Auth))
	logCore, logs := observer.New(zap.DebugLevel)
	listener := NewTestListener(t)
	gatewayServer, err := gateway.NewGateway(config, gateway.WithListener(listener), gateway.WithLog(zap.New(logCore)))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, gatewayServer.Close()) })
	ctx, cancel := context.WithCancel(t.Context())
	var group errgroup.Group
	group.Go(func() error { return gatewayServer.Run(ctx) })
	t.Cleanup(func() { cancel(); require.NoError(t, group.Wait()) })

	backendListener := NewTestListener(t)
	backend := grpc.NewServer()
	backend.RegisterService(&cancelProbeServiceDesc, &auditProbeServer{})
	go func() { _ = backend.Serve(backendListener) }()
	t.Cleanup(backend.Stop)
	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	client := ynpb.NewGatewayClient(connection)
	_, err = client.Register(t.Context(), &ynpb.RegisterRequest{Backend: &ynpb.BackendDesc{Name: cancelProbeServiceName, Endpoint: backendListener.Addr().String()}}, grpc.WaitForReady(true))
	require.NoError(t, err)

	for _, tc := range []struct {
		name     string
		kind     string
		verified bool
	}{
		{name: "signed trusted certificate", kind: "valid", verified: true},
		{name: "missing credential", kind: "missing"},
		{name: "unsupported credential", kind: "unsupported"},
		{name: "malformed credential", kind: "malformed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, method := range []string{ynpb.Gateway_ListServices_FullMethodName, cancelProbeFullMethod} {
				token := signAuditToken(t, authority, signer, method)
				switch tc.kind {
				case "missing":
					token = ""
				case "unsupported":
					token = "unsupported secret-credential"
				case "malformed":
					token = "sshcert !!!secret-credential"
				}
				callContext := metadata.NewOutgoingContext(t.Context(), metadata.Pairs("x-yanet-authentication", token))
				logs.TakeAll()
				if method == cancelProbeFullMethod {
					err = connection.Invoke(callContext, method, &emptypb.Empty{}, &emptypb.Empty{})
					require.Equal(t, codes.FailedPrecondition, status.Code(err))
				} else {
					_, err = client.ListServices(callContext, &ynpb.ListServicesRequest{})
					require.NoError(t, err)
				}
				require.Eventually(t, func() bool {
					return logs.FilterField(zap.String("method", method)).FilterMessageSnippet("gRPC").FilterFieldKey("status").Len() == 1
				}, time.Second, time.Millisecond)
				entries := logs.FilterField(zap.String("method", method)).FilterFieldKey("status").All()
				require.Len(t, entries, 1)
				fields := entries[0].ContextMap()
				user, authMethod := "anonymous", "none"
				if tc.verified {
					user, authMethod = "verified-operator", "sshcert"
				}
				require.Equal(t, user, fields["user"])
				require.Equal(t, authMethod, fields["auth_method"])
				require.Equal(t, !tc.verified, fields["is_anonymous"])
				require.Contains(t, fields, "duration")
				require.Equal(t, status.Code(err).String(), fields["status"])
				for _, entry := range logs.All() {
					rendered := fmt.Sprint(entry.ContextMap())
					if token != "" {
						require.NotContains(t, rendered, token)
					}
					require.NotContains(t, rendered, "secret-credential")
				}
			}
		})
	}

	malformedCertificate := sshcert.Token{
		Version:     1,
		Certificate: base64.StdEncoding.EncodeToString(ssh.Marshal(struct{ Algorithm string }{"introspection-secret"})),
		Timestamp:   time.Now().UnixNano(),
		Nonce:       "audit-test-nonce",
		Method:      ynpb.AuthService_IntrospectToken_FullMethodName,
		Signature:   "unused",
	}
	malformedPayload, err := json.Marshal(malformedCertificate)
	require.NoError(t, err)
	authClient := ynpb.NewAuthServiceClient(connection)
	for _, tc := range []struct {
		token        string
		parsingError bool
	}{
		{token: "sshcert !!!introspection-secret"},
		{token: "unsupported introspection-secret"},
		{token: "sshcert " + base64.StdEncoding.EncodeToString(malformedPayload), parsingError: true},
	} {
		logs.TakeAll()
		_, err := authClient.IntrospectToken(t.Context(), &ynpb.IntrospectTokenRequest{Token: tc.token})
		require.Equal(t, codes.Unauthenticated, status.Code(err))
		if tc.parsingError {
			require.ErrorContains(t, err, "ssh: unknown key algorithm: introspection-secret")
		}
		for _, entry := range logs.All() {
			require.NotContains(t, fmt.Sprint(entry.ContextMap()), "introspection-secret")
		}
	}
}
