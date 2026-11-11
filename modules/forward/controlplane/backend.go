package forward

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/crud"
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

func (m *backend) Update(name string, payload RuleSet) (crud.Handle, error) {
	module, err := cforward.NewModuleConfig(m.agent, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)
	}

	build := func(module *cforward.ModuleConfig) error {
		if err := module.Update(payload.Compiled); err != nil {
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
