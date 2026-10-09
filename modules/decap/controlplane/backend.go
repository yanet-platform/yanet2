package decap

import (
	"fmt"
	"net/netip"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// moduleType identifies the decap module to the shared-memory agent.
const moduleType = "decap"

// moduleConfig is a decap configuration built in shared memory and not
// published yet.
//
// The C api builds it by default; the yanet_rust_cp build tag switches to
// the Rust api, whose layout only the Rust dataplane module reads.
type moduleConfig interface {
	ModuleHandle
	AsFFIModule() ffi.ModuleConfig
}

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
	mod, err := newModuleConfig(m.agent, name, prefixes)
	if err != nil {
		return nil, err
	}

	if err := m.agent.UpdateModules(
		[]ffi.ModuleConfig{mod.AsFFIModule()},
	); err != nil {
		if err := mod.Free(); err != nil {
			return nil, fmt.Errorf("failed to free abandoned config: %w", err)
		}
		return nil, fmt.Errorf("failed to update module %q: %w", name, err)
	}

	return mod, nil
}

func (m *backend) DeleteModule(name string) error {
	return m.agent.DeleteModuleConfig(moduleType, name)
}
