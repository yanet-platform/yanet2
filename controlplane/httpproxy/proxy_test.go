package httpproxy_test

import (
	"context"
	"errors"
	"io"
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
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
	"github.com/yanet-platform/yanet2/controlplane/httpproxy"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// emptyRegistry is a BackendRegistry that never has a matching backend,
// simulating a service that is missing from the gateway's registry.
type emptyRegistry struct{}

func (m emptyRegistry) GetBackend(_ string) (proxy.Backend, func(), bool) {
	return nil, func() {}, false
}

func (m emptyRegistry) HasBackend(_ string) bool {
	return false
}

// TestServeHTTP_UnknownService verifies that a registry miss is answered
// with 503, not 404, on both the unary and server-streaming request paths.
func TestServeHTTP_UnknownService(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		path string
		// rationale documents why this case exercises the registry-miss
		// branch rather than a genuine NotFound from a backend.
		rationale string
	}{
		{
			name:      "unary",
			path:      "/api/some.unknown.Service/Method",
			rationale: "the service name matches nothing in the registry or the proto registry",
		},
		{
			name:      "streaming",
			path:      "/api" + ynpb.ReadinessService_Watch_FullMethodName,
			rationale: "the method resolves in the proto registry but the backend is still missing",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			handler := httpproxy.NewHTTPHandler(emptyRegistry{})

			req := httptest.NewRequest(http.MethodPost, testCase.path, strings.NewReader("{}"))
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, req)

			require.Equal(t, http.StatusServiceUnavailable, recorder.Code, testCase.rationale)
		})
	}
}

// explodingBody is an io.Reader that fails the test if it is ever read.
//
// It stands in for a request body in the unregistered-service tests, where
// the handler must reject the request before reading it.
type explodingBody struct {
	t *testing.T
}

func (m explodingBody) Read(_ []byte) (int, error) {
	m.t.Fatal("request body was read for an unregistered service")
	return 0, nil
}

// TestServeHTTP_UnknownService_DoesNotReadBody verifies that a registry miss
// is answered without the handler ever consuming the request body, on both
// the unary and server-streaming request paths.
func TestServeHTTP_UnknownService_DoesNotReadBody(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		path string
	}{
		{
			name: "unary",
			path: "/api/some.unknown.Service/Method",
		},
		{
			name: "streaming",
			path: "/api" + ynpb.ReadinessService_Watch_FullMethodName,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			handler := httpproxy.NewHTTPHandler(emptyRegistry{})

			req := httptest.NewRequest(http.MethodPost, testCase.path, explodingBody{t: t})
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, req)

			require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		})
	}
}

// unreachableBackend is a proxy.Backend whose GetConnection always fails,
// simulating a registered service whose upstream connection cannot be
// established.
type unreachableBackend struct {
	err error
	// called records whether GetConnection was actually invoked, so the
	// test can distinguish this branch from the registry-miss branch,
	// which also answers 503 without ever calling GetConnection.
	called *bool
}

func (m unreachableBackend) String() string {
	return "unreachable-backend"
}

func (m unreachableBackend) GetConnection(_ context.Context, _ string) (context.Context, *grpc.ClientConn, error) {
	*m.called = true
	return nil, nil, m.err
}

func (m unreachableBackend) AppendInfo(_ bool, resp []byte) ([]byte, error) {
	return resp, nil
}

func (m unreachableBackend) BuildError(_ bool, _ error) ([]byte, error) {
	return nil, nil
}

// unreachableRegistry is a BackendRegistry that always resolves to an
// unreachableBackend, simulating a service that is registered but whose
// control plane connection is currently down.
type unreachableRegistry struct {
	backend unreachableBackend
}

func (m unreachableRegistry) GetBackend(_ string) (proxy.Backend, func(), bool) {
	return m.backend, func() {}, true
}

func (m unreachableRegistry) HasBackend(_ string) bool {
	return true
}

// TestServeHTTP_BackendUnreachable verifies that a GetConnection failure is
// answered with 503, not 500, on both the unary and server-streaming
// request paths, and that the failing backend was actually reached.
func TestServeHTTP_BackendUnreachable(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		path string
	}{
		{
			name: "unary",
			path: "/api" + ynpb.ReadinessService_Ready_FullMethodName,
		},
		{
			name: "streaming",
			path: "/api" + ynpb.ReadinessService_Watch_FullMethodName,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			called := false
			backend := unreachableBackend{err: errors.New("connection refused"), called: &called}
			handler := httpproxy.NewHTTPHandler(unreachableRegistry{backend: backend})

			req := httptest.NewRequest(http.MethodPost, testCase.path, strings.NewReader("{}"))
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, req)

			require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
			require.True(t, called, "GetConnection was never invoked")
		})
	}
}

