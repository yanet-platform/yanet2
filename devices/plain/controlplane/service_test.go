package plain

import (
	"strings"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/plain/controlplane/plainpb/v1"
)

// TestUpdateDevice_ReclaimsSupersededDevice verifies that repeated
// UpdateDevice calls do not leak shared-memory arena space.
//
// Each update after the first supersedes the previous generation's device;
// freeing that handle destroys it once it is dangling, so the arena must
// settle after the first supersede instead of decreasing indefinitely.
func TestUpdateDevice_ReclaimsSupersededDevice(t *testing.T) {
	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(datasize.MB * 32),
		DPMemory:      uint64(datasize.MB * 4),
		WorkerCount:   1,
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	shm := harness.SharedMemory()
	agent, err := shm.AgentAttach("plain", 0, datasize.MB*2)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	service := NewDevicePlainService(agent)
	request := &plainpb.UpdateDevicePlainRequest{
		Name:   "d0",
		Device: &commonpb.Device{},
	}

	const updateCount = 32

	var previousFreeBytes uint64
	for idx := range updateCount {
		_, err := service.UpdateDevice(t.Context(), request)
		require.NoError(t, err)

		freeBytes := freeBytesForAgent(t, shm, "plain")
		// The first supersede is measured before anything was freed
		// against it; every later update destroys its superseded
		// predecessor, so free bytes must hold steady from there on.
		if idx > 1 {
			require.Equalf(
				t,
				previousFreeBytes,
				freeBytes,
				"free bytes changed on update %d: %d -> %d",
				idx,
				previousFreeBytes,
				freeBytes,
			)
		}
		previousFreeBytes = freeBytes
	}
}

// TestUpdateDevice_ReclaimsAcrossMultipleNames verifies that tracking and
// retiring superseded handles works independently per device name.
//
// Two names are created and then updated for two more rounds: every
// update destroys the device it supersedes for its own name, so after
// the first superseding round the arena stops shrinking.
func TestUpdateDevice_ReclaimsAcrossMultipleNames(t *testing.T) {
	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(datasize.MB * 32),
		DPMemory:      uint64(datasize.MB * 4),
		WorkerCount:   1,
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	shm := harness.SharedMemory()
	agent, err := shm.AgentAttach("plain", 0, datasize.MB*2)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	service := NewDevicePlainService(agent)
	names := []string{"d0", "d1"}

	var previousFreeBytes uint64
	for round := range 3 {
		for _, name := range names {
			_, err := service.UpdateDevice(t.Context(), &plainpb.UpdateDevicePlainRequest{
				Name:   name,
				Device: &commonpb.Device{},
			})
			require.NoError(t, err)
		}

		freeBytes := freeBytesForAgent(t, shm, "plain")
		// The first superseding round is measured before anything was
		// freed against it; every later update destroys the devices it
		// supersedes, so free bytes must hold steady from there on.
		if round > 1 {
			require.Equalf(
				t,
				previousFreeBytes,
				freeBytes,
				"free bytes changed on round %d: %d -> %d",
				round,
				previousFreeBytes,
				freeBytes,
			)
		}
		previousFreeBytes = freeBytes
	}
}

// Test_DevicePlainService_UpdateDevice_RejectsOverlongName verifies that a name
// the C-side fixed-size buffer cannot hold is rejected before the agent.
func Test_DevicePlainService_UpdateDevice_RejectsOverlongName(t *testing.T) {
	service := NewDevicePlainService(nil)

	resp, err := service.UpdateDevice(t.Context(), &plainpb.UpdateDevicePlainRequest{
		Name:   strings.Repeat("a", ffi.MaxDeviceNameLen),
		Device: &commonpb.Device{},
	})
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// Test_DevicePlainService_UpdateDevice_AcceptsNameAtLimit verifies that a name
// exactly at the C-side buffer's usable length is published end to end.
func Test_DevicePlainService_UpdateDevice_AcceptsNameAtLimit(t *testing.T) {
	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(datasize.MB * 32),
		DPMemory:      uint64(datasize.MB * 4),
		WorkerCount:   1,
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	shm := harness.SharedMemory()
	agent, err := shm.AgentAttach("plain", 0, datasize.MB*2)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	service := NewDevicePlainService(agent)

	_, err = service.UpdateDevice(t.Context(), &plainpb.UpdateDevicePlainRequest{
		Name:   strings.Repeat("a", ffi.MaxDeviceNameLen-1),
		Device: &commonpb.Device{},
	})
	require.NoError(t, err)
}

// freeBytesForAgent returns the free byte count reported for the named
// agent's first instance.
func freeBytesForAgent(t *testing.T, shm *ffi.SharedMemory, name string) uint64 {
	t.Helper()

	for _, agentInfo := range shm.DPConfig(0).Agents() {
		if agentInfo.Name == name {
			require.NotEmpty(t, agentInfo.Instances)
			return agentInfo.Instances[0].FreeBytes
		}
	}

	t.Fatalf("agent %q not found in dataplane config", name)
	return 0
}
