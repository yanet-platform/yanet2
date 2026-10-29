package dataplaneut_test

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/plain/bindings/go/cplain"
)

// Test_LoadedEntityCount_CountsPublishedEntity verifies that an agent that
// published nothing holds no loaded entity, and that publishing a device makes
// the live generation hold one.
func Test_LoadedEntityCount_CountsPublishedEntity(t *testing.T) {
	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(32 * datasize.MB),
		DPMemory:      uint64(datasize.MB),
		WorkerCount:   1,
		Devices:       []string{"port0"},
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)
	agent, err := harness.SharedMemory().AgentAttach("loaded", 0, 4*datasize.MB)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, agent.CleanUp()) })
	require.Zero(t, dataplaneut.LoadedEntityCount(agent))

	devices, err := cplain.UpdateDevices(agent, []ffi.DeviceConfig{{Name: "port0"}})
	require.NoError(t, err)
	t.Cleanup(func() {
		for _, device := range devices {
			_ = device.Free()
		}
	})

	require.Equal(t, uint64(1), dataplaneut.LoadedEntityCount(agent))
}
