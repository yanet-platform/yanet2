package unrdup

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/crud"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/unrdup/bindings/go/cunrdup"
)

// moduleType identifies the unrdup module to the shared-memory agent.
const moduleType = "unrdup"

type backend struct {
	agent *ffi.Agent
}

func newBackend(agent *ffi.Agent) *backend {
	return &backend{
		agent: agent,
	}
}

func (m *backend) Update(name string, payload *ModuleConfig) (crud.Handle, error) {
	module, err := cunrdup.NewModuleConfig(m.agent, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)
	}

	build := func(module *cunrdup.ModuleConfig) error {
		for _, source := range payload.Sources() {
			if err := module.SetSource(source); err != nil {
				return fmt.Errorf("failed to set source: %w", err)
			}
		}
		if err := module.UpdateServices(payload.Services); err != nil {
			return fmt.Errorf("failed to update services: %w", err)
		}
		return nil
	}
	if err := crud.PublishModule(m.agent, module, build); err != nil {
		return nil, err
	}

	return module, nil
}

func (m *backend) Delete(name string) error {
	return m.agent.DeleteModuleConfig(moduleType, name)
}
