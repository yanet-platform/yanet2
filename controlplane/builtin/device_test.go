package builtin_test

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/builtin"
	"github.com/yanet-platform/yanet2/controlplane/configstore"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
	cplain "github.com/yanet-platform/yanet2/devices/plain/bindings/go/cplain"
	plain "github.com/yanet-platform/yanet2/devices/plain/controlplane"
	plainpb "github.com/yanet-platform/yanet2/devices/plain/controlplane/plainpb/v1"
	trafgen "github.com/yanet-platform/yanet2/devices/trafgen/controlplane"
	vlan "github.com/yanet-platform/yanet2/devices/vlan/controlplane"
	vxlan "github.com/yanet-platform/yanet2/devices/vxlan/controlplane"
)

// The device modules own the handles a Device service delete reclaims;
// the assertions pin the owner contract to each module wrapper the
// director wires.
var (
	_ builtin.DeviceOwner = (*plain.DevicePlainDevice)(nil)
	_ builtin.DeviceOwner = (*vlan.DeviceVlanDevice)(nil)
	_ builtin.DeviceOwner = (*trafgen.TrafgenDevice)(nil)
	_ builtin.DeviceOwner = (*vxlan.DeviceVxlanDevice)(nil)
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

// Test_Device_Delete_RemovesDevice verifies that Delete removes a
// non-topology device from the dataplane registry while leaving the
// predefined topology device in place.
func Test_Device_Delete_RemovesDevice(t *testing.T) {
	harness := newDeviceHarness(t)
	shm := harness.SharedMemory()

	agent, err := shm.AgentAttach("plain", 0, datasize.MB*4)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	deviceConfig, err := cplain.NewDeviceConfig(agent, "extra", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = deviceConfig.Free() })

	require.NoError(t, agent.UpdateDevices([]ffi.ShmDeviceConfig{deviceConfig.AsFFIDevice()}))
	require.Contains(t, deviceNames(shm.DPConfig(0).Devices()), "extra")

	svc := builtin.NewDevice(0, shm, deviceOwnerPath)

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

	svc := builtin.NewDevice(0, shm, deviceOwnerPath)

	_, err := svc.Delete(t.Context(), &ynpb.DeleteDeviceRequest{Name: "d0"})

	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Contains(t, deviceNames(shm.DPConfig(0).Devices()), "d0")
}

// Test_Device_Delete_MissingDevice verifies that Delete reports NotFound
// for a device name absent from the registry.
func Test_Device_Delete_MissingDevice(t *testing.T) {
	harness := newDeviceHarness(t)
	shm := harness.SharedMemory()

	svc := builtin.NewDevice(0, shm, deviceOwnerPath)

	_, err := svc.Delete(t.Context(), &ynpb.DeleteDeviceRequest{Name: "absent"})

	require.Equal(t, codes.NotFound, status.Code(err))
}

// plainOwner adapts the plain service to the Device service's owner
// contract, standing in for the module wrapper the director wires.
type plainOwner struct {
	service *plain.DevicePlainService
}

func (m *plainOwner) DeviceType() string {
	return "plain"
}

func (m *plainOwner) Dataplane() (string, uint32) {
	return deviceOwnerPath, 0
}

func (m *plainOwner) DeleteDevice(name string) error {
	return m.service.DeleteDevice(name)
}

// recordingOwner records the deletes routed to an owner of one device
// type and answers them with a fixed error.
type recordingOwner struct {
	deviceType string
	memoryPath string
	instanceID uint32
	deleted    []string
	err        error
}

func (m *recordingOwner) DeviceType() string {
	return m.deviceType
}

func (m *recordingOwner) Dataplane() (string, uint32) {
	return m.memoryPath, m.instanceID
}

func (m *recordingOwner) DeleteDevice(name string) error {
	m.deleted = append(m.deleted, name)
	return m.err
}

// deviceOwnerPath is the shared-memory path the tests register owners
// for; it needs no existing file, only agreement between the Device
// service and its owners.
const deviceOwnerPath = "/dev/hugepages/yanet"

// newPlainOwnerHarness attaches a plain agent and publishes the named
// device through a real plain service.
func newPlainOwnerHarness(t *testing.T, name string) (*ffi.SharedMemory, *plain.DevicePlainService) {
	t.Helper()

	harness := newDeviceHarness(t)
	shm := harness.SharedMemory()

	agent, err := shm.AgentAttach("plain", 0, datasize.MB*4)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	service := plain.NewDevicePlainService(plain.NewBackend(agent))
	_, err = service.UpdateDevice(
		t.Context(),
		&plainpb.UpdateDevicePlainRequest{Name: name, Device: &commonpb.Device{}},
	)
	require.NoError(t, err)

	return shm, service
}

// Test_Device_Delete_ReclaimsOwnerEntry verifies that a delete routed to
// the owner of the device's type removes the registry entry together with
// the owner's published entry, so the handle does not wait for a later
// update under the same name.
func Test_Device_Delete_ReclaimsOwnerEntry(t *testing.T) {
	shm, service := newPlainOwnerHarness(t, "extra")
	require.Contains(t, deviceNames(shm.DPConfig(0).Devices()), "extra")

	svc := builtin.NewDevice(0, shm, deviceOwnerPath, &plainOwner{service: service})

	_, err := svc.Delete(t.Context(), &ynpb.DeleteDeviceRequest{Name: "extra"})
	require.NoError(t, err)

	require.NotContains(t, deviceNames(shm.DPConfig(0).Devices()), "extra")
	err = service.DeleteDevice("extra")
	require.ErrorIs(t, err, configstore.ErrNotFound)
}

