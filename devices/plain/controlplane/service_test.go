package plain

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/configstore"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/plain/controlplane/plainpb/v1"
	vlan "github.com/yanet-platform/yanet2/devices/vlan/controlplane"
	"github.com/yanet-platform/yanet2/devices/vlan/controlplane/vlanpb/v1"
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

	service := NewDevicePlainService(NewBackend(agent))
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

	service := NewDevicePlainService(NewBackend(agent))
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

// Test_DevicePlainService_ShowDevice_UnknownName verifies that ShowDevice
// reports NotFound for a name absent from the dataplane device registry.
func Test_DevicePlainService_ShowDevice_UnknownName(t *testing.T) {
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

	service := NewDevicePlainService(NewBackend(agent))

	resp, err := service.ShowDevice(t.Context(), &plainpb.ShowDevicePlainRequest{Name: "absent"})

	require.Nil(t, resp)
	require.Equal(t, codes.NotFound, status.Code(err))
}

// Test_DevicePlainService_ShowDevice_ReportsLiveBindings verifies that
// ShowDevice follows the latest published generation.
//
// The device is republished with a different binding set between two
// reads, so the second read must report the replacement.
func Test_DevicePlainService_ShowDevice_ReportsLiveBindings(t *testing.T) {
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

	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "p-in"}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "p-out"}))

	service := NewDevicePlainService(NewBackend(agent))

	_, err = service.UpdateDevice(t.Context(), &plainpb.UpdateDevicePlainRequest{
		Name: "d0",
		Device: &commonpb.Device{
			Input:  []*commonpb.DevicePipeline{{Name: "p-in", Weight: 3}},
			Output: []*commonpb.DevicePipeline{{Name: "p-out", Weight: 1}},
		},
	})
	require.NoError(t, err)

	resp, err := service.ShowDevice(t.Context(), &plainpb.ShowDevicePlainRequest{Name: "d0"})
	require.NoError(t, err)
	require.True(t, proto.Equal(&commonpb.Device{
		Input:  []*commonpb.DevicePipeline{{Name: "p-in", Weight: 3}},
		Output: []*commonpb.DevicePipeline{{Name: "p-out", Weight: 1}},
	}, resp.GetDevice()))

	_, err = service.UpdateDevice(t.Context(), &plainpb.UpdateDevicePlainRequest{
		Name: "d0",
		Device: &commonpb.Device{
			Output: []*commonpb.DevicePipeline{{Name: "p-out", Weight: 5}},
		},
	})
	require.NoError(t, err)

	resp, err = service.ShowDevice(t.Context(), &plainpb.ShowDevicePlainRequest{Name: "d0"})
	require.NoError(t, err)
	require.True(t, proto.Equal(&commonpb.Device{
		Output: []*commonpb.DevicePipeline{{Name: "p-out", Weight: 5}},
	}, resp.GetDevice()))
}

// Test_DevicePlainService_ShowDevice_IgnoresOtherDeviceTypes verifies that
// ShowDevice reports NotFound for a name registered by a device of a
// different type, exercising the registry type filter.
func Test_DevicePlainService_ShowDevice_IgnoresOtherDeviceTypes(t *testing.T) {
	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(datasize.MB * 32),
		DPMemory:      uint64(datasize.MB * 4),
		WorkerCount:   1,
		DevicesToLoad: []string{"plain", "vlan"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	shm := harness.SharedMemory()

	vlanAgent, err := shm.AgentAttach("vlan", 0, datasize.MB*2)
	require.NoError(t, err)
	t.Cleanup(func() { _ = vlanAgent.CleanUp() })

	_, err = vlan.NewDeviceVlanService(vlan.NewBackend(vlanAgent)).UpdateDevice(t.Context(), &vlanpb.UpdateDeviceVlanRequest{
		Name:   "v0",
		Device: &commonpb.Device{},
		Vlan:   100,
	})
	require.NoError(t, err)

	plainAgent, err := shm.AgentAttach("plain", 0, datasize.MB*2)
	require.NoError(t, err)
	t.Cleanup(func() { _ = plainAgent.CleanUp() })

	service := NewDevicePlainService(NewBackend(plainAgent))

	resp, err := service.ShowDevice(t.Context(), &plainpb.ShowDevicePlainRequest{Name: "v0"})

	require.Nil(t, resp)
	require.Equal(t, codes.NotFound, status.Code(err))
}

// TestDeleteDevice_ReleasesPublishedDevice verifies that deleting a
// device removes it from the dataplane registry and forgets the published
// entry, so the handle is reclaimed rather than left for a later update
// under the same name.
func TestDeleteDevice_ReleasesPublishedDevice(t *testing.T) {
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

	service := NewDevicePlainService(NewBackend(agent))
	_, err = service.UpdateDevice(
		t.Context(),
		&plainpb.UpdateDevicePlainRequest{Name: "extra", Device: &commonpb.Device{}},
	)
	require.NoError(t, err)
	_, err = service.ShowDevice(t.Context(), &plainpb.ShowDevicePlainRequest{Name: "extra"})
	require.NoError(t, err)

	require.NoError(t, service.DeleteDevice("extra"))

	_, err = service.ShowDevice(t.Context(), &plainpb.ShowDevicePlainRequest{Name: "extra"})
	require.Equal(t, codes.NotFound, status.Code(err))
	err = service.DeleteDevice("extra")
	require.ErrorIs(t, err, configstore.ErrNotFound)
}

// TestDeleteDevice_RefusedDeleteKeepsEntry verifies that a delete the
// dataplane refuses leaves the published entry in place: the registry
// delete runs before the entry is withdrawn, so a retried delete reaches
// the registry again instead of reporting the name as unknown.
func TestDeleteDevice_RefusedDeleteKeepsEntry(t *testing.T) {
	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(datasize.MB * 32),
		DPMemory:      uint64(datasize.MB * 4),
		WorkerCount:   1,
		Devices:       []string{"d0"},
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	shm := harness.SharedMemory()
	agent, err := shm.AgentAttach("plain", 0, datasize.MB*2)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	service := NewDevicePlainService(NewBackend(agent))
	_, err = service.UpdateDevice(
		t.Context(),
		&plainpb.UpdateDevicePlainRequest{Name: "d0", Device: &commonpb.Device{}},
	)
	require.NoError(t, err)

	err = service.DeleteDevice("d0")
	require.ErrorIs(t, err, ffi.ErrFailedPrecondition)
	err = service.DeleteDevice("d0")
	require.ErrorIs(t, err, ffi.ErrFailedPrecondition)

	_, err = service.ShowDevice(t.Context(), &plainpb.ShowDevicePlainRequest{Name: "d0"})
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
