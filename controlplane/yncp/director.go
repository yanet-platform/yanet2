package yncp

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/internal/gateway"
	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
	balancer "github.com/yanet-platform/yanet2/modules/balancer/controlplane"
	decap "github.com/yanet-platform/yanet2/modules/decap/controlplane"
	dscp "github.com/yanet-platform/yanet2/modules/dscp/controlplane"
	forward "github.com/yanet-platform/yanet2/modules/forward/controlplane"
	nat64 "github.com/yanet-platform/yanet2/modules/nat64/controlplane"
	pdump "github.com/yanet-platform/yanet2/modules/pdump/controlplane"
	route "github.com/yanet-platform/yanet2/modules/route/controlplane"

	plain "github.com/yanet-platform/yanet2/devices/plain/controlplane"
	vlan "github.com/yanet-platform/yanet2/devices/vlan/controlplane"
)

type options struct {
	Log      *zap.SugaredLogger
	LogLevel *zap.AtomicLevel
}

func newOptions() *options {
	return &options{
		Log: zap.NewNop().Sugar(),
	}
}

// DirectorOption is a function that configures the YANET controlplane
// director.
type DirectorOption func(*options)

// WithLog sets the logger for the YANET controlplane director.
func WithLog(log *zap.SugaredLogger) DirectorOption {
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
	log     *zap.SugaredLogger
}

// NewDirector creates a new YANET controlplane director using specified
// config.
func NewDirector(cfg *Config, options ...DirectorOption) (*Director, error) {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log
	log.Infof("initializing YANET controlplane ...")
	log.Debugw("parsed config", zap.Any("config", cfg))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath)
	if err != nil {
		return nil, fmt.Errorf("failed to attach to shared memory %q: %w", cfg.MemoryPath, err)
	}
	log.Debugw("attached to shared memory", zap.String("path", cfg.MemoryPath))

	routeModule, err := route.NewRouteModule(cfg.Modules.Route, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize route built-in module: %w", err)
	}

	decapModule, err := decap.NewDecapModule(cfg.Modules.Decap, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize decap built-in module: %w", err)
	}

	dscpModule, err := dscp.NewDSCPModule(cfg.Modules.DSCP, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize dscp built-in module: %w", err)
	}

	forwardModule, err := forward.NewForwardModule(cfg.Modules.Forward, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize forward built-in module: %w", err)
	}

	nat64Module, err := nat64.NewNAT64Module(cfg.Modules.NAT64, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize nat64 built-in module: %w", err)
	}

	pdumpModule, err := pdump.NewPdumpModule(cfg.Modules.Pdump, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize pdump built-in module: %w", err)
	}

	aclModule, err := acl.NewACLModule(cfg.Modules.ACL, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize acl built-in module: %w", err)
	}

	balancerModule, err := balancer.NewBalancerModule(cfg.Modules.Balancer, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize balancer built-in module: %w", err)
	}

	plainDevice, err := plain.NewDevicePlainDevice(cfg.Devices.Plain, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize plain built-in device: %w", err)
	}

	vlanDevice, err := vlan.NewDeviceVlanDevice(cfg.Devices.Vlan, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize vlan built-in device: %w", err)
	}

	gateway := gateway.NewGateway(
		cfg.Gateway,
		shm,
		gateway.WithBuiltInModule(
			routeModule,
		),
		gateway.WithBuiltInModule(
			decapModule,
		),
		gateway.WithBuiltInModule(
			dscpModule,
		),
		gateway.WithBuiltInModule(
			forwardModule,
		),
		gateway.WithBuiltInModule(
			nat64Module,
		),
		gateway.WithBuiltInModule(
			pdumpModule,
		),
		gateway.WithBuiltInModule(
			aclModule,
		),
		gateway.WithBuiltInModule(
			balancerModule,
		),
		gateway.WithBuiltInDevice(
			plainDevice,
		),
		gateway.WithBuiltInDevice(
			vlanDevice,
		),
		gateway.WithLog(log),
		gateway.WithAtomicLogLevel(opts.LogLevel),
	)

	return &Director{
		cfg:     cfg,
		shm:     shm,
		gateway: gateway,
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
