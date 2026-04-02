package forward

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
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

func (m *backend) UpdateModule(name string, rules []cforward.ForwardRule) (ModuleHandle, error) {
	module, err := cforward.NewModuleConfig(m.agent, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)
	}

	if err := module.Update(rules); err != nil {
		module.Free()
		return nil, fmt.Errorf("failed to update module config: %w", err)
	}

	if err := m.agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}); err != nil {
		module.Free()
		return nil, fmt.Errorf("failed to update module: %w", err)
	}

	return module, nil
}

func (m *backend) DeleteModule(name string) error {
	return m.agent.DeleteModuleConfig(name)
}
