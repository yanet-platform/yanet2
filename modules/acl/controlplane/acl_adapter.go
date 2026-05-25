package acl

import (
	"fmt"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	fwstate "github.com/yanet-platform/yanet2/modules/fwstate/controlplane"
)

var _ fwstate.ACLServiceProvider = (*ACLAdapter)(nil)

// ACLAdapter provides an interface for fwstate module to interact with ACL service.
// It implements the ACLServiceProvider interface required by fwstate module.
type ACLAdapter struct {
	service *ACLService
}

// NewACLAdapter creates a new adapter for fwstate integration.
func NewACLAdapter(service *ACLService) *ACLAdapter {
	return &ACLAdapter{
		service: service,
	}
}

// LinkedConfigNames returns ACL config names linked to the given fwstate
// config name.
func (m *ACLAdapter) LinkedConfigNames(fwstateConfigName string) []string {
	m.service.mu.Lock()
	defer m.service.mu.Unlock()

	return m.service.linkedConfigNamesLocked(fwstateConfigName)
}

// RelinkConfigs creates new ACL module configs for every name currently
// linked to fwstateConfig.
//
// Calls publish to push the combined dataplane update.
func (m *ACLAdapter) RelinkConfigs(
	fwstateConfig *fwstate.FwStateConfig,
	publish func(linkedFFI []ffi.ModuleConfig) error,
) error {
	m.service.mu.Lock()
	defer m.service.mu.Unlock()

	names := m.service.linkedConfigNamesLocked(fwstateConfig.Name())
	if len(names) == 0 {
		return publish(nil)
	}

	newHandles, err := m.service.createLinkedHandlesLocked(names, fwstateConfig)
	if err != nil {
		return err
	}

	ffiCfgs := make([]ffi.ModuleConfig, 0, len(names))
	for _, name := range names {
		ffiCfgs = append(ffiCfgs, newHandles[name].AsFFIModule())
	}

	if err := publish(ffiCfgs); err != nil {
		for _, h := range newHandles {
			h.Free()
		}

		return err
	}

	for name, newHandle := range newHandles {
		oldConfig := m.service.configs[name]
		if oldConfig.acl != nil {
			oldConfig.acl.Free()
		}

		m.service.configs[name] = aclConfig{
			rules:       oldConfig.rules,
			acl:         newHandle,
			fwstateName: fwstateConfig.Name(),
		}
	}

	return nil
}

// LinkConfigs creates new ACL module configs for the given explicit list of
// names, linking them to fwstateConfig, then calls publish so the caller can
// push the combined dataplane update atomically.
func (m *ACLAdapter) LinkConfigs(
	names []string,
	fwstateConfig *fwstate.FwStateConfig,
	publish func(linkedFFI []ffi.ModuleConfig) error,
) error {
	m.service.mu.Lock()
	defer m.service.mu.Unlock()

	newHandles, err := m.service.createLinkedHandlesLocked(names, fwstateConfig)
	if err != nil {
		return err
	}

	ffiCfgs := make([]ffi.ModuleConfig, 0, len(names))
	for _, name := range names {
		ffiCfgs = append(ffiCfgs, newHandles[name].AsFFIModule())
	}

	if err := publish(ffiCfgs); err != nil {
		for _, h := range newHandles {
			h.Free()
		}

		return err
	}

	for name, newHandle := range newHandles {
		oldConfig := m.service.configs[name]
		if oldConfig.acl != nil {
			oldConfig.acl.Free()
		}

		m.service.configs[name] = aclConfig{
			rules:       oldConfig.rules,
			acl:         newHandle,
			fwstateName: fwstateConfig.Name(),
		}
	}

	return nil
}

// linkedConfigNamesLocked returns linked names without taking the mutex.
//
// Caller must hold m.mu.
func (m *ACLService) linkedConfigNamesLocked(fwstateConfigName string) []string {
	names := make([]string, 0)
	for name, config := range m.configs {
		if config.fwstateName == fwstateConfigName {
			names = append(names, name)
		}
	}

	return names
}

// createLinkedHandlesLocked creates new ACL module handles for every name in
// names, linked to fwstateConfig. On any failure every handle created so far
// is freed before returning the error.
//
// Caller must hold m.mu.
func (m *ACLService) createLinkedHandlesLocked(
	names []string,
	fwstateConfig *fwstate.FwStateConfig,
) (map[string]ModuleHandle, error) {
	newHandles := make(map[string]ModuleHandle)

	for _, name := range names {
		oldConfig, ok := m.configs[name]
		if !ok {
			for _, h := range newHandles {
				h.Free()
			}

			return nil, fmt.Errorf("ACL config %q not found", name)
		}

		handle, err := m.backend.NewModule(name)
		if err != nil {
			for _, h := range newHandles {
				h.Free()
			}

			return nil, fmt.Errorf("failed to create ACL module config %q: %w", name, err)
		}

		handle.SetFwStateConfig(fwstateConfig.AsFFIModule())

		rules, err := convertRules(oldConfig.rules)
		if err != nil {
			handle.Free()
			for _, h := range newHandles {
				h.Free()
			}

			return nil, fmt.Errorf("failed to convert rules for ACL config %q: %w", name, err)
		}

		if err := handle.UpdateRules(rules); err != nil {
			handle.Free()
			for _, h := range newHandles {
				h.Free()
			}

			return nil, fmt.Errorf("failed to update ACL module config %q: %w", name, err)
		}

		newHandles[name] = handle
	}

	m.log.Info(
		"successfully created ACL configs",
		zap.Strings("acl_configs", names),
		zap.String("fwstate", fwstateConfig.Name()),
	)

	return newHandles, nil
}
