package blackhole

import (
	"context"
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/blackhole/bindings/go/cblackhole"
)

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

func (m *backend) UpdateModule(ctx context.Context, name string) (ModuleHandle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	mod, err := cblackhole.NewModuleConfig(m.agent, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)
	}

	if err := m.agent.UpdateModules(
		ctx,
		[]ffi.ModuleConfig{mod.AsFFIModule()},
	); err != nil {
		if err := mod.Free(); err != nil {
			return nil, fmt.Errorf("failed to free abandoned config: %w", err)
		}
		return nil, fmt.Errorf("failed to update module %q: %w", name, err)
	}

	return mod, nil
}

func (m *backend) DeleteModule(ctx context.Context, name string) error {
	return m.agent.DeleteModuleConfig(ctx, moduleName, name)
}
