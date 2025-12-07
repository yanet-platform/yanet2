package balancer

import (
	"fmt"
	"sync"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/module"
	"go.uber.org/zap"
)

////////////////////////////////////////////////////////////////////////////////

// Balancer module
type Balancer struct {
	// Balancer module config
	moduleConfig *ModuleConfig

	// Balancer module config state
	moduleConfigState *ModuleConfigState

	// Mutex for working with balancer
	lock *sync.Mutex

	// Logger with context
	log *zap.SugaredLogger
}

func NewBalancerFromProto(
	agent ffi.Agent,
	name string,
	moduleConfig *balancerpb.ModuleConfig,
	moduleStateConfig *balancerpb.ModuleStateConfig,
	log *zap.SugaredLogger,
) (*Balancer, error) {
	log.Infow("creating balancer instance", "name", name)

	lock := &sync.Mutex{}
	stateLog := log.With("component", "state")
	state, err := NewModuleConfigState(
		agent,
		lock,
		uint(moduleStateConfig.SessionTableCapacity),
		uint(moduleStateConfig.SessionTableScanPeriodMs),
		moduleStateConfig.SessionTableMaxLoadFactor,
		stateLog,
	)
	if err != nil {
		log.Errorw(
			"failed to create module config state",
			"name",
			name,
			"error",
			err,
		)
		return nil, fmt.Errorf("failed to create module config state: %w", err)
	}

	// Parse balancer addresses
	addresses, err := module.NewBalancerAddressesFromProto(moduleConfig)
	if err != nil {
		log.Errorw(
			"failed to parse balancer addresses",
			"name",
			name,
			"error",
			err,
		)
		return nil, fmt.Errorf("failed to parse balancer addresses: %w", err)
	}

	// Parse session timeouts
	sessionTimeouts := module.NewSessionsTimeoutsFromProto(
		moduleConfig.SessionsTimeouts,
	)

	// Register virtual services with their reals
	virtualServices := make(
		[]module.VirtualService,
		0,
		len(moduleConfig.VirtualServices),
	)
	for i, protoVs := range moduleConfig.VirtualServices {
		vs, err := state.RegisterVsWithReals(protoVs)
		if err != nil {
			log.Errorw(
				"failed to register virtual service",
				"name",
				name,
				"index",
				i,
				"error",
				err,
			)
			return nil, fmt.Errorf(
				"failed to register virtual service at index %d: %w",
				i,
				err,
			)
		}
		virtualServices = append(virtualServices, *vs)
	}
	log.Debugw(
		"registered virtual services",
		"name",
		name,
		"count",
		len(virtualServices),
	)

	wlc, err := module.NewWlcConfigFromProto(moduleConfig.Wlc)
	if err != nil {
		log.Errorw("failed to parse wlc config", "name", name, "error", err)
		return nil, fmt.Errorf("failed to parse wlc config: %w", err)
	}

	// Create module config
	configLog := log.With("component", "config")
	config, err := NewModuleConfig(
		agent,
		name,
		state,
		virtualServices,
		addresses,
		sessionTimeouts,
		wlc,
		configLog,
	)
	if err != nil {
		log.Errorw("failed to create module config", "name", name, "error", err)
		state.Free()
		return nil, fmt.Errorf("failed to create module config: %w", err)
	}

	log.Infow("balancer instance created successfully", "name", name)
	return &Balancer{
		moduleConfig:      config,
		moduleConfigState: state,
		lock:              lock,
		log:               log,
	}, nil
}

////////////////////////////////////////////////////////////////////////////////

