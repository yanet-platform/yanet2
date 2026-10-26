package functional_test

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	cplain "github.com/yanet-platform/yanet2/devices/plain/bindings/go/cplain"
	cvlan "github.com/yanet-platform/yanet2/devices/vlan/bindings/go/cvlan"
)

func TestDeviceRegistryRejectsSameNameAcrossTypes(t *testing.T) {
	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(datasize.MB * 32),
		DPMemory:      uint64(datasize.MB * 4),
		WorkerCount:   1,
		Devices:       []string{"d0"},
		DevicesToLoad: []string{"plain", "vlan"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	shm := harness.SharedMemory()
	const agentName = "device-registry-type-key"
	agent, err := shm.AgentAttach(agentName, 0, datasize.MB*4)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	devicesBefore := shm.DPConfig(0).Devices()
	require.Len(t, devicesBefore, 1)
	require.Equal(t, "plain", devicesBefore[0].Type)
	require.Equal(t, "d0", devicesBefore[0].Name)
	plainConfig, err := cplain.NewDeviceConfig(agent, "d0", nil, nil)
	require.NoError(t, err)
	vlanConfig, err := cvlan.NewDeviceConfig(agent, "d0", nil, nil, 100)
	require.NoError(t, err)

	updateErr := agent.UpdateDevices([]ffi.ShmDeviceConfig{
		plainConfig.AsFFIDevice(),
		vlanConfig.AsFFIDevice(),
	})
	if updateErr != nil {
		plainConfig.Free()
		vlanConfig.Free()
	}
	require.Error(t, updateErr)

	require.Equal(t, devicesBefore, shm.DPConfig(0).Devices())
}
