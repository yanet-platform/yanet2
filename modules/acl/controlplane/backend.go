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

// Backend abstracts shared-memory operations for the ACL service.
type Backend interface {
	// NewModule allocates a new ACL module config in shared memory.
	// The returned handle is not yet published to the dataplane.
	NewModule(name string) (ModuleHandle, error)
	// UpdateModule publishes handle to dp_config_gen so the dataplane
	// picks it up on the next round.
	UpdateModule(handle ModuleHandle) error
	// UpdateModules atomically publishes a batch of module configs in a
	// single dp_config_gen generation bump.
	//
	// Used by republish paths that rebuild several handles at once (e.g.
	// fwstate-map layer insert). Single-handle updates use UpdateModule.
	UpdateModules(cfgs []ffi.ModuleConfig) error
	// DeleteModule removes a module config from the dataplane.
	DeleteModule(name string) error
	// TODO: remove this
	MemoryBytes() uint64
	// DPConfig returns the dataplane configuration handle for counter
	// and position queries.
	DPConfig() *ffi.DPConfig
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

func (m *backend) UpdateModules(cfgs []ffi.ModuleConfig) error {
	return m.agent.UpdateModules(cfgs)
}

func (m *backend) DeleteModule(name string) error {
	return m.agent.DeleteModule(moduleType, name)
}

func (m *backend) MemoryBytes() uint64 {
	return m.memoryBytes
}

func (m *backend) DPConfig() *ffi.DPConfig {
	return m.agent.DPConfig()
}
