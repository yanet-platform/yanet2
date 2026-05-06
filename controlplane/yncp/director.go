package yncp

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/controlplane/builtin"
	"github.com/yanet-platform/yanet2/controlplane/bundle"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/gateway"
)

type options struct {
	Log      *zap.Logger
	LogLevel *zap.AtomicLevel
}

func newOptions() *options {
	return &options{
		Log: zap.NewNop(),
	}
}

// DirectorOption is a function that configures the YANET controlplane
// director.
type DirectorOption func(*options)

// WithLog sets the logger for the YANET controlplane director.
func WithLog(log *zap.Logger) DirectorOption {
	return func(o *options) {
		o.Log = log
	}
}

// WithAtomicLogLevel sets the atomic logger level for the YANET controlplane
// director.
//
// This level can be changed at runtime.
func WithAtomicLogLevel(level *zap.AtomicLevel) DirectorOption {
	return func(o *options) {
		o.LogLevel = level
	}
}

// Director is the YANET controlplane director.
//
// This is an entry point for the YANET controlplane. Its main purposes is to
// initialize basic configuration, set up the Gateway API, sidecar gRPC
// services and run them.
type Director struct {
	cfg     *Config
	shm     *ffi.SharedMemory
	gateway *gateway.Gateway
	log     *zap.Logger
}

// NewDirector creates a new YANET controlplane director using specified
// config.
func NewDirector(cfg *Config, options ...DirectorOption) (*Director, error) {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log
	log.Info("initializing YANET controlplane ...")
	log.Info("parsed config", zap.Any("config", cfg))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath)
	if err != nil {
		return nil, fmt.Errorf("failed to attach to shared memory %q: %w", cfg.MemoryPath, err)
	}
	log.Debug("attached to shared memory", zap.String("path", cfg.MemoryPath))

	bundle, err := bundle.NewBundle(cfg.Modules, cfg.Devices, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize bundle: %w", err)
	}

	gatewayOptions := []gateway.GatewayOption{
		gateway.WithService(
			builtin.NewLogging(opts.LogLevel, log),
		),
		gateway.WithService(
			builtin.NewInspect(cfg.Gateway.InstanceID, shm),
		),
		gateway.WithService(
			builtin.NewPipeline(cfg.Gateway.InstanceID, shm, log),
		),
		gateway.WithService(
			builtin.NewFunction(cfg.Gateway.InstanceID, shm, log),
		),
		gateway.WithService(
			builtin.NewCounters(cfg.Gateway.InstanceID, shm),
		),
		gateway.WithLog(log),
		gateway.WithAtomicLogLevel(opts.LogLevel),
	}
	for _, service := range bundle.Services() {
		gatewayOptions = append(gatewayOptions, gateway.WithService(service))
	}

	gw, err := gateway.NewGateway(cfg.Gateway, gatewayOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to create gateway: %w", err)
	}

	return &Director{
		cfg:     cfg,
		shm:     shm,
		gateway: gw,
		log:     log,
	}, nil
}

// Close closes the YANET controlplane director.
func (m *Director) Close() error {
	defer m.shm.Detach()

	return m.gateway.Close()
}

// Run runs the YANET controlplane director.
func (m *Director) Run(ctx context.Context) error {
	return m.gateway.Run(ctx)
}
