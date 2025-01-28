package yncp

import (
	"context"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/controlplane/internal/pkg/gateway"
	"github.com/yanet-platform/yanet2/controlplane/modules/route/pkg/route"
)

type Director struct {
	cfg     *Config
	gateway *gateway.Gateway
	log     *zap.SugaredLogger
}

func NewDirector(cfg *Config, log *zap.SugaredLogger) (*Director, error) {
	log.Infof("initializing YANET controlplane ...")
	log.Debugw("parsed config", zap.Any("config", cfg))

	gw := gateway.NewGateway(
		cfg.Gateway,
		gateway.WithBuiltInModule(
			route.NewRouteModule(cfg.Modules.Route, log),
		),
		gateway.WithLog(log),
	)

	return &Director{
		cfg:     cfg,
		gateway: gw,
		log:     log,
	}, nil
}

// Run runs the YANET controlplane director.
func (m *Director) Run(ctx context.Context) error {
	// Serve.
	wg, ctx := errgroup.WithContext(ctx)
	wg.Go(func() error {
		return m.gateway.Run(ctx)
	})

	return wg.Wait()
}
