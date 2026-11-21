package nat64s

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/crud"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/nat64s/bindings/go/cnat64s"
)

var _ crud.Handle = (*cnat64s.ModuleConfig)(nil)

// Backend abstracts shared-memory operations.
type Backend interface {
	// Update builds a module config from the decoded one and publishes
	// it to the dataplane.
	Update(name string, config *NAT64sConfig) (crud.Handle, error)
	// Delete removes a module config.
	Delete(name string) error
}

type backend struct {
	agent *ffi.Agent
	maps  *mapRegistry
}

// NewBackend creates a Backend that operates on real shared memory,
// creating the session tables the config names on first reference.
func NewBackend(agent *ffi.Agent, maps *mapRegistry) Backend {
	return &backend{
		agent: agent,
		maps:  maps,
	}
}

func (m *backend) Update(name string, config *NAT64sConfig) (crud.Handle, error) {
	// The linked map must exist before the module config that links it
	// publishes, or building its execution context would fail.
	// Acquire the new map reference before publishing, and release the
	// config's previous one afterwards: the object is reclaimed when
	// its last user goes away.
	oldMap := m.maps.MapOf(name)
	if config.MapName != "" {
		if err := m.maps.Acquire(name, config.MapName); err != nil {
			return nil, fmt.Errorf("failed to ensure the session map: %w", err)
		}
	}
	if oldMap != "" && oldMap != config.MapName {
		m.maps.Release(name, oldMap)
	}

	module, err := cnat64s.NewModuleConfig(m.agent, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)
	}

	update := cnat64s.ModuleUpdate{
		Prefixes: config.Prefixes,
		Timeout:  cnat64s.SessionTimeout(config.Timeout),
		MTUv4:    uint16(config.MTU.IPv4MTU),
		MTUv6:    uint16(config.MTU.IPv6MTU),
		PortLo:   config.PortRange.Lo,
		PortHi:   config.PortRange.Hi,
		MapName:  config.MapName,
	}
	// The wire pool addresses are already network-order uint32 values.
	update.Pool = config.Pool[:]

	build := func(module *cnat64s.ModuleConfig) error {
		if err := module.Update(update); err != nil {
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
	if err := m.agent.DeleteModuleConfig(moduleType, name); err != nil {
		return err
	}
	if mapName := m.maps.MapOf(name); mapName != "" {
		m.maps.Release(name, mapName)
	}
	return nil
}
