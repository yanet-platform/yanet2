package acl

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
)

// backend is the production Backend implementation backed by *ffi.Agent.
type backend struct {
	agent *ffi.Agent
}

// NewBackend creates a Backend that operates on real shared memory.
func NewBackend(agent *ffi.Agent) Backend {
	return &backend{agent: agent}
}

func (m *backend) NewModule(
	name string,
	rules []cacl.AclRule,
	fw4MapName, fw6MapName string,
) (ModuleHandle, error) {
	handle, err := cacl.NewModuleConfig(
		m.agent, name, rules, fw4MapName, fw6MapName,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)
	}

	return handle, nil
}

func (m *backend) UpdateModule(handle ModuleHandle) error {
	return m.agent.UpdateModules([]ffi.ModuleConfig{handle.AsFFIModule()})
}

func (m *backend) DeleteModule(name string) error {
	return m.agent.DeleteModuleConfig(moduleType, name)
}

func (m *backend) DPConfig() *ffi.DPConfig {
	return m.agent.DPConfig()
}
