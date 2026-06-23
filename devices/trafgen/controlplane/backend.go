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

	// Reclaim devices parked on the agent's unused list once the update
	// settles, regardless of outcome.
	//
	// A device leaving the config generation is parked rather than freed: the
	// one replaced by a successful update, or the new one rejected mid-install
	// by a failed update (e.g. an unknown pipeline), both land on the unused
	// list. Draining on every exit frees them and keeps repeated failed updates
	// from accumulating dead devices in the agent arena.
	defer ctrafgen.DrainUnusedDevices(m.agent)

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