// Test_Device_Delete_FallsBackWithoutOwnerEntry verifies that a device
// published beside its owner — before a restart or through another
// writer — is still removed from the registry when the owner knows
// nothing of the name.
func Test_Device_Delete_FallsBackWithoutOwnerEntry(t *testing.T) {
	harness := newDeviceHarness(t)
	shm := harness.SharedMemory()

	agent, err := shm.AgentAttach("plain", 0, datasize.MB*4)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	deviceConfig, err := cplain.NewDeviceConfig(agent, "extra", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = deviceConfig.Free() })
	require.NoError(t, agent.UpdateDevices([]ffi.ShmDeviceConfig{deviceConfig.AsFFIDevice()}))

	svc := builtin.NewDevice(0, shm, deviceOwnerPath, &plainOwner{service: plain.NewDevicePlainService(plain.NewBackend(agent))})

	_, err = svc.Delete(t.Context(), &ynpb.DeleteDeviceRequest{Name: "extra"})
	require.NoError(t, err)

	require.NotContains(t, deviceNames(shm.DPConfig(0).Devices()), "extra")
}

// Test_Device_Delete_IgnoresOwnerOfOtherType verifies that only the owner
// of the deleted device's type is asked to reclaim the name.
func Test_Device_Delete_IgnoresOwnerOfOtherType(t *testing.T) {
	harness := newDeviceHarness(t)
	shm := harness.SharedMemory()

	agent, err := shm.AgentAttach("plain", 0, datasize.MB*4)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	deviceConfig, err := cplain.NewDeviceConfig(agent, "extra", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = deviceConfig.Free() })
	require.NoError(t, agent.UpdateDevices([]ffi.ShmDeviceConfig{deviceConfig.AsFFIDevice()}))

	other := &recordingOwner{deviceType: "vlan", memoryPath: deviceOwnerPath}
	svc := builtin.NewDevice(0, shm, deviceOwnerPath, other)

	_, err = svc.Delete(t.Context(), &ynpb.DeleteDeviceRequest{Name: "extra"})
	require.NoError(t, err)

	require.Empty(t, other.deleted)
	require.NotContains(t, deviceNames(shm.DPConfig(0).Devices()), "extra")
}

// Test_Device_Delete_ReportsOwnerRefusal verifies that a delete the owner
// refuses reaches the caller with the refusing status and leaves the
// device in the registry.
func Test_Device_Delete_ReportsOwnerRefusal(t *testing.T) {
	harness := newDeviceHarness(t)
	shm := harness.SharedMemory()

	owner := &recordingOwner{
		deviceType: "plain",
		memoryPath: deviceOwnerPath,
		err:        ffi.ErrFailedPrecondition,
	}
	svc := builtin.NewDevice(0, shm, deviceOwnerPath, owner)

	_, err := svc.Delete(t.Context(), &ynpb.DeleteDeviceRequest{Name: "d0"})

	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Equal(t, []string{"d0"}, owner.deleted)
	require.Contains(t, deviceNames(shm.DPConfig(0).Devices()), "d0")
}

// Test_Device_Delete_IgnoresOwnerOfOtherDataplane verifies that an owner
// serving another shared-memory dataplane is not routed this service's
// deletes: the device is removed from this dataplane's registry alone.
func Test_Device_Delete_IgnoresOwnerOfOtherDataplane(t *testing.T) {
	harness := newDeviceHarness(t)
	shm := harness.SharedMemory()

	agent, err := shm.AgentAttach("plain", 0, datasize.MB*4)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	deviceConfig, err := cplain.NewDeviceConfig(agent, "extra", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = deviceConfig.Free() })
	require.NoError(t, agent.UpdateDevices([]ffi.ShmDeviceConfig{deviceConfig.AsFFIDevice()}))

	foreign := &recordingOwner{
		deviceType: "plain",
		memoryPath: "/dev/hugepages/other",
	}
	svc := builtin.NewDevice(0, shm, deviceOwnerPath, foreign)

	_, err = svc.Delete(t.Context(), &ynpb.DeleteDeviceRequest{Name: "extra"})
	require.NoError(t, err)

	require.Empty(t, foreign.deleted)
	require.NotContains(t, deviceNames(shm.DPConfig(0).Devices()), "extra")
}

// Test_Device_Delete_IgnoresOwnerOfOtherInstance verifies that an owner
// serving another instance of the same shared-memory dataplane is not
// routed this service's deletes either.
func Test_Device_Delete_IgnoresOwnerOfOtherInstance(t *testing.T) {
	harness := newDeviceHarness(t)
	shm := harness.SharedMemory()

	agent, err := shm.AgentAttach("plain", 0, datasize.MB*4)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	deviceConfig, err := cplain.NewDeviceConfig(agent, "extra", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = deviceConfig.Free() })
	require.NoError(t, agent.UpdateDevices([]ffi.ShmDeviceConfig{deviceConfig.AsFFIDevice()}))

	foreign := &recordingOwner{
		deviceType: "plain",
		memoryPath: deviceOwnerPath,
		instanceID: 1,
	}
	svc := builtin.NewDevice(0, shm, deviceOwnerPath, foreign)

	_, err = svc.Delete(t.Context(), &ynpb.DeleteDeviceRequest{Name: "extra"})
	require.NoError(t, err)

	require.Empty(t, foreign.deleted)
	require.NotContains(t, deviceNames(shm.DPConfig(0).Devices()), "extra")
}
