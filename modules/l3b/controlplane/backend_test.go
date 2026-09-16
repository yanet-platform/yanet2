package l3b_test

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	controlplane "github.com/yanet-platform/yanet2/modules/l3b/controlplane"
)

// Test_Backend_UpdateRealServer_IndexOutOfRange verifies that a real server
// index past the service's real servers is reported as InvalidArgument.
func Test_Backend_UpdateRealServer_IndexOutOfRange(t *testing.T) {
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

	backend := controlplane.NewBackend(agent)
	require.NoError(t, backend.CreateService(sampleService("vs0")))

	err = backend.UpdateRealServerState("vs0", 0, false)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	err = backend.UpdateRealServerWeight("vs0", 0, 1)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}
