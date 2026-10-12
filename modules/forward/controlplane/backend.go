package forward

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
	"github.com/yanet-platform/yanet2/objects/classifier/bindings/go/cclassifier"
)

// classifierObjectType is the shared object type the module binds its
// classification through.
const classifierObjectType = "classifier"

// boundModule is the published pair behind one forward config: the
// module and the classifier object its rules were compiled into. The
// object carries the name of the module, so the pair is published and
// retired as one unit.
type boundModule struct {
	agent  *ffi.Agent
	name   string
	module *cforward.ModuleConfig
	object *cclassifier.ObjectConfig
}

// Free releases both handles of the pair, the module first: the
// module links the object, and the object retires only after every
// module generation referencing it is gone. Safe to call repeatedly;
// reports ffi.ErrStillReferenced while a live generation still holds
// either half, so the config store parks the pair for a later retry.
func (m *boundModule) Free() error {
	if m.module != nil {
		if err := m.module.Free(); err != nil {
			return err
		}
	}
	if m.object != nil {
		if err := m.object.Free(); err != nil {
			return err
		}
	}
	return nil
}

// abandon retracts a pair whose publication failed: the module, the
// object and its live-generation registration.
func (m *boundModule) abandon() {
	if m.module != nil {
		if err := m.module.Free(); err != nil {
			return
		}
	}
	if err := m.agent.DeleteObject(classifierObjectType, m.name); err != nil {
		return
	}
	m.object.Free()
}

type CounterView struct {
	Device   string
	Pipeline string
	Function string
	Chain    string
	Name     string
	Values   [][]uint64
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

func (m *backend) UpdateModule(name string, rules []cforward.ForwardRule) (ModuleHandle, error) {
	classifierRules, decisions := cforward.Split(rules)

	// The classifier object publishes first: the module build binds it
	// by name and resolves the ruleset its decisions are indexed by.
	object, err := cclassifier.NewL3(m.agent, name, classifierRules)
	if err != nil {
		return nil, fmt.Errorf("failed to create classifier object: %w", err)
	}

	if err := m.agent.UpdateObjects([]ffi.ObjectConfig{object.AsFFIObject()}); err != nil {
		if freeErr := object.Free(); freeErr != nil {
			return nil, fmt.Errorf(
				"failed to update classifier object: %v (failed to free abandoned object: %v)",
				err,
				freeErr,
			)
		}
		return nil, fmt.Errorf("failed to update classifier object: %w", err)
	}

	bound := &boundModule{
		agent:  m.agent,
		name:   name,
		object: object,
	}

	module, err := cforward.NewModuleConfig(m.agent, name, name, decisions)
	if err != nil {
		bound.abandon()
		return nil, fmt.Errorf("failed to create module config: %w", err)
	}
	bound.module = module

	if err := m.agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}); err != nil {
		bound.abandon()
		return nil, fmt.Errorf("failed to update module: %w", err)
	}

	return bound, nil
}

func (m *backend) DeleteModule(name string) error {
	if err := m.agent.DeleteModuleConfig(moduleType, name); err != nil {
		return err
	}
	return m.agent.DeleteObject(classifierObjectType, name)
}

func (m *backend) ModuleCounters(name string, counterNames []string) []CounterView {
	dpConfig := m.agent.DPConfig()

	var views []CounterView
	for pos := range dpConfig.AllModulePositions(moduleType) {
		if pos.ModuleName != name {
			continue
		}

		infos := dpConfig.ModuleCounters(
			pos.Device,
			pos.Pipeline,
			pos.Function,
			pos.Chain,
			moduleType,
			name,
			counterNames,
		)
		for _, info := range infos {
			views = append(views, CounterView{
				Device:   pos.Device,
				Pipeline: pos.Pipeline,
				Function: pos.Function,
				Chain:    pos.Chain,
				Name:     info.Name,
				Values:   info.Values,
			})
		}
	}

	return views
}
