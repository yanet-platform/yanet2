//go:build linux && (amd64 || arm64)

package cpconfig_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// Test_NewPlainDevice_RefusesNameItsFieldCannotHold verifies that a
// device or device pipeline name C would cut short is refused before the
// agent is touched.
//
// The owner has no agent, so any allocation from it would fault.
func Test_NewPlainDevice_RefusesNameItsFieldCannotHold(t *testing.T) {
	type invalidDevice struct {
		name   string
		config cpconfig.DeviceConfig
	}
	var cases []invalidDevice
	for _, invalid := range invalidNames(len(layout.CpDevice{}.Name)) {
		cases = append(cases, invalidDevice{"device: " + invalid.name, cpconfig.DeviceConfig{Name: invalid.value}})
	}
	for _, invalid := range invalidNames(len(layout.CpDevicePipeline{}.Name)) {
		pipelines := []cpconfig.DevicePipelineConfig{{Name: invalid.value, Weight: 1}}
		cases = append(cases,
			invalidDevice{"input pipeline: " + invalid.name, cpconfig.DeviceConfig{Name: "port0", Input: pipelines}},
			invalidDevice{"output pipeline: " + invalid.name, cpconfig.DeviceConfig{Name: "port0", Output: pipelines}},
		)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cpconfig.NewPlainDevice(&agent.Agent{}, tc.config)
			require.ErrorIs(t, err, cpconfig.ErrInvalidName)
		})
	}
}

// Test_InitDevice_RefusesInvalidInput verifies that a device type its
// field cannot hold whole and a pipeline weighing past the bound are
// refused before the header is touched.
//
// The owner has no instance, so a type lookup would fault.
func Test_InitDevice_RefusesInvalidInput(t *testing.T) {
	cases := []struct {
		name       string
		deviceType string
		config     cpconfig.DeviceConfig
	}{
		{"type with an inner NUL", "plain\x00other", cpconfig.DeviceConfig{Name: "port0"}},
		{"input pipeline past the weight bound", "plain", cpconfig.DeviceConfig{
			Name:  "port0",
			Input: []cpconfig.DevicePipelineConfig{{Name: "drop", Weight: 65536}},
		}},
		{"output pipeline past the weight bound", "plain", cpconfig.DeviceConfig{
			Name:   "port0",
			Output: []cpconfig.DevicePipelineConfig{{Name: "drop", Weight: 65536}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			device := layout.CpDevice{DpDeviceIdx: 1}

			err := cpconfig.InitDevice(&device, &agent.Agent{}, tc.deviceType, tc.config)

			require.Error(t, err)
			require.Equal(t, uint64(1), device.DpDeviceIdx, "the header is untouched")
		})
	}
}

// Test_UpdateDevices_RefusesNilDevice verifies that an update holding a
// nil device is refused before the instance is touched.
//
// The instance has no zones, so any access to them would fault.
func Test_UpdateDevices_RefusesNilDevice(t *testing.T) {
	err := cpconfig.UpdateDevices(&zone.Instance{}, []*layout.CpDevice{{}, nil})

	require.Error(t, err)
}
