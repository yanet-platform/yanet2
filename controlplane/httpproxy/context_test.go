package httpproxy_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/siderolabs/grpc-proxy/proxy"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
	"github.com/yanet-platform/yanet2/controlplane/httpproxy"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// ctxObservation is what a probe handler saw once the context it was given
// terminated.
type ctxObservation struct {
	err error
}

// perfProbeServer blocks CountersService.Perf on the request context,
// signaling entry and then publishing the context's terminal state.
type perfProbeServer struct {
	ynpb.UnimplementedCountersServiceServer
	entered chan struct{}
	results chan ctxObservation
}

func (m *perfProbeServer) Perf(ctx context.Context, _ *ynpb.PerfCountersRequest) (*ynpb.PerfCountersResponse, error) {
	m.entered <- struct{}{}
	<-ctx.Done()
	m.results <- ctxObservation{err: ctx.Err()}
	return nil, ctx.Err()
}

// watchProbeServer blocks ReadinessService.Watch on the stream context,
// signaling entry and then publishing the context's terminal state.
type watchProbeServer struct {
	ynpb.UnimplementedReadinessServiceServer
	entered chan struct{}
	results chan ctxObservation
}

func (m *watchProbeServer) Watch(_ *readinesspb.ReadyRequest, stream ynpb.ReadinessService_WatchServer) error {
	ctx := stream.Context()
	m.entered <- struct{}{}
	<-ctx.Done()
	m.results <- ctxObservation{err: ctx.Err()}
	return ctx.Err()
}

// passthroughBackend is a proxy.Backend that hands the given ctx straight
// through to a real gRPC connection, standing in for the ctx-derivation the
// gateway's own backend performs.
type passthroughBackend struct {
	conn *grpc.ClientConn
}

func (m passthroughBackend) String() string { return "passthrough-backend" }

func (m passthroughBackend) GetConnection(ctx context.Context, _ string) (context.Context, *grpc.ClientConn, error) {
	return ctx, m.conn, nil
}

func (m passthroughBackend) AppendInfo(_ bool, resp []byte) ([]byte, error) {
	return resp, nil
}

func (m passthroughBackend) BuildError(_ bool, _ error) ([]byte, error) {
	return nil, nil
}

// singleBackendRegistry is a BackendRegistry that always resolves to the
// same backend, regardless of the requested service.
type singleBackendRegistry struct {
	backend proxy.Backend
}

func (m singleBackendRegistry) GetBackend(_ string) (proxy.Backend, func(), bool) {
	return m.backend, func() {}, true
}

func (m singleBackendRegistry) HasBackend(_ string) bool { return true }

// TestServeHTTP_ClientAbortPropagatesToBackendCtx verifies that aborting an
// in-flight HTTP request cancels the backend gRPC handler's context.
func TestServeHTTP_ClientAbortPropagatesToBackendCtx(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	entered := make(chan struct{}, 1)
	results := make(chan ctxObservation, 1)

	grpcServer := grpc.NewServer()
	ynpb.RegisterCountersServiceServer(grpcServer, &perfProbeServer{entered: entered, results: results})
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	handler := httpproxy.NewHTTPHandler(singleBackendRegistry{backend: passthroughBackend{conn: conn}})
	server := httptest.NewServer(handler)
	// The conn.Close call must run before server.Close: server.Close drains the
	// in-flight HTTP request, which stays outstanding until closing conn
	// unblocks the handler goroutine parked in conn.Invoke. Registering
	// conn's cleanup after server.Close's here puts it first under LIFO.
	t.Cleanup(server.Close)
	t.Cleanup(func() { _ = conn.Close() })

	reqCtx, cancel := context.WithCancel(t.Context())
	defer cancel()

	req, err := http.NewRequestWithContext(
		reqCtx, http.MethodPost,
		server.URL+"/api"+ynpb.CountersService_Perf_FullMethodName,
		strings.NewReader("{}"),
	)
	require.NoError(t, err)

	go func() {
		resp, doErr := server.Client().Do(req)
		if doErr == nil {
			_ = resp.Body.Close()
		}
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("backend handler was never entered")
	}

	select {
	case observation := <-results:
		t.Fatalf("backend handler observed termination before the client aborted: %v", observation.err)
	case <-time.After(250 * time.Millisecond):
	}

	cancel()

	select {
	case observation := <-results:
		require.ErrorIs(t, observation.err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("backend handler did not observe the aborted HTTP request")
	}
}

// TestServeHTTP_ClientAbortPropagatesToBackendCtx_Streaming verifies that
// aborting an in-flight server-streaming HTTP request cancels the backend
// gRPC handler's context, covering handleServerStreaming's own r.Context()
// use alongside the unary path above. ReadinessService.Watch is the
// server-streaming RPC already used as the streaming fixture elsewhere in
// this package.
func TestServeHTTP_ClientAbortPropagatesToBackendCtx_Streaming(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	entered := make(chan struct{}, 1)
	results := make(chan ctxObservation, 1)

	grpcServer := grpc.NewServer()
	ynpb.RegisterReadinessServiceServer(grpcServer, &watchProbeServer{entered: entered, results: results})
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	handler := httpproxy.NewHTTPHandler(singleBackendRegistry{backend: passthroughBackend{conn: conn}})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	t.Cleanup(func() { _ = conn.Close() })

	reqCtx, cancel := context.WithCancel(t.Context())
	defer cancel()

	req, err := http.NewRequestWithContext(
		reqCtx, http.MethodPost,
		server.URL+"/api"+ynpb.ReadinessService_Watch_FullMethodName,
		strings.NewReader("{}"),
	)
	require.NoError(t, err)

	go func() {
		resp, doErr := server.Client().Do(req)
		if doErr == nil {
			_ = resp.Body.Close()
		}
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("backend handler was never entered")
	}

	select {
	case observation := <-results:
		t.Fatalf("backend handler observed termination before the client aborted: %v", observation.err)
	case <-time.After(250 * time.Millisecond):
	}

	cancel()

	select {
	case observation := <-results:
		require.ErrorIs(t, observation.err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("backend handler did not observe the aborted HTTP request")
	}
}
