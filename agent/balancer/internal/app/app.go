package app

import (
	"context"
	"time"

	"github.com/yanet-platform/yanet2/agent/balancer/internal/controlplane"
	"github.com/yanet-platform/yanet2/agent/balancer/internal/core"
)

type App struct {
	config *Config
	core   *core.Core
}

func New(config *Config) (*App, error) {
	controlPlane, err := controlplane.New(config.ControlPlane)
	if err != nil {
		return nil, err
	}
	return &App{
		config: config,
		core:   core.New(controlPlane),
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		coreConfig, err := core.LoadConfig(a.config.ServicesPath)
		if err != nil {
			return err
		}
		err = a.core.Reload(ctx, coreConfig)
		if err != nil {
			return err
		}
	}
}
