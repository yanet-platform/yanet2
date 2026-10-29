package decap

import (
	"fmt"
	"slices"

	"github.com/yanet-platform/yanet2/controlplane/crud"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/decap/bindings/go/cdecap"
)

// moduleType identifies the decap module to the shared-memory agent.
const moduleType = "decap"

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

func (m *backend) Update(name string, payload PrefixSets) (crud.Handle, error) {
	module, err := cdecap.NewModuleConfig(m.agent, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)
	}

	build := func(module *cdecap.ModuleConfig) error {
		for _, prefix := range slices.Concat(payload.Prefixes4, payload.Prefixes6) {
			if err := module.PrefixAdd(prefix); err != nil {
				return fmt.Errorf("failed to add prefix %v: %w", prefix, err)
			}
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
