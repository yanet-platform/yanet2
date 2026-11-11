package operator_test

import (
	"context"
	"crypto/tls"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/testutils/tlscert"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/common/go/xgrpc"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// recordingGateway is a fake gateway reporting the endpoints it is sent.
type recordingGateway struct {
	ynpb.UnimplementedGatewayServer

	endpoints chan string
}

func newRecordingGateway() *recordingGateway {
	return &recordingGateway{
		endpoints: make(chan string, 16),
	}
}

func (m *recordingGateway) Register(
	_ context.Context,
	request *ynpb.RegisterRequest,
) (*ynpb.RegisterResponse, error) {
	select {
	case m.endpoints <- request.GetBackend().GetEndpoint():
	default:
	}

	return &ynpb.RegisterResponse{}, nil
}

// rejectingGateway is a fake gateway counting registration requests and
// rejecting the leading ones with a configured status.
//
// A negative failure count rejects every request. A non-negative one rejects
// exactly that many leading requests and then accepts.
type rejectingGateway struct {
	ynpb.UnimplementedGatewayServer

	code     codes.Code
	failures int64
	calls    atomic.Int64
}

func (m *rejectingGateway) Register(
	_ context.Context,
	_ *ynpb.RegisterRequest,
) (*ynpb.RegisterResponse, error) {
	call := m.calls.Add(1)
	if m.failures < 0 || call <= m.failures {
		return nil, status.Error(m.code, "fake rejection")
	}

	return &ynpb.RegisterResponse{
		Status: ynpb.RegistrationStatus_REGISTRATION_STATUS_REGISTERED,
	}, nil
}

// hungGateway is a fake gateway counting registration requests and answering
// none of them before the caller gives up.
type hungGateway struct {
	ynpb.UnimplementedGatewayServer

	calls atomic.Int64
}

func (m *hungGateway) Register(
	ctx context.Context,
	_ *ynpb.RegisterRequest,
) (*ynpb.RegisterResponse, error) {
	m.calls.Add(1)
	<-ctx.Done()
	return nil, ctx.Err()
}

// serveGateway serves a fake gateway on a loopback port until the test ends
// and returns the endpoint to dial.
func serveGateway(t *testing.T, service ynpb.GatewayServer, options ...grpc.ServerOption) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := grpc.NewServer(options...)
	ynpb.RegisterGatewayServer(server, service)

	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(server.Stop)

	return listener.Addr().String()
}

// startRegistration keeps one service registered in the gateway at the given
// endpoint, renewing it at the given interval, until the test ends.
func startRegistration(t *testing.T, endpoint string, interval time.Duration) {
	t.Helper()

	runner := operator.NewGatewayRegRunner(
		[]operator.GatewayConfig{{
			Name:     "numa0",
			Endpoint: xcfg.MustNonEmptyString(endpoint),
		}},
		[]string{"fake.Service"},
		"127.0.0.1:1",
		operator.WithGatewayRegInterval(interval),
	)

	ctx, cancel := context.WithCancel(t.Context())
	var wg errgroup.Group
	wg.Go(func() error {
		return runner.Run(ctx)
	})
	t.Cleanup(func() {
		cancel()
		_ = wg.Wait()
	})
}

// awaitEndpoint returns the endpoint of the next registration, failing on
// timeout.
func awaitEndpoint(t *testing.T, service *recordingGateway) string {
	t.Helper()

	select {
	case endpoint := <-service.endpoints:
		return endpoint
	case <-time.After(10 * time.Second):
		t.Fatal("gateway accepted no registration")
		return ""
	}
}

// Test_GatewayRegRunner_Run_RegistersGivenEndpointVerbatim verifies that the
// advertised endpoint reaches the gateway as written, its name unresolved.
func Test_GatewayRegRunner_Run_RegistersGivenEndpointVerbatim(t *testing.T) {
	t.Parallel()

	const advertised = "route-operator.yanet.svc.cluster.local:8080"

	service := newRecordingGateway()
	gatewayEndpoint := serveGateway(t, service)

	runner := operator.NewGatewayRegRunner(
		[]operator.GatewayConfig{{
			Name:     "numa0",
			Endpoint: xcfg.MustNonEmptyString(gatewayEndpoint),
		}},
		[]string{"fake.Service"},
		advertised,
		operator.WithGatewayRegInterval(time.Second),
		operator.WithGatewayRegLog(zap.NewNop()),
	)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() {
		_ = runner.Run(ctx)
	}()

	require.Equal(t, advertised, awaitEndpoint(t, service))
}

