package decap

import (
	"fmt"
	"net/netip"

	cpffi "github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/decap/internal/ffi"
)

// backend is the real Backend implementation backed by shared memory.
type backend struct {
	agent *cpffi.Agent
}

// NewBackend creates a Backend that operates on real shared memory.
func NewBackend(agent *cpffi.Agent) Backend {
	return &backend{
		agent: agent,
	}
}

func (m *backend) UpdateModule(name string, prefixes []netip.Prefix) (ModuleHandle, error) {
	mod, err := ffi.NewModuleConfig(m.agent, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)
	}

	for _, prefix := range prefixes {
		if err := mod.PrefixAdd(prefix); err != nil {
			mod.Free()
			return nil, fmt.Errorf("failed to add prefix: %w", err)
		}
	}

	if err := m.agent.UpdateModules(
		[]cpffi.ModuleConfig{mod.AsFFIModule()},
	); err != nil {
		mod.Free()
		return nil, fmt.Errorf("failed to update module: %w", err)
	}

	return mod, nil
}
