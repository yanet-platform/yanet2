package vlan

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/vlan/controlplane/vlanpb/v1"
)

// TestUpdateDevice_DrainsUnusedDevices verifies that repeated UpdateDevice
// calls do not leak shared-memory arena space.
//
// Each update after the first supersedes the previous generation's device;
// freeing that handle destroys it once it is dangling, so the arena must
// settle after the first supersede instead of decreasing indefinitely.
func TestUpdateDevice_DrainsUnusedDevices(t *testing.T) {
	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(datasize.MB * 32),
		DPMemory:      uint64(datasize.MB * 4),
		WorkerCount:   1,
		DevicesToLoad: []string{"vlan"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	shm := harness.SharedMemory()
	agent, err := shm.AgentAttach("vlan", 0, datasize.MB*2)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	service := NewDeviceVlanService(agent)
	request := &vlanpb.UpdateDeviceVlanRequest{
		Name:   "d0",
		Device: &commonpb.Device{},
		Vlan:   100,
	}

	const updateCount = 32

	var previousFreeBytes uint64
	for idx := range updateCount {
		_, err := service.UpdateDevice(t.Context(), request)
		require.NoError(t, err)

		freeBytes := freeBytesForAgent(t, shm, "vlan")
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
