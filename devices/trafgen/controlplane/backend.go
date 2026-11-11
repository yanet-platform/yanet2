package trafgen

import (
	"fmt"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/crud"
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

func (m *backend) Update(name string, payload *DeviceState) (crud.Handle, error) {
	frames, lengths := flattenFrames(payload.Packets)
	device, err := ctrafgen.NewDeviceConfig(
		m.agent,
		name,
		toBindingPipelines(payload.Input),
		toBindingPipelines(payload.Output),
		frames,
		lengths,
		payload.RatePps,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create device config: %w", err)
	}

	if err := crud.PublishDevice(m.agent, device); err != nil {
		return nil, err
	}

	return device, nil
}

func (m *backend) Delete(name string) error {
	return m.agent.DeleteDevice(name)
}

// flattenFrames concatenates the per-packet frames into a single buffer and
// returns it together with the per-frame lengths.
func flattenFrames(packets [][]byte) ([]byte, []uint32) {
	if len(packets) == 0 {
		return nil, nil
	}

	lengths := make([]uint32, len(packets))
	total := 0
	for idx, packet := range packets {
		lengths[idx] = uint32(len(packet))
		total += len(packet)
	}

	frames := make([]byte, 0, total)
	for _, packet := range packets {
		frames = append(frames, packet...)
	}

	return frames, lengths
}

// toBindingPipelines converts service pipeline assignments into the binding
// representation consumed by the C API.
func toBindingPipelines(pipelines []commonpb.Pipeline) []ctrafgen.Pipeline {
	out := make([]ctrafgen.Pipeline, 0, len(pipelines))
	for idx := range pipelines {
		out = append(out, ctrafgen.Pipeline{
			Name:   pipelines[idx].Name,
			Weight: pipelines[idx].Weight,
		})
	}
	return out
}
