package unrdup

import (
	"context"
	"fmt"

	"github.com/yanet-platform/xnetip"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/unrdup/bindings/go/cunrdup"
)

type backend struct {
	agent *ffi.Agent
}

func newBackend(agent *ffi.Agent) *backend {
	return &backend{
		agent: agent,
	}
}

func (m *backend) UpdateModule(
	ctx context.Context,
	name string,
	sources []xnetip.Network,
	services []cunrdup.Service,
) (ModuleHandle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	module, err := cunrdup.NewModuleConfig(m.agent, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)
	}

	for _, source := range sources {
		if err := module.SetSource(source); err != nil {
			_ = module.Free()
			return nil, fmt.Errorf("failed to set source: %w", err)
		}
	}

	if err := module.UpdateServices(services); err != nil {
		_ = module.Free()
		return nil, fmt.Errorf("failed to update services: %w", err)
	}

	if err := m.agent.UpdateModules(ctx, []ffi.ModuleConfig{module.AsFFIModule()}); err != nil {
		_ = module.Free()
		return nil, fmt.Errorf("failed to update module: %w", err)
	}

	return module, nil
}
