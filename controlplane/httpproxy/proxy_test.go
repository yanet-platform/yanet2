package httpproxy_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
	"github.com/yanet-platform/yanet2/controlplane/httpproxy"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// emptyRegistry is a BackendRegistry that knows no service, simulating a
// service that is missing from the gateway's registry.
type emptyRegistry struct{}

func (m emptyRegistry) HasBackend(_ string) bool {
	return false
}

// everyServiceRegistry is a BackendRegistry that knows every service,
// simulating a service registered with the gateway.
type everyServiceRegistry struct{}

func (m everyServiceRegistry) HasBackend(_ string) bool {
	return true
}

// admitAll is an Admitter that lets every call in with no metadata and no
// bound on its body.
func admitAll(_ *http.Request, _ string) (httpproxy.Admission, error) {
	return httpproxy.Admission{MaxBodySize: math.MaxInt64, Release: func() {}}, nil
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

			handler := httpproxy.NewHTTPHandler(nil, emptyRegistry{}, admitAll)

			req := httptest.NewRequest(http.MethodPost, testCase.path, strings.NewReader("{}"))
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, req)

			require.Equal(t, http.StatusServiceUnavailable, recorder.Code, testCase.rationale)
		})
	}
}

// explodingBody is an io.Reader that fails the test if it is ever read.
//
// It stands in for the body of a call the handler must answer before
// reading it.
type explodingBody struct {
	t *testing.T
}

func (m explodingBody) Read(_ []byte) (int, error) {
	m.t.Fatal("request body was read for a call that had to be answered first")
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

			handler := httpproxy.NewHTTPHandler(nil, emptyRegistry{}, admitAll)

			req := httptest.NewRequest(http.MethodPost, testCase.path, explodingBody{t: t})
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, req)

			require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		})
	}
}

// unreachableBackend is a Backend whose GetConnection always fails,
// simulating a registered service whose upstream connection cannot be
// established.
type unreachableBackend struct {
	err error
	// called records whether GetConnection was actually invoked, so the
	// test can distinguish this branch from the registry-miss branch,
	// which also answers 503 without ever calling GetConnection.
	called *bool
}

func (m unreachableBackend) GetConnection(_ context.Context, _ string) (context.Context, *grpc.ClientConn, error) {
	*m.called = true
	return nil, nil, m.err
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
			handler := httpproxy.NewHTTPHandler(backend, everyServiceRegistry{}, admitAll)

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

	admit := func(_ *http.Request, _ string) (httpproxy.Admission, error) {
		return httpproxy.Admission{
			Metadata:    metadata.Pairs("x-test-admission", "admitted"),
			MaxBodySize: math.MaxInt64,
			Release:     func() {},
		}, nil
	}
	server := httptest.NewServer(httpproxy.NewHTTPHandler(passthroughBackend{conn: conn}, everyServiceRegistry{}, admit))
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

// Test_TransparentWebGRPCProxy_ForwardsOnlyAdmissionMetadata verifies that a
// call carries to its backend its admission's metadata, no request header.
//
// A header gRPC metadata cannot carry, such as a UTF-8 cookie, therefore
// does not fail the call.
func Test_TransparentWebGRPCProxy_ForwardsOnlyAdmissionMetadata(t *testing.T) {
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
			require.Equal(t, []string{"admitted"}, md.Get("x-test-admission"))
			require.Empty(t, md.Get("x-yanet-authentication"))
			require.Empty(t, md.Get("cookie"))
			require.Empty(t, md.Get("x-custom"))
		})
	}
}

// Test_TransparentWebGRPCProxy_RefusedCallReadsNoBody verifies that a call
// its admitter refuses gets the refusal's status before any body is read.
//
// It holds for compressed bodies and on both request paths.
func Test_TransparentWebGRPCProxy_RefusedCallReadsNoBody(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		method   string
		encoding string
		code     codes.Code
		status   int
	}{
		{
			name:   "unary call of an unauthenticated caller",
			method: ynpb.ReadinessService_Ready_FullMethodName,
			code:   codes.Unauthenticated,
			status: http.StatusUnauthorized,
		},
		{
			name:   "streaming call of a caller without permission",
			method: ynpb.ReadinessService_Watch_FullMethodName,
			code:   codes.PermissionDenied,
			status: http.StatusForbidden,
		},
		{
			name:     "compressed call of a caller without permission",
			method:   ynpb.ReadinessService_Ready_FullMethodName,
			encoding: "gzip",
			code:     codes.PermissionDenied,
			status:   http.StatusForbidden,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			refuse := func(_ *http.Request, _ string) (httpproxy.Admission, error) {
				return httpproxy.Admission{}, status.Error(tc.code, "refused")
			}
			handler := httpproxy.GzipMiddleware(httpproxy.NewHTTPHandler(nil, everyServiceRegistry{}, refuse))

			request := httptest.NewRequest(http.MethodPost, "/api"+tc.method, explodingBody{t: t})
			if tc.encoding != "" {
				request.Header.Set("Content-Encoding", tc.encoding)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			require.Equal(t, tc.status, recorder.Code)
		})
	}
}

