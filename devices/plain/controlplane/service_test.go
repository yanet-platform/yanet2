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

// TestUpdateDevice_DrainsUnusedDevices verifies that repeated UpdateDevice
// calls do not leak shared-memory arena space.
//
// Each update after the first retires the previous generation's device onto
// the agent's unused list; without draining that list, the arena shrinks by
// a fixed amount every call and eventually runs out. Free bytes must settle
// after the first update instead of decreasing indefinitely.
func TestUpdateDevice_DrainsUnusedDevices(t *testing.T) {
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

// TestUpdateDevices_ParksTwoPredecessorsBackToBack verifies that a single
// UpdateDevices call replacing two live devices at once drains cleanly.
//
// Replacing two devices in one call parks both predecessors onto the
// agent's unused list back-to-back, before either is drained. That is the
// only way to link a second entry onto a non-empty unused list, so it is
// the case a single-device update (one park per drain) can never exercise.
func TestUpdateDevices_ParksTwoPredecessorsBackToBack(t *testing.T) {
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

	freeBytesBeforeReplace := freeBytesForAgent(t, shm, "plain")

	deviceConfig0, err := NewDeviceConfig(agent, "d0", &commonpb.Device{})
	require.NoError(t, err)
	deviceConfig1, err := NewDeviceConfig(agent, "d1", &commonpb.Device{})
	require.NoError(t, err)

	err = agent.UpdateDevices([]ffi.ShmDeviceConfig{
		deviceConfig0.AsFFIDevice(),
		deviceConfig1.AsFFIDevice(),
	})
	require.NoError(t, err)

	DrainUnusedDevices(agent)

	// Both predecessors retired by this replace and both fresh configs
	// built above are reclaimed, so the arena settles back to the size
	// it had right before the replace.
	require.Equal(t, freeBytesBeforeReplace, freeBytesForAgent(t, shm, "plain"))
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
