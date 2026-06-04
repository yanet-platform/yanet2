package gateway

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// sharedServerService is a test Service whose Endpoint returns "" so it is
// hosted on the gateway's own gRPC server.
type sharedServerService struct {
	name     string
	svcNames []string
}

func (m *sharedServerService) Name() string                   { return m.name }
func (m *sharedServerService) Endpoint() string               { return "" }
func (m *sharedServerService) ServicesNames() []string        { return m.svcNames }
func (m *sharedServerService) RegisterService(_ *grpc.Server) {}

// TestNewGateway_DeclaredKindsWired verifies that WithBuiltinService records
// BackendKindBuiltin and WithService records BackendKindInProcess for services
// that share the gateway's own gRPC server, regardless of endpoint being empty
// in both cases.
func TestNewGateway_DeclaredKindsWired(t *testing.T) {
	t.Parallel()

	builtinSvc := &sharedServerService{
		name:     "builtin-framework",
		svcNames: []string{"test.BuiltinService"},
	}
	inprocSvc := &sharedServerService{
		name:     "inproc-module",
		svcNames: []string{"test.InProcessService"},
	}

	cfg := DefaultConfig()
	gw, err := NewGateway(cfg,
		WithBuiltinService(builtinSvc),
		WithService(inprocSvc),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = gw.Close() })

	entries := gw.registry.ListBackends()
	kinds := map[string]BackendKind{}
	for _, e := range entries {
		kinds[e.Service()] = e.Kind()
	}

	// Framework services registered with WithBuiltinService must be built-in.
	require.Equal(t, BackendKindBuiltin, kinds["controlplane.ynpb.v1.Gateway"], "controlplane.ynpb.v1.Gateway must be built-in")
	require.Equal(t, BackendKindBuiltin, kinds["controlplane.ynpb.v1.Auth"], "controlplane.ynpb.v1.Auth must be built-in")
	require.Equal(t, BackendKindBuiltin, kinds["test.BuiltinService"], "WithBuiltinService must yield built-in kind")

	// Module/device services registered with WithService must be in-process.
	require.Equal(t, BackendKindInProcess, kinds["test.InProcessService"], "WithService must yield in-process kind")
}
