package nat64

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/nat64/bindings/go/cnat64"
)

type ModuleHandle interface {
	Free()
}

var _ ModuleHandle = (*cnat64.ModuleConfig)(nil)

type Backend interface {
	UpdateModule(name string, config *NAT64Config) (ModuleHandle, error)
}

type backend struct {
	agent *ffi.Agent
}

func NewBackend(agent *ffi.Agent) Backend {
	return &backend{
		agent: agent,
	}
}

func (m *backend) UpdateModule(name string, config *NAT64Config) (ModuleHandle, error) {
	module, err := cnat64.NewModuleConfig(m.agent, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)
	}

	for _, prefix := range config.Prefixes {
		if err := module.AddPrefix(prefix); err != nil {
			module.Free()
			return nil, fmt.Errorf("failed to add prefix: %w", err)
		}
	}

	for _, mapping := range config.Mappings {
		if err := module.AddMapping(mapping.IPv4, mapping.IPv6, mapping.PrefixIndex); err != nil {
			module.Free()
			return nil, fmt.Errorf("failed to add mapping: %w", err)
		}
	}

	if err := module.SetDropUnknown(config.DropUnknownPrefix, config.DropUnknownMapping); err != nil {
		module.Free()
		return nil, fmt.Errorf("failed to set drop unknown flags: %w", err)
	}

	if err := module.SetMTU(config.MTU.IPv4MTU, config.MTU.IPv6MTU); err != nil {
		module.Free()
		return nil, fmt.Errorf("failed to set MTU: %w", err)
	}

	if err := m.agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}); err != nil {
		module.Free()
		return nil, fmt.Errorf("failed to update module: %w", err)
	}

	return module, nil
}
