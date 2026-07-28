package plain

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/plain/controlplane/plainpb/v1"
)

// TestUpdateDevice_ReclaimsSupersededDevice verifies that repeated
// UpdateDevice calls do not leak shared-memory arena space.
//
// Each update after the first supersedes the previous generation's device;
// the service frees that handle explicitly once the new generation is
// installed, so free bytes must settle after the first update instead of
// decreasing indefinitely.
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
		if idx > 0 {
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
// reclaiming superseded handles works independently per device name.
//
// Two names are created and then each is updated again: every superseded
// handle is freed by the service, so the arena settles back to the size it
// had right before the second round of updates.
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

	for _, name := range []string{"d0", "d1"} {
		_, err := service.UpdateDevice(t.Context(), &plainpb.UpdateDevicePlainRequest{
			Name:   name,
			Device: &commonpb.Device{},
		})
		require.NoError(t, err)
	}

	freeBytesBeforeSecondRound := freeBytesForAgent(t, shm, "plain")

	for _, name := range []string{"d0", "d1"} {
		_, err := service.UpdateDevice(t.Context(), &plainpb.UpdateDevicePlainRequest{
			Name:   name,
			Device: &commonpb.Device{},
		})
		require.NoError(t, err)
	}

	// Both superseded handles from the first round are freed by the second
	// round of updates, so the arena returns to its pre-second-round size.
	require.Equal(
		t,
		freeBytesBeforeSecondRound,
		freeBytesForAgent(t, shm, "plain"),
	)
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
