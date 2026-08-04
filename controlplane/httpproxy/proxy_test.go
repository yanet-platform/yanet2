package httpproxy_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/siderolabs/grpc-proxy/proxy"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

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
