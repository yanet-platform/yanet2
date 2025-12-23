package acl

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	fwstate "github.com/yanet-platform/yanet2/modules/fwstate/controlplane"
)

// Compile-time check to ensure ACLAdapter implements fwstate.ACLServiceProvider interface
var _ fwstate.ACLServiceProvider = (*ACLAdapter)(nil)

// ACLAdapter provides an interface for fwstate module to interact with ACL service.
// It implements the ACLServiceProvider interface required by fwstate module.
type ACLAdapter struct {
	service *ACLService
}

// NewACLAdapter creates a new adapter for fwstate integration
func NewACLAdapter(service *ACLService) *ACLAdapter {
	return &ACLAdapter{
		service: service,
	}
}

// Lock locks the ACL service mutex for external synchronization
func (a *ACLAdapter) Lock() {
	a.service.lock()
}

// Unlock unlocks the ACL service mutex for external synchronization
func (a *ACLAdapter) Unlock() {
	a.service.unlock()
}

// LinkedConfigNames returns the names of ACL configs that are linked to the specified fwstate config
func (a *ACLAdapter) LinkedConfigNames(fwstateConfigName string) []string {
	return a.service.linkedConfigNames(fwstateConfigName)
}

// CreateACLConfigs creates new ACL module configs for the specified ACL config names
// and links them to the provided fwstate config. Returns the newly created ACL configs
// as FFI modules and a transaction object for commit/abort.
func (a *ACLAdapter) CreateACLConfigs(aclConfigNames []string, fwstateConfig *fwstate.FwStateConfig) ([]ffi.ModuleConfig, fwstate.ACLConfigTransaction, error) {
	return a.service.createACLConfigs(aclConfigNames, fwstateConfig)
}

// //////////////////////////////////////////////////////////////////////////////
// ACLService private methods for adapter

// lock locks the service mutex for external synchronization
func (m *ACLService) lock() {
	m.mu.Lock()
}

// unlock unlocks the service mutex for external synchronization
func (m *ACLService) unlock() {
	m.mu.Unlock()
}

// linkedConfigNames returns the names of ACL configs that are linked to the specified fwstate config
func (m *ACLService) linkedConfigNames(fwstateConfigName string) []string {
	names := make([]string, 0)
	for name, config := range m.configs {
		if config.fwstateName == fwstateConfigName {
			names = append(names, name)
		}
	}
	return names
}

// aclConfigTransaction implements transaction pattern for ACL config updates
type aclConfigTransaction struct {
	service     *ACLService
	newConfigs  map[string]*ModuleConfig
	fwstateName string
}

func (t *aclConfigTransaction) Commit() {
	// Free old configs and save new ones
	for name, newConfig := range t.newConfigs {
		oldConfig := t.service.configs[name]
		if oldConfig.acl != nil {
			oldConfig.acl.Free()
		}

		t.service.configs[name] = aclConfig{
			rules:       oldConfig.rules,
			acl:         newConfig,
			fwstateName: t.fwstateName,
		}
	}
}

func (t *aclConfigTransaction) Abort() {
	// Free new configs, keep old ones
	for _, newConfig := range t.newConfigs {
		if newConfig != nil {
			newConfig.Free()
		}
	}
}

// createACLConfigs creates new ACL module configs for the specified ACL config names
// and links them to the provided fwstate config. Returns the newly created ACL configs
// as FFI modules and a transaction object for commit/abort.
func (m *ACLService) createACLConfigs(aclConfigNames []string, fwstateConfig *fwstate.FwStateConfig) ([]ffi.ModuleConfig, fwstate.ACLConfigTransaction, error) {
	newConfigs := make(map[string]*ModuleConfig)

	// Create all new configs
	for _, name := range aclConfigNames {
		oldConfig, ok := m.configs[name]
		if !ok {
			// Clean up any configs we've already created
			for _, cfg := range newConfigs {
				cfg.Free()
			}
			return nil, nil, fmt.Errorf("ACL config %q not found", name)
		}

		// Create new ACL config
		newACLConfig, err := NewModuleConfig(m.agent, name)
		if err != nil {
			// Clean up any configs we've already created
			for _, cfg := range newConfigs {
				cfg.Free()
			}
			return nil, nil, fmt.Errorf("failed to create ACL module config %q: %w", name, err)
		}

		newACLConfig.SetFwStateConfig(fwstateConfig)

		// Convert and update rules
		rules, err := convertRules(oldConfig.rules)
		if err != nil {
			newACLConfig.Free()
			// Clean up any configs we've already created
			for _, cfg := range newConfigs {
				cfg.Free()
			}
			return nil, nil, fmt.Errorf("failed to convert rules for ACL config %q: %w", name, err)
		}

		if err := newACLConfig.Update(rules); err != nil {
			newACLConfig.Free()
			// Clean up any configs we've already created
			for _, cfg := range newConfigs {
				cfg.Free()
			}
			return nil, nil, fmt.Errorf("failed to update ACL module config %q: %w", name, err)
		}

		newConfigs[name] = newACLConfig
	}

	// Create transaction
	transaction := &aclConfigTransaction{
		service:     m,
		newConfigs:  newConfigs,
		fwstateName: fwstateConfig.Name(),
	}

	// Convert to FFI modules
	ffiConfigs := make([]ffi.ModuleConfig, 0, len(aclConfigNames))
	for _, name := range aclConfigNames {
		ffiConfigs = append(ffiConfigs, newConfigs[name].AsFFIModule())
	}

	return ffiConfigs, transaction, nil
}
