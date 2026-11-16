package cplain_test

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/devices/plain/bindings/go/cplain"
)

// Test_NewDeviceConfig_RejectsOverBoundPipelineWeight verifies that a
// pipeline weight above the documented bound is rejected at the device
// config setter instead of silently reaching shared memory.
func Test_NewDeviceConfig_RejectsOverBoundPipelineWeight(t *testing.T) {
	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(datasize.MB * 32),
		DPMemory:      uint64(datasize.MB * 4),
		WorkerCount:   1,
		Devices:       []string{"d0"},
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	agent, err := harness.SharedMemory().AgentAttach("plain", 0, datasize.MB*4)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	device, err := cplain.NewDeviceConfig(agent, "ok", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = device.Free() })

	for _, tc := range []struct {
		name   string
		device string
		input  []cplain.Pipeline
		output []cplain.Pipeline
	}{
		{
			name:   "input weight above the bound",
			device: "bad-input",
			input:  []cplain.Pipeline{{Name: "p0", Weight: 1 << 63}},
		},
		{
			name:   "output weight above the bound",
			device: "bad-output",
			output: []cplain.Pipeline{{Name: "p0", Weight: 1 << 63}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cplain.NewDeviceConfig(agent, tc.device, tc.input, tc.output)
			require.Error(t, err)
		})
	}
}