// Test_GatewayRegRunner_Run_RegistersOverTLS verifies that a gateway with a
// tls block is reached over TLS with the configured CA.
func Test_GatewayRegRunner_Run_RegistersOverTLS(t *testing.T) {
	t.Parallel()

	ca := tlscert.NewCA(t)
	server := ca.IssueServer(t, "127.0.0.1")

	service := newRecordingGateway()
	gatewayEndpoint := serveGateway(t, service, grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{server.Certificate},
		MinVersion:   tls.VersionTLS12,
	})))

	runner := operator.NewGatewayRegRunner(
		[]operator.GatewayConfig{{
			Name:     "numa0",
			Endpoint: xcfg.MustNonEmptyString(gatewayEndpoint),
			TLS:      &xgrpc.ClientTLSConfig{CAFile: ca.BundleFile()},
		}},
		[]string{"fake.Service"},
		"route-operator.yanet:8080",
		operator.WithGatewayRegInterval(time.Second),
		operator.WithGatewayRegLog(zap.NewNop()),
	)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() {
		_ = runner.Run(ctx)
	}()

	require.Equal(t, "route-operator.yanet:8080", awaitEndpoint(t, service))
}

// Test_GatewayRegRunner_Run_AbandonsPermanentRejection verifies that a
// registration the gateway rejects for good is not retried in the same cycle.
func Test_GatewayRegRunner_Run_AbandonsPermanentRejection(t *testing.T) {
	t.Parallel()

	for _, code := range []codes.Code{
		codes.InvalidArgument,
		codes.Unauthenticated,
		codes.PermissionDenied,
	} {
		t.Run(code.String(), func(t *testing.T) {
			t.Parallel()

			service := &rejectingGateway{code: code, failures: -1}
			startRegistration(t, serveGateway(t, service), time.Hour)

			require.Eventually(t, func() bool {
				return service.calls.Load() == 1
			}, 10*time.Second, 10*time.Millisecond, "the gateway received no registration")
			// A retry would come within 750ms: the default backoff
			// starts at 500ms with 50% jitter.
			require.Never(t, func() bool {
				return service.calls.Load() > 1
			}, time.Second, 10*time.Millisecond, "a permanent rejection must not be retried")
		})
	}
}

// Test_GatewayRegRunner_Run_RetriesTransientRejection verifies that a
// transiently rejected registration is retried until it succeeds.
func Test_GatewayRegRunner_Run_RetriesTransientRejection(t *testing.T) {
	t.Parallel()

	for _, code := range []codes.Code{
		codes.Unavailable,
		codes.DeadlineExceeded,
		codes.Internal,
		codes.Unimplemented,
		codes.FailedPrecondition,
	} {
		t.Run(code.String(), func(t *testing.T) {
			t.Parallel()

			service := &rejectingGateway{code: code, failures: 1}
			startRegistration(t, serveGateway(t, service), time.Hour)

			require.Eventually(t, func() bool {
				return service.calls.Load() == 2
			}, 10*time.Second, 10*time.Millisecond, "a transient rejection must be retried once before succeeding")
		})
	}
}

// Test_GatewayRegRunner_Run_CutsOffUnansweredRegistration verifies that a
// registration the gateway never answers does not stall the next cycle.
func Test_GatewayRegRunner_Run_CutsOffUnansweredRegistration(t *testing.T) {
	t.Parallel()

	service := &hungGateway{}
	startRegistration(t, serveGateway(t, service), 200*time.Millisecond)

	require.Eventually(t, func() bool {
		return service.calls.Load() >= 2
	}, 10*time.Second, 10*time.Millisecond, "an unanswered registration must not stall the heartbeat")
}
