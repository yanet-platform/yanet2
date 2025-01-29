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

type options struct {
	Log      *zap.SugaredLogger
	LogLevel *zap.AtomicLevel
}

func newOptions() *options {
	return &options{
		Log: zap.NewNop().Sugar(),
	}
}

type DirectorOption func(*options)

func WithLog(log *zap.SugaredLogger) DirectorOption {
	return func(o *options) {
		o.Log = log
	}
}

func WithAtomicLogLevel(level *zap.AtomicLevel) DirectorOption {
	return func(o *options) {
		o.LogLevel = level
	}
}

func NewDirector(cfg *Config, options ...DirectorOption) (*Director, error) {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log
	log.Infof("initializing YANET controlplane ...")
	log.Debugw("parsed config", zap.Any("config", cfg))

	gw := gateway.NewGateway(
		cfg.Gateway,
		gateway.WithBuiltInModule(
			route.NewRouteModule(cfg.Modules.Route, log),
		),
		gateway.WithLog(log),
		gateway.WithAtomicLogLevel(opts.LogLevel),
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
