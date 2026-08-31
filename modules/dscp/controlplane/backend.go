package dscp

import (
	"fmt"
	"net/netip"

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

func (m *backend) UpdateModule(
	name string,
	prefixes []netip.Prefix,
	flag uint8,
	mark uint8,
) (ModuleHandle, error) {
	module, err := cdscp.NewModuleConfig(m.agent, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)
	}

	for _, prefix := range prefixes {
		if err := module.PrefixAdd(prefix); err != nil {
			if err := module.Free(); err != nil {
				return nil, fmt.Errorf("failed to free abandoned config: %w", err)
			}
			return nil, fmt.Errorf("failed to add prefix: %w", err)
		}
	}

	if err := module.SetDscpMarking(flag, mark); err != nil {
		if err := module.Free(); err != nil {
			return nil, fmt.Errorf("failed to free abandoned config: %w", err)
		}
		return nil, fmt.Errorf("failed to set DSCP marking: %w", err)
	}

	if err := m.agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}); err != nil {
		if err := module.Free(); err != nil {
			return nil, fmt.Errorf("failed to free abandoned config: %w", err)
		}
		return nil, fmt.Errorf("failed to update module: %w", err)
	}

	return module, nil
}

func (m *backend) DeleteModule(name string) error {
	return m.agent.DeleteModuleConfig(moduleType, name)
}
