package builtin_test

import (
	"strings"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/builtin"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
	plain "github.com/yanet-platform/yanet2/devices/plain/controlplane"
)

// newDeviceHarness builds a one-instance in-process dataplane harness with
// a single predefined topology device "d0" of type "plain" and registers
// its teardown.
func newDeviceHarness(t *testing.T) *dataplaneut.Harness {
	t.Helper()

	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(datasize.MB * 32),
		DPMemory:      uint64(datasize.MB * 4),
		WorkerCount:   1,
		Devices:       []string{"d0"},
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	return harness
}

// deviceNames returns the registry names of the given devices in order.
func deviceNames(devices []ffi.DeviceInfo) []string {
	names := make([]string, len(devices))
	for idx, device := range devices {
		names[idx] = device.Name
	}
	return names
}

// Test_Device_Delete_RejectsInvalidName verifies that Delete rejects a
// name that is empty, contains an interior NUL, or exceeds the C-side
// buffer, before any shared memory is touched.
func Test_Device_Delete_RejectsInvalidName(t *testing.T) {
	cases := []struct {
		name       string
		deviceName string
	}{
		{
			name:       "empty name",
			deviceName: "",
		},
		{
			name:       "interior NUL",
			deviceName: "extra\x00keep",
		},
		{
			name:       "overlong name",
			deviceName: strings.Repeat("d", ffi.MaxDeviceNameLen),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := builtin.NewDevice(0, nil)

			_, err := svc.Delete(t.Context(), &ynpb.DeleteDeviceRequest{Name: tc.deviceName})

			require.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
}

// Test_Device_Delete_RemovesDevice verifies that Delete removes a
// non-topology device from the dataplane registry while leaving the
// predefined topology device in place.
func Test_Device_Delete_RemovesDevice(t *testing.T) {
	harness := newDeviceHarness(t)
	shm := harness.SharedMemory()

	agent, err := shm.AgentAttach("plain", 0, datasize.MB*4)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	deviceConfig, err := plain.NewDeviceConfig(agent, "extra", &commonpb.Device{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = deviceConfig.Free() })

	require.NoError(t, agent.UpdateDevices([]ffi.ShmDeviceConfig{deviceConfig.AsFFIDevice()}))
	require.Contains(t, deviceNames(shm.DPConfig(0).Devices()), "extra")

	svc := builtin.NewDevice(0, shm)

	_, err = svc.Delete(t.Context(), &ynpb.DeleteDeviceRequest{Name: "extra"})
	require.NoError(t, err)

	names := deviceNames(shm.DPConfig(0).Devices())
	require.NotContains(t, names, "extra")
	require.Contains(t, names, "d0")
}

// Test_Device_Delete_RefusesTopologyDevice verifies that Delete refuses to
// remove a predefined topology device and leaves the registry unchanged.
func Test_Device_Delete_RefusesTopologyDevice(t *testing.T) {
	harness := newDeviceHarness(t)
	shm := harness.SharedMemory()

	svc := builtin.NewDevice(0, shm)

	_, err := svc.Delete(t.Context(), &ynpb.DeleteDeviceRequest{Name: "d0"})

	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Contains(t, deviceNames(shm.DPConfig(0).Devices()), "d0")
}

// Test_Device_Delete_MissingDevice verifies that Delete reports NotFound
// for a device name absent from the registry.
func Test_Device_Delete_MissingDevice(t *testing.T) {
	harness := newDeviceHarness(t)
	shm := harness.SharedMemory()

	svc := builtin.NewDevice(0, shm)

	_, err := svc.Delete(t.Context(), &ynpb.DeleteDeviceRequest{Name: "absent"})

	require.Equal(t, codes.NotFound, status.Code(err))
}
