package plain

import (
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/crud"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/plain/bindings/go/cplain"
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

func (m *backend) Update(name string, payload DevicePipelines) (crud.Handle, error) {
	device, err := cplain.NewDeviceConfig(
		m.agent,
		name,
		toBindingPipelines(payload.Input),
		toBindingPipelines(payload.Output),
	)
	if err != nil {
		return nil, err
	}

	if err := crud.PublishDevice(m.agent, device); err != nil {
		return nil, err
	}

	return device, nil
}

func (m *backend) Delete(name string) error {
	return m.agent.DeleteDevice(name)
}

func (m *backend) Device(name string) (ffi.DeviceInfo, bool) {
	return m.agent.DPConfig().Device("plain", name)
}

// toBindingPipelines converts service pipeline assignments into the binding
// representation consumed by the C API.
func toBindingPipelines(pipelines []commonpb.Pipeline) []cplain.Pipeline {
	out := make([]cplain.Pipeline, 0, len(pipelines))
	for idx := range pipelines {
		out = append(out, cplain.Pipeline{
			Name:   pipelines[idx].Name,
			Weight: pipelines[idx].Weight,
		})
	}
	return out
}
