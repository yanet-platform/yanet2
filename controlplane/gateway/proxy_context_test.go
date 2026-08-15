package gateway_test

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
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/yanet-platform/yanet2/controlplane/gateway"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// cancelProbeServiceName is the arbitrary, non-compiled service name the
// fake module backend registers under.
const cancelProbeServiceName = "yanet.test.CancelProbe"

// cancelProbeFullMethod is the full method name a client dials to reach the
// fake module backend's Probe handler.
const cancelProbeFullMethod = "/" + cancelProbeServiceName + "/Probe"

// ctxObservation is what a cancelProbeServer handler saw once the request
// context it was given terminated.
type ctxObservation struct {
	err         error
	deadline    time.Time
	hasDeadline bool
}

// cancelProbeServer is the interface the registered handler is dispatched
// through.
type cancelProbeServer interface {
	Probe(ctx context.Context, in *emptypb.Empty) (*emptypb.Empty, error)
}

// probeServer blocks its Probe handler on the request context, signaling
// entry and then publishing the context's terminal state.
type probeServer struct {
	entered chan struct{}
	results chan ctxObservation
}

func (m *probeServer) Probe(ctx context.Context, _ *emptypb.Empty) (*emptypb.Empty, error) {
	deadline, hasDeadline := ctx.Deadline()
	m.entered <- struct{}{}
	<-ctx.Done()
	m.results <- ctxObservation{err: ctx.Err(), deadline: deadline, hasDeadline: hasDeadline}
	return nil, ctx.Err()
}

// cancelProbeServiceDesc registers probeServer under an arbitrary service
// name the gateway never compiled against, exercising the
// UnknownServiceHandler proxy path rather than a statically routed one.
var cancelProbeServiceDesc = grpc.ServiceDesc{
	ServiceName: cancelProbeServiceName,
	HandlerType: (*cancelProbeServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Probe",
			Handler: func(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
				in := new(emptypb.Empty)
				if err := dec(in); err != nil {
					return nil, err
				}
				return srv.(cancelProbeServer).Probe(ctx, in)
			},
		},
	},
	Metadata: "cancel_probe_test",
}

// newCancelProbeServer starts a gRPC server hosting cancelProbeServiceDesc
// on a real TCP listener, returning its address plus the probe's entry and
// result channels.
func newCancelProbeServer(t *testing.T) (addr string, entered chan struct{}, results chan ctxObservation) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	entered = make(chan struct{}, 1)
	results = make(chan ctxObservation, 1)

	server := grpc.NewServer()
	server.RegisterService(&cancelProbeServiceDesc, &probeServer{entered: entered, results: results})

	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	return listener.Addr().String(), entered, results
}

// startCancelProbeGateway starts a gateway and registers a fake module
// backend under cancelProbeServiceName, returning the gateway's address
// plus the probe's entry and result channels.
func startCancelProbeGateway(t *testing.T) (gatewayAddr string, entered chan struct{}, results chan ctxObservation) {
	t.Helper()

	cfg := gateway.DefaultConfig()
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

	// The newCancelProbeServer helper registers t.Cleanup(server.Stop) here,
	// after the run-group cleanup above. LIFO then stops the probe server before
	// waiting on the group, so a handler still parked on its context
	// unblocks instead of deadlocking the run-group wait.
	backendAddr, entered, results := newCancelProbeServer(t)

	registerConn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = registerConn.Close() })

	registerClient := ynpb.NewGatewayClient(registerConn)
	// A fresh gRPC server is not immediately reachable after Run starts, so
	// retry past a transient dial failure instead of requiring a fixed
	// sleep.
	require.Eventually(t, func() bool {
		_, regErr := registerClient.Register(t.Context(), &ynpb.RegisterRequest{
			Backend: &ynpb.BackendDesc{Name: cancelProbeServiceName, Endpoint: backendAddr},
		})
		return regErr == nil
	}, 5*time.Second, 50*time.Millisecond, "failed to register the fake module backend with the gateway")

	return listener.Addr().String(), entered, results
}

// TestGateway_ProxiedRPC_ClientCancelPropagatesToBackendCtx verifies that
// cancelling a proxied gRPC call's context cancels the backend handler's
// context, and that the handler's context is not already dead beforehand.
func TestGateway_ProxiedRPC_ClientCancelPropagatesToBackendCtx(t *testing.T) {
	t.Parallel()

	gatewayAddr, entered, results := startCancelProbeGateway(t)

	conn, err := grpc.NewClient(gatewayAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() {
		_ = conn.Invoke(ctx, cancelProbeFullMethod, &emptypb.Empty{}, &emptypb.Empty{})
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("backend handler was never entered")
	}

	select {
	case observation := <-results:
		t.Fatalf("backend handler observed termination before the client canceled: %v", observation.err)
	case <-time.After(250 * time.Millisecond):
	}

	cancel()

	select {
	case observation := <-results:
		require.ErrorIs(t, observation.err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("backend handler did not observe the client cancellation")
	}
}

// TestGateway_ProxiedRPC_ClientDisconnectPropagatesToBackendCtx verifies
// that closing the client connection mid-call cancels the backend
// handler's context, exercising transport teardown rather than a client
// cancel frame.
func TestGateway_ProxiedRPC_ClientDisconnectPropagatesToBackendCtx(t *testing.T) {
	t.Parallel()

	gatewayAddr, entered, results := startCancelProbeGateway(t)

	conn, err := grpc.NewClient(gatewayAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	go func() {
		_ = conn.Invoke(t.Context(), cancelProbeFullMethod, &emptypb.Empty{}, &emptypb.Empty{})
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("backend handler was never entered")
	}

	select {
	case observation := <-results:
		t.Fatalf("backend handler observed termination before the client disconnected: %v", observation.err)
	case <-time.After(250 * time.Millisecond):
	}

	require.NoError(t, conn.Close())

	select {
	case observation := <-results:
		require.ErrorIs(t, observation.err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("backend handler did not observe the dropped connection")
	}
}

// TestGateway_ProxiedRPC_ClientDeadlinePropagatesToBackendCtx verifies that
// a client-set deadline is genuinely derived into the backend handler's
// context, landing close to the deadline the client actually set rather
// than merely being cancelled once the call ends.
func TestGateway_ProxiedRPC_ClientDeadlinePropagatesToBackendCtx(t *testing.T) {
	t.Parallel()

	gatewayAddr, entered, results := startCancelProbeGateway(t)

	conn, err := grpc.NewClient(gatewayAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	const clientTimeout = 2 * time.Second
	ctx, cancel := context.WithTimeout(t.Context(), clientTimeout)
	defer cancel()

	clientDeadline, ok := ctx.Deadline()
	require.True(t, ok)

	go func() {
		_ = conn.Invoke(ctx, cancelProbeFullMethod, &emptypb.Empty{}, &emptypb.Empty{})
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("backend handler was never entered")
	}

	select {
	case observation := <-results:
		// The observation.err field is not asserted: each hop recomputes and rounds
		// the deadline, so the client's timer can win the teardown race. The exact
		// terminal error is intentionally not part of this test.
		require.True(t, observation.hasDeadline, "backend handler ctx must carry a deadline")
		require.WithinDuration(t, clientDeadline, observation.deadline, 50*time.Millisecond)
	case <-time.After(5 * time.Second):
		t.Fatal("backend handler did not observe the deadline expiring")
	}
}
