package vlan

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/vlan/bindings/go/cvlan"
)

// backend is the real Backend implementation backed by shared memory.
type backend struct {
	agent *ffi.Agent
}

// NewBackend creates a Backend that operates on real shared memory.
func NewBackend(agent *ffi.Agent) Backend {
	return &backend{
		agent: agent,
	}
}

func (m *backend) UpdateDevice(
	name string,
	input []Pipeline,
	output []Pipeline,
	vlanID uint16,
) (*cvlan.DeviceConfig, error) {
	device, err := cvlan.NewDeviceConfig(
		m.agent,
		name,
		toBindingPipelines(input),
		toBindingPipelines(output),
		vlanID,
	)
	if err != nil {
		return nil, err
	}

	if err := m.agent.UpdateDevices(
		[]ffi.ShmDeviceConfig{device.AsFFIDevice()},
	); err != nil {
		if err := device.Free(); err != nil {
			return nil, fmt.Errorf("failed to free abandoned config: %w", err)
		}
		return nil, err
	}

	return device, nil
}

func (m *backend) DeleteDevice(name string) error {
	return m.agent.DeleteDevice(name)
}

func (m *backend) Device(name string) (ffi.DeviceInfo, bool) {
	return m.agent.DPConfig().Device("vlan", name)
}

func (m *backend) LookupVlan(name string) (uint16, error) {
	return cvlan.LookupVlan(m.agent, name)
}

// toBindingPipelines converts service pipeline assignments into the binding
// representation consumed by the C API.
func toBindingPipelines(pipelines []Pipeline) []cvlan.Pipeline {
	out := make([]cvlan.Pipeline, 0, len(pipelines))
	for idx := range pipelines {
		out = append(out, cvlan.Pipeline{
			Name:   pipelines[idx].Name,
			Weight: pipelines[idx].Weight,
		})
	}
	return out
}
