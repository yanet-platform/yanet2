package route_mpls

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/route-mpls/bindings/go/croutempls"
)

// ModuleHandle is a handle to a route-mpls module configuration in shared
// memory.
type ModuleHandle interface {
	Free()
}

// Compile-time assertion that *croutempls.ModuleConfig satisfies the
// ModuleHandle interface; catches drift in the bindings layer.
var _ ModuleHandle = (*croutempls.ModuleConfig)(nil)

// Backend abstracts shared memory write-path operations for the route-mpls
// module.
type Backend interface {
	// UpdateModule builds a fresh ModuleConfig from the supplied rules and
	// publishes it to the dataplane atomically.
	UpdateModule(name string, rules []croutempls.Rule) (ModuleHandle, error)
	// DeleteModule removes a module config from the dataplane.
	DeleteModule(name string) error
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

func (m *backend) UpdateModule(name string, rules []croutempls.Rule) (ModuleHandle, error) {
	module, err := croutempls.NewModuleConfig(m.agent, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)
	}

	if err := module.Update(rules); err != nil {
		module.Free()
		return nil, fmt.Errorf("failed to update module config: %w", err)
	}

	if err := m.agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}); err != nil {
		module.Free()
		return nil, fmt.Errorf("failed to publish module config: %w", err)
	}

	return module, nil
}

func (m *backend) DeleteModule(name string) error {
	return m.agent.DeleteModuleConfig(name)
}
