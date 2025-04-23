package registry

import (
	"context"
	"sync"
)

type RegisterEvent struct {
	Name   string
	Module Module
}

// Module represents a registered module.
type Module interface {
	SetupConfig(ctx context.Context, numaIdx uint32, configName string, config []byte) error
}

// Registry keeps track of all registered modules.
type Registry struct {
	mu      sync.RWMutex
	modules map[string]Module
	tx      chan<- RegisterEvent
}

// New creates a new module registry.
func New(tx chan<- RegisterEvent) *Registry {
	return &Registry{
		modules: map[string]Module{},
		tx:      tx,
	}
}

// RegisterModule registers a module with the given name.
func (m *Registry) RegisterModule(name string, module Module) {
	m.mu.Lock()
	m.modules[name] = module
	m.mu.Unlock()

	m.tx <- RegisterEvent{
		Name:   name,
		Module: module,
	}
}

// GetModule returns a module by name.
func (m *Registry) GetModule(name string) (Module, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	module, ok := m.modules[name]
	return module, ok
}

// ListModules returns a list of all registered module names.
func (m *Registry) ListModules() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.modules))
	for name := range m.modules {
		names = append(names, name)
	}

	return names
}