// Test_TransparentWebGRPCProxy_BoundsBodyByAdmission verifies that a body
// past its admission's bound gets 413 without reaching the backend.
//
// The bound counts decompressed bytes, and a body at the bound reaches the
// backend.
func Test_TransparentWebGRPCProxy_BoundsBodyByAdmission(t *testing.T) {
	t.Parallel()

	const bound = 1024
	// A JSON object padded with spaces stays valid at any size.
	padded := "{}" + strings.Repeat(" ", bound-2)
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, err := writer.Write([]byte(padded + " "))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	require.Less(t, compressed.Len(), bound, "the compressed body alone must stay within the bound")

	for _, tc := range []struct {
		name     string
		method   string
		body     string
		encoding string
		status   int
		reached  bool
	}{
		{
			name:    "unary call with a body at the bound",
			method:  ynpb.ReadinessService_Ready_FullMethodName,
			body:    padded,
			status:  http.StatusServiceUnavailable,
			reached: true,
		},
		{
			name:   "unary call with a body past the bound",
			method: ynpb.ReadinessService_Ready_FullMethodName,
			body:   padded + " ",
			status: http.StatusRequestEntityTooLarge,
		},
		{
			name:     "unary call with a gzip body past the bound once decompressed",
			method:   ynpb.ReadinessService_Ready_FullMethodName,
			body:     compressed.String(),
			encoding: "gzip",
			status:   http.StatusRequestEntityTooLarge,
		},
		{
			name:   "server-streaming call with a body past the bound",
			method: ynpb.ReadinessService_Watch_FullMethodName,
			body:   padded + " ",
			status: http.StatusRequestEntityTooLarge,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reached := false
			backend := unreachableBackend{err: errors.New("connection refused"), called: &reached}
			admit := func(_ *http.Request, _ string) (httpproxy.Admission, error) {
				return httpproxy.Admission{MaxBodySize: bound, Release: func() {}}, nil
			}
			handler := httpproxy.GzipMiddleware(httpproxy.NewHTTPHandler(backend, everyServiceRegistry{}, admit))

			request := httptest.NewRequest(http.MethodPost, "/api"+tc.method, strings.NewReader(tc.body))
			if tc.encoding != "" {
				request.Header.Set("Content-Encoding", tc.encoding)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			require.Equal(t, tc.status, recorder.Code, recorder.Body.String())
			require.Equal(t, tc.reached, reached, "whether the call reached its backend")
		})
	}
}

// Test_TransparentWebGRPCProxy_ReleasesAdmission verifies that an admission
// is released exactly once when its call is done.
//
// It holds whether the call reached its backend or ended before.
func Test_TransparentWebGRPCProxy_ReleasesAdmission(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "call that reached its backend", body: "{}"},
		{name: "call refused for its size", body: "{}" + strings.Repeat(" ", 64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			released := 0
			admit := func(_ *http.Request, _ string) (httpproxy.Admission, error) {
				return httpproxy.Admission{MaxBodySize: 32, Release: func() { released++ }}, nil
			}
			called := false
			backend := unreachableBackend{err: errors.New("connection refused"), called: &called}
			handler := httpproxy.NewHTTPHandler(backend, everyServiceRegistry{}, admit)

			request := httptest.NewRequest(http.MethodPost, "/api"+ynpb.ReadinessService_Ready_FullMethodName, strings.NewReader(tc.body))
			handler.ServeHTTP(httptest.NewRecorder(), request)

			require.Equal(t, 1, released)
		})
	}
}

// Test_TransparentWebGRPCProxy_AllowsCredentialsOnlyForTrustedOrigin
// verifies that only a trusted origin may send its calls with the browser's
// credentials.
func Test_TransparentWebGRPCProxy_AllowsCredentialsOnlyForTrustedOrigin(t *testing.T) {
	t.Parallel()

	handler := httpproxy.NewHTTPHandler(nil, emptyRegistry{}, admitAll, httpproxy.WithTrustedOrigins([]string{"https://ui.example"}))
	for _, tc := range []struct {
		name        string
		origin      string
		credentials string
	}{
		{name: "trusted origin", origin: "https://ui.example", credentials: "true"},
		{name: "untrusted origin", origin: "https://evil.example", credentials: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			request := httptest.NewRequest(http.MethodOptions, "/api/some.Service/Method", nil)
			request.Header.Set("Origin", tc.origin)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusOK, recorder.Code)
			require.Equal(t, tc.credentials, recorder.Header().Get("Access-Control-Allow-Credentials"))
		})
	}
}
