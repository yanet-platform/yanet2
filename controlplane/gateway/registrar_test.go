package gateway_test

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/gateway"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// stubGatewayServer is a minimal ynpb.GatewayServer that counts Register
// calls and rejects a configured number of leading calls with a configured
// status before succeeding.
//
// A negative failures rejects every call, which is what the permanent-error
// test needs; a non-negative value rejects exactly that many leading calls
// and then succeeds, letting the transient-error test observe a successful
// retry without depending on wall-clock timing.
type stubGatewayServer struct {
	ynpb.UnimplementedGatewayServer

	code     codes.Code
	failures int64
	calls    atomic.Int64
}

func (m *stubGatewayServer) Register(
	_ context.Context,
	_ *ynpb.RegisterRequest,
) (*ynpb.RegisterResponse, error) {
	call := m.calls.Add(1)
	if m.failures < 0 || call <= m.failures {
		return nil, status.Error(m.code, "stub rejection")
	}
	return &ynpb.RegisterResponse{
		Status: ynpb.RegistrationStatus_REGISTRATION_STATUS_REGISTERED,
	}, nil
}

// startGRPCStub starts server on a real 127.0.0.1 listener and returns its
// endpoint.
func startGRPCStub(t *testing.T, server ynpb.GatewayServer) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	grpcServer := grpc.NewServer()
	ynpb.RegisterGatewayServer(grpcServer, server)

	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	return listener.Addr().String()
}

// startStubGateway starts a stubGatewayServer on a real 127.0.0.1 listener
// and returns its endpoint alongside the server for call-count inspection.
//
// The stub rejects the first failures calls with code, then succeeds; pass a
// negative failures to reject every call.
func startStubGateway(t *testing.T, code codes.Code, failures int64) (string, *stubGatewayServer) {
	t.Helper()

	stub := &stubGatewayServer{code: code, failures: failures}
	return startGRPCStub(t, stub), stub
}

// cancelingStubGatewayServer is a stub whose first Register call cancels the
// caller-supplied context before returning a transient rejection, isolating
// the retry loop's own cancellation check from the permanent-error
// classifier.
type cancelingStubGatewayServer struct {
	ynpb.UnimplementedGatewayServer

	cancel context.CancelFunc
	calls  atomic.Int64
}

func (m *cancelingStubGatewayServer) Register(
	_ context.Context,
	_ *ynpb.RegisterRequest,
) (*ynpb.RegisterResponse, error) {
	m.calls.Add(1)
	m.cancel()
	return nil, status.Error(codes.Unavailable, "stub rejection")
}

// constantBackOffFor10ms returns a fresh 10ms constant backoff, matching the
// factory shape RegisterServices requires per attempt.
func constantBackOffFor10ms() backoff.BackOff {
	return backoff.NewConstantBackOff(10 * time.Millisecond)
}

// TestGatewayRegistrar_PermanentErrorFailsFast verifies that a Register
// rejection classified as permanent aborts RegisterServices after exactly
// one attempt, and that the returned error still carries the original gRPC
// status through the wrap.
func TestGatewayRegistrar_PermanentErrorFailsFast(t *testing.T) {
	t.Parallel()

	for _, code := range []codes.Code{
		codes.InvalidArgument,
		codes.Unauthenticated,
		codes.PermissionDenied,
	} {
		t.Run(code.String(), func(t *testing.T) {
			t.Parallel()

			endpoint, stub := startStubGateway(t, code, -1)

			registrar, err := gateway.NewGatewayRegistrar(endpoint, nil,
				gateway.WithBackOff(constantBackOffFor10ms),
				gateway.WithMaxElapsedTime(5*time.Second),
			)
			require.NoError(t, err)
			t.Cleanup(func() { _ = registrar.Close() })

			registerErr := registrar.RegisterServices(t.Context(), []string{"test.Service"}, "127.0.0.1:1")
			require.Error(t, registerErr)
			assert.Equal(t, int64(1), stub.calls.Load(),
				"a permanent rejection must not be retried")
			assert.Equal(t, code, status.Code(registerErr),
				"wrapping the permanent error must preserve its gRPC status code")
		})
	}
}

// TestGatewayRegistrar_TransientErrorIsRetried is the positive control for
// TestGatewayRegistrar_PermanentErrorFailsFast: it proves codes outside the
// permanent set are retried until the underlying RPC succeeds, instead of
// failing fast, which is what breaks if the classifier is ever widened to
// treat everything as permanent. The stub fails only the first call, so the
// assertion holds deterministically without depending on wall-clock timing.
func TestGatewayRegistrar_TransientErrorIsRetried(t *testing.T) {
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

			endpoint, stub := startStubGateway(t, code, 1)

			registrar, err := gateway.NewGatewayRegistrar(endpoint, nil,
				gateway.WithBackOff(constantBackOffFor10ms),
				gateway.WithMaxElapsedTime(5*time.Second),
			)
			require.NoError(t, err)
			t.Cleanup(func() { _ = registrar.Close() })

			registerErr := registrar.RegisterServices(t.Context(), []string{"test.Service"}, "127.0.0.1:1")
			require.NoError(t, registerErr)
			assert.Equal(t, int64(2), stub.calls.Load(),
				"a transient rejection must be retried exactly once before succeeding")
		})
	}
}

// TestGatewayRegistrar_CanceledContextIsNotPermanent verifies that a Register
// call cut short by context cancellation surfaces as a plain cancellation
// error, not as a permanent-rejection error. This guards against two
// regressions: adding Canceled to the permanent-code set, and moving the
// permanent-error classification ahead of the retry loop's own cancellation
// check.
func TestGatewayRegistrar_CanceledContextIsNotPermanent(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	stub := &cancelingStubGatewayServer{cancel: cancel}
	endpoint := startGRPCStub(t, stub)

	registrar, err := gateway.NewGatewayRegistrar(endpoint, nil,
		gateway.WithBackOff(constantBackOffFor10ms),
		gateway.WithMaxElapsedTime(5*time.Second),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = registrar.Close() })

	registerErr := registrar.RegisterServices(ctx, []string{"test.Service"}, "127.0.0.1:1")
	require.Error(t, registerErr)
	assert.ErrorIs(t, registerErr, context.Canceled)
	assert.Equal(t, int64(1), stub.calls.Load(),
		"the retry loop must stop on cancellation instead of retrying")
}
