package l3b_test

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/xnetip"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	controlplane "github.com/yanet-platform/yanet2/modules/l3b/controlplane"
	l3bpb "github.com/yanet-platform/yanet2/modules/l3b/controlplane/l3bpb/v1"
)

// newTestBackend returns a shared-memory backend over a harness with the l3b
// module and its objects loaded.
func newTestBackend(t *testing.T) controlplane.Backend {
	t.Helper()

	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(64 * datasize.MB),
		DPMemory:      uint64(4 * datasize.MB),
		WorkerCount:   1,
		Modules:       []string{"l3b"},
		ObjectsToLoad: []string{"l3b_virtual_service", "l3b_session_table"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	agent, err := harness.SharedMemory().AgentAttach("l3b-backend-test", 0, 16*datasize.MB)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	return controlplane.NewBackend(agent)
}

// Test_Backend_UpdateRealServer_IndexOutOfRange verifies that a real server
// index past the service's real servers is reported as InvalidArgument.
func Test_Backend_UpdateRealServer_IndexOutOfRange(t *testing.T) {
	backend := newTestBackend(t)
	require.NoError(t, backend.CreateService(sampleService("vs0")))

	err := backend.UpdateRealServerState("vs0", 0, false)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	err = backend.UpdateRealServerWeight("vs0", 0, 1)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// Test_Backend_CreateService_FilterErrorNotNested verifies that an invalid
// source filter network is reported as InvalidArgument without a nested status.
func Test_Backend_CreateService_FilterErrorNotNested(t *testing.T) {
	backend := newTestBackend(t)
	service := sampleService("vs0")
	service.SourceFilterRules = []*l3bpb.SourceFilterRule{{
		Net6S: []*commonpb.IPv6Network{commonpb.NewIPv6NetworkFrom6(xnetip.MustParseNetwork6("2001:db8::/ffff:0:ffff::"))},
	}}

	err := backend.CreateService(service)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.NotContains(t, status.Convert(err).Message(), "rpc error")
}
