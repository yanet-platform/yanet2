package nat64

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/crud"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/nat64/bindings/go/cnat64"
)

var _ crud.Handle = (*cnat64.ModuleConfig)(nil)

type Backend interface {
	Update(name string, config *NAT64Config) (crud.Handle, error)
	// Delete removes a module config.
	Delete(name string) error
}

// moduleType identifies the nat64 module to the shared-memory agent.
const moduleType = "nat64"

type backend struct {
	agent *ffi.Agent
}

func NewBackend(agent *ffi.Agent) Backend {
	return &backend{
		agent: agent,
	}
}

func (m *backend) Update(name string, config *NAT64Config) (crud.Handle, error) {
	module, err := cnat64.NewModuleConfig(m.agent, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)
	}

	build := func(module *cnat64.ModuleConfig) error {
		for _, prefix := range config.Prefixes {
			if err := module.AddPrefix(prefix); err != nil {
				return fmt.Errorf("failed to add prefix: %w", err)
			}
		}
		for _, mapping := range config.Mappings {
			if err := module.AddMapping(mapping.IPv4, mapping.IPv6, mapping.PrefixIndex); err != nil {
				return fmt.Errorf("failed to add mapping: %w", err)
			}
		}
		if err := module.SetDropUnknown(config.DropUnknownPrefix, config.DropUnknownMapping); err != nil {
			return fmt.Errorf("failed to set drop unknown flags: %w", err)
		}
		if err := module.SetMTU(config.MTU.IPv4MTU, config.MTU.IPv6MTU); err != nil {
			return fmt.Errorf("failed to set MTU: %w", err)
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
