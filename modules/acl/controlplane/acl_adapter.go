package acl

import (
	"fmt"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	aclpb "github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb/v1"
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
	return m.service.linkedConfigNames(fwstateConfigName)
}

// RelinkConfigs creates new ACL module configs for every name currently
// linked to fwstateConfig.
//
// Calls publish to push the combined dataplane update. Every linked name's
// lock is held for the whole rebuild (including the compile), so a read on
// one of those names still returns instantly under mu.RLock, and an
// UpdateConfig/DeleteConfig on one of those names waits behind this call
// instead of racing it.
func (m *ACLAdapter) RelinkConfigs(
	fwstateConfig *fwstate.FwStateConfig,
	publish func(linkedFFI []ffi.ModuleConfig) error,
) error {
	names := m.service.linkedConfigNames(fwstateConfig.Name())
	if len(names) == 0 {
		return publish(nil)
	}

	return m.service.withEntries(names, func(entries map[string]*configEntry) error {
		// A Delete or an UpdateConfig that lost the race for one of these
		// name locks may have run to completion before we got them, so the
		// snapshot above can no longer be trusted. Keep only names still
		// linked.
		linked := m.service.stillLinked(names, entries, fwstateConfig.Name())
		if len(linked) == 0 {
			return publish(nil)
		}

		newHandles, err := m.service.createLinkedHandles(linked, entries, fwstateConfig)
		if err != nil {
			return err
		}

		ffiCfgs := make([]ffi.ModuleConfig, 0, len(newHandles))
		for _, h := range newHandles {
			ffiCfgs = append(ffiCfgs, h.AsFFIModule())
		}

		if err := publish(ffiCfgs); err != nil {
			for _, h := range newHandles {
				h.Free()
			}

			return err
		}

		m.service.swapLinkedHandles(newHandles, entries, fwstateConfig.Name())

		return nil
	})
}

// LinkConfigs creates new ACL module configs for the given explicit list of
// names, linking them to fwstateConfig, then calls publish so the caller can
// push the combined dataplane update atomically. A name absent from configs
// is an error, as today.
func (m *ACLAdapter) LinkConfigs(
	names []string,
	fwstateConfig *fwstate.FwStateConfig,
	publish func(linkedFFI []ffi.ModuleConfig) error,
) error {
	if err := m.service.checkLinkable(names); err != nil {
		return err
	}

	return m.service.withEntries(names, func(entries map[string]*configEntry) error {
		newHandles, err := m.service.createLinkedHandles(names, entries, fwstateConfig)
		if err != nil {
			return err
		}

		ffiCfgs := make([]ffi.ModuleConfig, 0, len(newHandles))
		for _, h := range newHandles {
			ffiCfgs = append(ffiCfgs, h.AsFFIModule())
		}

		if err := publish(ffiCfgs); err != nil {
			for _, h := range newHandles {
				h.Free()
			}

			return err
		}

		m.service.swapLinkedHandles(newHandles, entries, fwstateConfig.Name())

		return nil
	})
}

// linkedConfigNames returns the names currently linked to fwstateConfigName.
func (m *ACLService) linkedConfigNames(fwstateConfigName string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0)
	for name, entry := range m.configs {
		if entry.published != nil && entry.published.fwstateName == fwstateConfigName {
			names = append(names, name)
		}
	}

	return names
}

// checkLinkable verifies that every name in names already has an entry,
// returning the same not-found error createLinkedHandles would produce for
// the first missing name.
//
// LinkConfigs uses this as a read-only pre-check, before withEntries
// interns an entry for every requested name regardless of whether one
// already exists. Existence is the correct test here: an entry is created
// only by UpdateConfig, so one already being present means the name was
// created at some point, or is being created right now by an UpdateConfig
// whose compile has not finished, and either way the name belongs on the
// locked path below rather than being rejected up front. A name confirmed
// to have an entry here is not guaranteed to still have a live published
// config once withEntries locks it: a concurrent delete could run in
// between, or an in-flight create could still be compiling. Neither race is
// this check's job: the authoritative re-check under the name's own lock in
// createLinkedHandles, which runs only after any in-flight compile for the
// name has finished, catches both.
func (m *ACLService) checkLinkable(names []string) error {
	for _, name := range names {
		if !m.hasEntry(name) {
			return fmt.Errorf("ACL config %q not found", name)
		}
	}

	return nil
}

// stillLinked filters names to those still linked to fwstateConfigName,
// preserving names' order.
//
// The caller holds updateMu for every entry in entries, which is what
// makes reading published without m.mu safe and authoritative: nothing
// else can mutate one of these entries while its lock is held.
func (m *ACLService) stillLinked(names []string, entries map[string]*configEntry, fwstateConfigName string) []string {
	kept := make([]string, 0, len(names))
	for _, name := range names {
		entry := entries[name]
		if entry.published != nil && entry.published.fwstateName == fwstateConfigName {
			kept = append(kept, name)
		}
	}

	return kept
}

// swapLinkedHandles publishes newHandles into entries and the metrics
// snapshot under a single mu.Lock section, then frees the handles they
// replaced.
//
// Caller must hold the name lock for every name in newHandles, and entries
// must carry that name's entry.
func (m *ACLService) swapLinkedHandles(newHandles map[string]ModuleHandle, entries map[string]*configEntry, fwstateConfigName string) {
	m.mu.Lock()
	oldHandles := make(map[string]ModuleHandle, len(newHandles))
	for name, newHandle := range newHandles {
		entry := entries[name]

		var rules []*aclpb.Rule
		if entry.published != nil {
			oldHandles[name] = entry.published.acl
			rules = entry.published.rules
		}

		entry.published = &aclConfig{
			rules:       rules,
			acl:         newHandle,
			fwstateName: fwstateConfigName,
		}
	}
	m.publishMetricsSnapshotLocked()
	m.mu.Unlock()

	for _, h := range oldHandles {
		if h != nil {
			h.Free()
		}
	}
}

// createLinkedHandles creates one new ACL module handle per distinct name in
// names, linked to fwstateConfig.
//
// names may repeat a name, in which case the returned map still holds one
// handle for it. On any failure every handle created so far is freed before
// returning the error.
//
// Caller must hold the name lock for every name in names, and entries must
// carry that name's entry.
func (m *ACLService) createLinkedHandles(
	names []string,
	entries map[string]*configEntry,
	fwstateConfig *fwstate.FwStateConfig,
) (map[string]ModuleHandle, error) {
	newHandles := map[string]ModuleHandle{}

	for _, name := range names {
		if _, ok := newHandles[name]; ok {
			continue
		}

		entry, ok := entries[name]
		if !ok || entry.published == nil {
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

		rules, err := convertRules(entry.published.rules)
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
