package blackhole

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/crud"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/blackhole/bindings/go/cblackhole"
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

func (m *backend) Update(name string, _ struct{}) (crud.Handle, error) {
	module, err := cblackhole.NewModuleConfig(m.agent, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)
	}

	if err := crud.PublishModule(m.agent, module, nil); err != nil {
		return nil, err
	}

	return module, nil
}

func (m *backend) Delete(name string) error {
	return m.agent.DeleteModuleConfig(moduleName, name)
}