// Update updates the balancer configuration
func (b *Balancer) Update(
	moduleConfig *balancerpb.ModuleConfig,
	moduleStateConfig *balancerpb.ModuleStateConfig,
) error {
	b.log.Info("updating balancer configuration")
	b.lock.Lock()
	defer b.lock.Unlock()

	// Parse balancer addresses
	addresses, err := module.NewBalancerAddressesFromProto(moduleConfig)
	if err != nil {
		b.log.Errorw("failed to parse balancer addresses", "error", err)
		return fmt.Errorf("failed to parse balancer addresses: %w", err)
	}

	// Parse session timeouts
	sessionTimeouts := module.NewSessionsTimeoutsFromProto(
		moduleConfig.SessionsTimeouts,
	)

	// Register virtual services with their reals
	virtualServices := make(
		[]module.VirtualService,
		0,
		len(moduleConfig.VirtualServices),
	)
	for i, protoVs := range moduleConfig.VirtualServices {
		vs, err := b.moduleConfigState.RegisterVsWithReals(protoVs)
		if err != nil {
			b.log.Errorw(
				"failed to register virtual service",
				"index",
				i,
				"error",
				err,
			)
			return fmt.Errorf(
				"failed to register virtual service at index %d: %w",
				i,
				err,
			)
		}
		virtualServices = append(virtualServices, *vs)
	}
	b.log.Debugw("registered virtual services", "count", len(virtualServices))

	// Parse WLC
	wlc, err := module.NewWlcConfigFromProto(moduleConfig.Wlc)
	if err != nil {
		b.log.Errorw("failed to parse WLC", "error", err)
		return fmt.Errorf("failed to parse WLC: %w", err)
	}

	// Update module config
	if err := b.moduleConfig.Update(virtualServices, addresses, sessionTimeouts, wlc); err != nil {
		b.log.Errorw("failed to update module config", "error", err)
		return fmt.Errorf("failed to update module config: %w", err)
	}

	// Update state config if provided
	if moduleStateConfig != nil {
		b.moduleConfigState.Update(
			uint(moduleStateConfig.SessionTableCapacity),
			uint(moduleStateConfig.SessionTableScanPeriodMs),
			moduleStateConfig.SessionTableMaxLoadFactor,
		)
		b.log.Debug("updated state configuration")
	}

	b.log.Info("balancer configuration updated successfully")
	return nil
}

// UpdateReals updates reals with optional buffering
func (b *Balancer) UpdateReals(updates []module.RealUpdate, buffer bool) error {
	b.lock.Lock()
	defer b.lock.Unlock()

	return b.moduleConfig.UpdateReals(updates, buffer)
}

// FlushRealUpdates flushes buffered real updates
func (b *Balancer) FlushRealUpdates() (int, error) {
	b.lock.Lock()
	defer b.lock.Unlock()

	return b.moduleConfig.FlushRealUpdates()
}

// GetConfig returns the current configuration as proto
func (b *Balancer) GetConfig() (*balancerpb.ModuleConfig, *balancerpb.ModuleStateConfig) {
	b.lock.Lock()
	defer b.lock.Unlock()

	moduleConfigProto := b.moduleConfig.IntoProto()

	moduleStateConfigProto := &balancerpb.ModuleStateConfig{
		SessionTableCapacity: uint64(
			b.moduleConfigState.SessionTableCapacity(),
		),
		SessionTableScanPeriodMs: uint32(
			b.moduleConfigState.ScanSessionTablePeriodMs,
		),
		SessionTableMaxLoadFactor: float32(b.moduleConfigState.MaxLoadFactor),
	}

	return moduleConfigProto, moduleStateConfigProto
}

// GetStateInfo returns state information
func (b *Balancer) GetStateInfo() module.BalancerInfo {
	b.lock.Lock()
	defer b.lock.Unlock()

	return b.moduleConfigState.GetInfo()
}

// GetConfigStats returns configuration statistics
func (b *Balancer) GetConfigStats(
	dataplaneInstance uint32,
	device, pipeline, function, chain string,
) module.BalancerStats {
	b.lock.Lock()
	defer b.lock.Unlock()

	return b.moduleConfig.GetStats(
		dataplaneInstance,
		device,
		pipeline,
		function,
		chain,
	)
}

// GetModuleConfig returns the internal module configuration for testing
func (b *Balancer) GetModuleConfig() *ModuleConfig {
	return b.moduleConfig
}

// Free releases resources
func (b *Balancer) Free() {
	b.lock.Lock()
	defer b.lock.Unlock()

	b.moduleConfigState.Free()
	b.moduleConfig.Free()
}
