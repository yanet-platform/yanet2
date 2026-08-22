package forward

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
)

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
	module, err := cforward.NewModuleConfig(m.agent, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)
	}

	if err := module.Update(rules); err != nil {
		if err := module.Free(); err != nil {
			return nil, fmt.Errorf("failed to free abandoned config: %w", err)
		}
		return nil, fmt.Errorf("failed to update module config: %w", err)
	}

	if err := m.agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}); err != nil {
		if err := module.Free(); err != nil {
			return nil, fmt.Errorf("failed to free abandoned config: %w", err)
		}
		return nil, fmt.Errorf("failed to update module: %w", err)
	}

	return module, nil
}

func (m *backend) DeleteModule(name string) error {
	return m.agent.DeleteModuleConfig(moduleType, name)
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
