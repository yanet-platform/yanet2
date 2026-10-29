package dscp

import (
	"fmt"
	"slices"

	"github.com/yanet-platform/yanet2/controlplane/crud"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/dscp/bindings/go/cdscp"
)

// moduleType identifies the dscp module to the shared-memory agent.
const moduleType = "dscp"

// backend is the real Backend implementation backed by shared memory.
type backend struct {
	agent *ffi.Agent
}

// newBackend creates a Backend that operates on real shared memory.
func newBackend(agent *ffi.Agent) *backend {
	return &backend{
		agent: agent,
	}
}

func (m *backend) Update(name string, payload *config) (crud.Handle, error) {
	module, err := cdscp.NewModuleConfig(m.agent, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)
	}

	build := func(module *cdscp.ModuleConfig) error {
		for _, prefix := range slices.Concat(payload.Prefixes4, payload.Prefixes6) {
			if err := module.PrefixAdd(prefix); err != nil {
				return fmt.Errorf("failed to add prefix: %w", err)
			}
		}
		if err := module.SetDscpMarking(payload.Config.Flag, payload.Config.Mark); err != nil {
			return fmt.Errorf("failed to set DSCP marking: %w", err)
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
