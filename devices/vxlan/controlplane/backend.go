package vxlan

import (
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/crud"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/vxlan/bindings/go/cvxlan"
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

func (m *backend) Update(name string, payload DeviceState) (crud.Handle, error) {
	device, err := cvxlan.NewDeviceConfig(
		m.agent,
		name,
		toBindingPipelines(payload.Input),
		toBindingPipelines(payload.Output),
		payload.Settings,
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

// toBindingPipelines converts service pipeline assignments into the binding
// representation consumed by the C API.
func toBindingPipelines(pipelines []commonpb.Pipeline) []cvxlan.Pipeline {
	out := make([]cvxlan.Pipeline, 0, len(pipelines))
	for idx := range pipelines {
		out = append(out, cvxlan.Pipeline{
			Name:   pipelines[idx].Name,
			Weight: pipelines[idx].Weight,
		})
	}
	return out
}
