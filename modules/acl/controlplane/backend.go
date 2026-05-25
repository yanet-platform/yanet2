package acl

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
)

// backend is the production Backend implementation backed by *ffi.Agent.
type backend struct {
	agent       *ffi.Agent
	memoryBytes uint64
}

// NewBackend creates a Backend that operates on real shared memory.
func NewBackend(agent *ffi.Agent, memoryBytes uint64) Backend {
	return &backend{
		agent:       agent,
		memoryBytes: memoryBytes,
	}
}

func (m *backend) NewModule(name string) (ModuleHandle, error) {
	handle, err := cacl.NewModuleConfig(m.agent, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)
	}

	return handle, nil
}

func (m *backend) UpdateModule(handle ModuleHandle) error {
	return m.agent.UpdateModules([]ffi.ModuleConfig{handle.AsFFIModule()})
}

func (m *backend) DeleteModule(name string) error {
	return m.agent.DeleteModuleConfig(name)
}

func (m *backend) MemoryBytes() uint64 {
	return m.memoryBytes
}

func (m *backend) DPConfig() *ffi.DPConfig {
	return m.agent.DPConfig()
}
