package httpproxy_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

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

// workersProbeServer blocks CountersService.Workers on the request context,
// signaling entry and then publishing the context's terminal state.
type workersProbeServer struct {
	ynpb.UnimplementedCountersServiceServer
	entered chan struct{}
	results chan ctxObservation
}

func (m *workersProbeServer) Workers(ctx context.Context, _ *ynpb.WorkerCountersRequest) (*ynpb.WorkerCountersResponse, error) {
	m.entered <- struct{}{}
	<-ctx.Done()
	m.results <- ctxObservation{err: ctx.Err()}
	return nil, ctx.Err()
}

// watchProbeServer blocks ReadinessService.Watch on the stream context,
// signaling entry and then publishing the context's terminal state.
type watchProbeServer struct {
	ynpb.UnimplementedReadinessServiceServer
	entered     chan struct{}
	results     chan ctxObservation
	sendInitial bool
}

func (m *watchProbeServer) Watch(request *readinesspb.ReadyRequest, stream ynpb.ReadinessService_WatchServer) error {
	if m.sendInitial {
		if err := stream.Send(&readinesspb.ReadyResponse{}); err != nil {
			return err
		}
	}
	ctx := stream.Context()
	if m.entered != nil {
		m.entered <- struct{}{}
	}
	<-ctx.Done()
	m.results <- ctxObservation{err: ctx.Err()}
	return ctx.Err()
}

// passthroughBackend is a Backend that hands the given ctx straight
// through to a real gRPC connection, standing in for the ctx-derivation the
// gateway's own backend performs.
type passthroughBackend struct {
	conn *grpc.ClientConn
}

func (m passthroughBackend) GetConnection(ctx context.Context, _ string) (context.Context, *grpc.ClientConn, error) {
	return ctx, m.conn, nil
}

// TestServeHTTP_ClientAbortPropagatesToBackendCtx verifies that aborting an
// in-flight HTTP request cancels the backend gRPC handler's context.
func TestServeHTTP_ClientAbortPropagatesToBackendCtx(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	entered := make(chan struct{}, 1)
	results := make(chan ctxObservation, 1)

	grpcServer := grpc.NewServer()
	ynpb.RegisterCountersServiceServer(grpcServer, &workersProbeServer{entered: entered, results: results})
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	handler := httpproxy.NewHTTPHandler(passthroughBackend{conn: conn}, everyServiceRegistry{}, admitAll)
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
		server.URL+"/api"+ynpb.CountersService_Workers_FullMethodName,
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

	handler := httpproxy.NewHTTPHandler(passthroughBackend{conn: conn}, everyServiceRegistry{}, admitAll)
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

// blockedResponse reports a write timeout only after a deadline is set.
type blockedResponse struct {
	*httptest.ResponseRecorder
	deadline time.Time
	timedOut bool
}

func (m *blockedResponse) SetWriteDeadline(deadline time.Time) error {
	m.deadline = deadline
	return nil
}

func (m *blockedResponse) FlushError() error {
	if m.deadline.IsZero() {
		return http.ErrNotSupported
	}
	m.timedOut = true
	return os.ErrDeadlineExceeded
}

// Test_HTTPHandler_StreamWriteTimeoutCancelsBackend verifies that an SSE
// timeout cancels the upstream RPC even through a gzip response wrapper.
func Test_HTTPHandler_StreamWriteTimeoutCancelsBackend(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	results := make(chan ctxObservation, 1)
	ynpb.RegisterReadinessServiceServer(server, &watchProbeServer{results: results, sendInitial: true})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	handler := httpproxy.GzipMiddleware(httpproxy.NewHTTPHandler(
		passthroughBackend{conn: conn}, everyServiceRegistry{}, admitAll,
		httpproxy.WithStreamWriteTimeout(20*time.Millisecond),
	))
	for _, encoding := range []string{"identity", "gzip"} {
		t.Run(encoding, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			method := "/api" + ynpb.ReadinessService_Watch_FullMethodName
			request := httptest.NewRequestWithContext(ctx, http.MethodPost, method, strings.NewReader("{}"))
			request.Header.Set("Accept-Encoding", encoding)
			response := &blockedResponse{ResponseRecorder: httptest.NewRecorder()}
			handler.ServeHTTP(response, request)
			require.True(t, response.timedOut, "the stalled flush did not reach a write deadline")
			select {
			case observation := <-results:
				require.NoError(t, ctx.Err())
				require.ErrorIs(t, observation.err, context.Canceled)
			case <-ctx.Done():
				t.Fatal("failed HTTP write did not cancel the loopback call")
			}
		})
	}
}
