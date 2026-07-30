package httpproxy_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/siderolabs/grpc-proxy/proxy"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/yanet-platform/yanet2/controlplane/httpproxy"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// emptyRegistry is a BackendRegistry that never has a matching backend,
// simulating a service that is missing from the gateway's registry.
type emptyRegistry struct{}

func (m emptyRegistry) GetBackend(_ string) (proxy.Backend, bool) {
	return nil, false
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
