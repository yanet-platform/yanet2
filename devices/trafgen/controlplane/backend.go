package trafgen

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/trafgen/bindings/go/ctrafgen"
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
	frames []byte,
	lengths []uint32,
	ratePps uint64,
) error {
	// Reclaim devices parked on the agent's unused list, whatever the
	// outcome of this update.
	//
	// A device leaving the config generation is parked rather than freed:
	// both the one replaced by a successful update and the new one rejected
	// mid-install by a failed update land on the unused list. The drain is
	// registered before the device config is created on purpose: once the
	// arena is full, creation fails first, so a drain registered after that
	// check would never run and the arena could never recover.
	defer ctrafgen.DrainUnusedDevices(m.agent)

	device, err := ctrafgen.NewDeviceConfig(
		m.agent,
		name,
		toBindingPipelines(input),
		toBindingPipelines(output),
		frames,
		lengths,
		ratePps,
	)
	if err != nil {
		return fmt.Errorf("failed to create device config: %w", err)
	}

	if err := m.agent.UpdateDevices(
		[]ffi.ShmDeviceConfig{device.AsFFIDevice()},
	); err != nil {
		return fmt.Errorf("failed to update device %q: %w", name, err)
	}

	return nil
}

// toBindingPipelines converts service pipeline assignments into the binding
// representation consumed by the C API.
func toBindingPipelines(pipelines []Pipeline) []ctrafgen.Pipeline {
	out := make([]ctrafgen.Pipeline, 0, len(pipelines))
	for idx := range pipelines {
		out = append(out, ctrafgen.Pipeline{
			Name:   pipelines[idx].Name,
			Weight: pipelines[idx].Weight,
		})
	}
	return out
}
