package route_mpls

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/crud"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/route-mpls/bindings/go/croutempls"
)

// Compile-time assertion that *croutempls.ModuleConfig satisfies the
// crud.Handle interface. Catches drift in the bindings layer.
var _ crud.Handle = (*croutempls.ModuleConfig)(nil)

// Backend abstracts shared memory write-path operations for the route-mpls
// module.
type Backend interface {
	// Update builds a fresh module config from the payload's rules and
	// publishes it to the dataplane atomically.
	Update(name string, payload *routeMPLSConfig) (crud.Handle, error)
	// Delete removes a module config from the dataplane.
	Delete(name string) error
}

// backend is the real Backend implementation backed by shared memory.
type backend struct {
	agent *ffi.Agent
}

// NewBackend creates a backend that operates on real shared memory.
func NewBackend(agent *ffi.Agent) *backend {
	return &backend{
		agent: agent,
	}
}

// Update builds the payload's rules and publishes them.
func (m *backend) Update(name string, payload *routeMPLSConfig) (crud.Handle, error) {
	return m.UpdateModule(name, payload.BuildRules())
}

// UpdateModule builds a fresh module config from the supplied rules and
// publishes it to the dataplane atomically, the raw publish seam of
// callers that bypass the payload conversion.
func (m *backend) UpdateModule(name string, rules []croutempls.Rule) (crud.Handle, error) {
	module, err := croutempls.NewModuleConfig(m.agent, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)
	}

	build := func(module *croutempls.ModuleConfig) error {
		if err := module.Update(rules); err != nil {
			return fmt.Errorf("failed to update module config: %w", err)
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
