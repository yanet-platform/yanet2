package decap

import (
	"fmt"
	"net/netip"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/decap/bindings/go/cdecap"
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

func (m *backend) UpdateModule(name string, prefixes []netip.Prefix) (ModuleHandle, error) {
	mod, err := cdecap.NewModuleConfig(m.agent, name)
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
		[]ffi.ModuleConfig{mod.AsFFIModule()},
	); err != nil {
		mod.Free()
		return nil, fmt.Errorf("failed to update module: %w", err)
	}

	return mod, nil
}
