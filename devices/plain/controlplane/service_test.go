package plain

import (
	"strings"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/plain/controlplane/plainpb/v1"
	vlan "github.com/yanet-platform/yanet2/devices/vlan/controlplane"
	"github.com/yanet-platform/yanet2/devices/vlan/controlplane/vlanpb/v1"
)

// Test_DevicePlainService_UpdateDevice_RejectsInvalidWeights verifies that model
// validation produces InvalidArgument before allocating a device through FFI.
func Test_DevicePlainService_UpdateDevice_RejectsInvalidWeights(t *testing.T) {
	service := NewDevicePlainService(nil)
	_, err := service.UpdateDevice(t.Context(), &plainpb.UpdateDevicePlainRequest{
		Name: "weights",
		Device: &commonpb.Device{
			Input: []*commonpb.DevicePipeline{{Name: "pipeline", Weight: 65536}},
		},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

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

// Test_DevicePlainService_ShowDevice_RejectsInvalidName verifies that
// ShowDevice rejects a name that is empty, contains an interior NUL, or
// exceeds the C-side buffer, before the agent is ever touched.
func Test_DevicePlainService_ShowDevice_RejectsInvalidName(t *testing.T) {
	cases := []struct {
		name       string
		deviceName string
	}{
		{name: "empty name", deviceName: ""},
		{name: "interior NUL", deviceName: "extra\x00keep"},
		{name: "overlong name", deviceName: strings.Repeat("d", ffi.MaxDeviceNameLen)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := NewDevicePlainService(nil)

			resp, err := service.ShowDevice(t.Context(), &plainpb.ShowDevicePlainRequest{Name: tc.deviceName})

			require.Nil(t, resp)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
		})
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

	service := NewDevicePlainService(agent)

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

	service := NewDevicePlainService(agent)

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

	_, err = vlan.NewDeviceVlanService(vlanAgent).UpdateDevice(t.Context(), &vlanpb.UpdateDeviceVlanRequest{
		Name:   "v0",
		Device: &commonpb.Device{},
		Vlan:   100,
	})
	require.NoError(t, err)

	plainAgent, err := shm.AgentAttach("plain", 0, datasize.MB*2)
	require.NoError(t, err)
	t.Cleanup(func() { _ = plainAgent.CleanUp() })

	service := NewDevicePlainService(plainAgent)

	resp, err := service.ShowDevice(t.Context(), &plainpb.ShowDevicePlainRequest{Name: "v0"})

	require.Nil(t, resp)
	require.Equal(t, codes.NotFound, status.Code(err))
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