// TestReadinessServiceWatch_IsServerStreaming pins the fixture used by the
// streaming subtest above: ReadinessService.Watch must remain a
// server-streaming RPC, since that is the only reason a request for it
// reaches handleServerStreaming instead of handleUnary. If the RPC shape
// ever changes, this test fails instead of the streaming subtest silently
// degrading into a duplicate of the unary one.
func TestReadinessServiceWatch_IsServerStreaming(t *testing.T) {
	t.Parallel()

	serviceDesc, err := protoregistry.GlobalFiles.FindDescriptorByName("controlplane.ynpb.v1.ReadinessService")
	require.NoError(t, err)

	svcDesc, ok := serviceDesc.(protoreflect.ServiceDescriptor)
	require.True(t, ok)

	methodDesc := svcDesc.Methods().ByName("Watch")
	require.NotNil(t, methodDesc)
	require.True(t, methodDesc.IsStreamingServer())
}

// workersRecorder answers CountersService.Workers at once, publishing the
// incoming metadata of each call.
type workersRecorder struct {
	ynpb.UnimplementedCountersServiceServer
	calls chan metadata.MD
}

func (m *workersRecorder) Workers(ctx context.Context, request *ynpb.WorkerCountersRequest) (*ynpb.WorkerCountersResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	m.calls <- md
	return &ynpb.WorkerCountersResponse{}, nil
}

// watchRecorder ends ReadinessService.Watch at once, publishing the incoming
// metadata of each call.
type watchRecorder struct {
	ynpb.UnimplementedReadinessServiceServer
	calls chan metadata.MD
}

func (m *watchRecorder) Watch(request *readinesspb.ReadyRequest, stream ynpb.ReadinessService_WatchServer) error {
	md, _ := metadata.FromIncomingContext(stream.Context())
	m.calls <- md
	return nil
}

// newRecordingProxy returns the URL of a proxy in front of both recorders.
//
// Every call the recorders receive is published on the returned channel.
func newRecordingProxy(t *testing.T) (string, chan metadata.MD) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	calls := make(chan metadata.MD, 1)
	grpcServer := grpc.NewServer()
	ynpb.RegisterCountersServiceServer(grpcServer, &workersRecorder{calls: calls})
	ynpb.RegisterReadinessServiceServer(grpcServer, &watchRecorder{calls: calls})
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	server := httptest.NewServer(httpproxy.NewHTTPHandler(singleBackendRegistry{backend: passthroughBackend{conn: conn}}))
	t.Cleanup(server.Close)

	return server.URL, calls
}

// postWithHeaders sends an empty JSON body to the given URL with the given
// headers and returns the response status once the body is drained.
func postWithHeaders(t *testing.T, url string, headers map[string]string) int {
	t.Helper()

	request, err := http.NewRequest(http.MethodPost, url, strings.NewReader("{}"))
	require.NoError(t, err)
	for key, value := range headers {
		request.Header.Set(key, value)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	require.NoError(t, err)
	_, err = io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())

	return response.StatusCode
}

// Test_TransparentWebGRPCProxy_ForwardsOnlyAuthenticationHeader verifies
// that a call reaches its backend with the token as its only header.
//
// A header gRPC metadata cannot carry, such as a UTF-8 cookie, therefore
// does not fail the call.
func Test_TransparentWebGRPCProxy_ForwardsOnlyAuthenticationHeader(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		method string
	}{
		{name: "unary call", method: ynpb.CountersService_Workers_FullMethodName},
		{name: "server-streaming call", method: ynpb.ReadinessService_Watch_FullMethodName},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			url, calls := newRecordingProxy(t)
			statusCode := postWithHeaders(t, url+"/api"+tc.method, map[string]string{
				"X-Yanet-Authentication": "basic dXNlcjpwYXNz",
				"Cookie":                 "name=Иван",
				"X-Custom":               "value",
			})
			require.Equal(t, http.StatusOK, statusCode)

			var md metadata.MD
			select {
			case md = <-calls:
			case <-time.After(5 * time.Second):
				t.Fatal("backend was never called")
			}
			require.Equal(t, []string{"basic dXNlcjpwYXNz"}, md.Get("x-yanet-authentication"))
			require.Empty(t, md.Get("cookie"))
			require.Empty(t, md.Get("x-custom"))
		})
	}
}

// Test_TransparentWebGRPCProxy_RejectsMalformedAuthenticationHeader verifies
// that a token metadata cannot carry gets 400 without calling the backend.
func Test_TransparentWebGRPCProxy_RejectsMalformedAuthenticationHeader(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		method string
	}{
		{name: "unary call", method: ynpb.CountersService_Workers_FullMethodName},
		{name: "server-streaming call", method: ynpb.ReadinessService_Watch_FullMethodName},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			url, calls := newRecordingProxy(t)
			statusCode := postWithHeaders(t, url+"/api"+tc.method, map[string]string{
				"X-Yanet-Authentication": "basic Иван",
			})
			require.Equal(t, http.StatusBadRequest, statusCode)
			require.Empty(t, calls)
		})
	}
}
