package module

import (
	"fmt"
	"sync"
	"time"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/lib"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/durationpb"
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

// todo: add proto validations

func NewBalancerFromProto(
	agent ffi.Agent,
	name string,
	moduleConfig *balancerpb.ModuleConfig,
	moduleStateConfig *balancerpb.ModuleStateConfig,
	log *zap.SugaredLogger,
) (*Balancer, error) {
	log.Infow("creating balancer instance", "name", name)

	// Validate ModuleConfig
	if moduleConfig == nil {
		return nil, fmt.Errorf("module config is required")
	}

	// Validate ModuleStateConfig
	if moduleStateConfig == nil {
		return nil, fmt.Errorf("module state config is required")
	}
	if moduleStateConfig.SessionTableScanPeriod == nil {
		return nil, fmt.Errorf("session table scan period is required")
	}

	lock := &sync.Mutex{}
	stateLog := log.With("component", "state")
	state, err := NewModuleConfigState(
		agent,
		lock,
		uint(moduleStateConfig.SessionTableCapacity),
		uint(
			moduleStateConfig.SessionTableScanPeriod.AsDuration().
				Milliseconds(),
		),
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
	addresses, err := lib.NewBalancerAddressesFromProto(moduleConfig)
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
	sessionTimeouts, err := lib.NewSessionsTimeoutsFromProto(
		moduleConfig.SessionsTimeouts,
	)
	if err != nil {
		log.Errorw(
			"failed to parse session timeouts",
			"name",
			name,
			"error",
			err,
		)
		return nil, fmt.Errorf("failed to parse session timeouts: %w", err)
	}

	// Register virtual services with their reals
	virtualServices := make(
		[]lib.VirtualService,
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

	wlc, err := lib.NewWlcConfigFromProto(moduleConfig.Wlc)
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
		lock,
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

	// Validate ModuleConfig
	if moduleConfig == nil {
		return fmt.Errorf("module config is required")
	}

	// Validate ModuleStateConfig
	if moduleStateConfig == nil {
		return fmt.Errorf("module state config is required")
	}

	b.lock.Lock()
	defer b.lock.Unlock()

	// Parse balancer addresses
	addresses, err := lib.NewBalancerAddressesFromProto(moduleConfig)
	if err != nil {
		b.log.Errorw("failed to parse balancer addresses", "error", err)
		return fmt.Errorf("failed to parse balancer addresses: %w", err)
	}

	// Parse session timeouts
	sessionTimeouts, err := lib.NewSessionsTimeoutsFromProto(
		moduleConfig.SessionsTimeouts,
	)
	if err != nil {
		b.log.Errorw("failed to parse session timeouts", "error", err)
		return fmt.Errorf("failed to parse session timeouts: %w", err)
	}

	// Register virtual services with their reals
	virtualServices := make(
		[]lib.VirtualService,
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
	wlc, err := lib.NewWlcConfigFromProto(moduleConfig.Wlc)
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
	if moduleStateConfig.SessionTableScanPeriod == nil {
		return fmt.Errorf("session table scan period is required")
	}
	b.log.Infow(
		"updating state configuration",
		"old_scan_period_ms", b.moduleConfigState.ScanSessionTablePeriodMs,
		"new_scan_period_ms", moduleStateConfig.SessionTableScanPeriod.AsDuration().Milliseconds(),
	)
	b.moduleConfigState.Update(
		uint(moduleStateConfig.SessionTableCapacity),
		uint(
			moduleStateConfig.SessionTableScanPeriod.AsDuration().
				Milliseconds(),
		),
		moduleStateConfig.SessionTableMaxLoadFactor,
		time.Now(),
	)
	b.log.Debug("updated state configuration")

	b.log.Info("balancer configuration updated successfully")
	return nil
}

// UpdateReals updates reals with optional buffering
func (b *Balancer) UpdateReals(updates []lib.RealUpdate, buffer bool) error {
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
		SessionTableScanPeriod: durationpb.New(
			time.Duration(
				b.moduleConfigState.ScanSessionTablePeriodMs,
			) * time.Millisecond,
		),
		SessionTableMaxLoadFactor: float32(b.moduleConfigState.MaxLoadFactor),
	}

	return moduleConfigProto, moduleStateConfigProto
}

// GetStateInfo returns state information with fresh active session data
func (b *Balancer) GetStateInfo(now time.Time) *lib.BalancerInfo {
	b.lock.Lock()
	defer b.lock.Unlock()

	// Scan session table to get fresh active session counts
	if err := b.moduleConfigState.SyncActiveSessions(now); err != nil {
		b.log.Warnw(
			"failed to sync active sessions during StateInfo call",
			"error", err,
		)
		// Continue and return info with potentially stale data
		// rather than failing completely
	}

	return b.moduleConfigState.GetInfo()
}

// GetSessionsInfo returns information about active sessions
func (b *Balancer) GetSessionsInfo(time time.Time) (*lib.SessionsInfo, error) {
	b.lock.Lock()
	defer b.lock.Unlock()

	return b.moduleConfigState.GetAndUpdateSessionsInfo(time)
}

// GetConfigStats returns configuration statistics
func (b *Balancer) GetConfigStats(
	device, pipeline, function, chain string,
) lib.BalancerStats {
	b.lock.Lock()
	defer b.lock.Unlock()

	return b.moduleConfig.GetStats(
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

// GetModuleConfig returns the internal module state configuration for testing
func (b *Balancer) GetModuleConfigState() *ModuleConfigState {
	return b.moduleConfigState
}

// Free releases resources
func (b *Balancer) Free() {
	b.lock.Lock()
	defer b.lock.Unlock()

	b.moduleConfigState.Free()
	b.moduleConfig.Free()
}

////////////////////////////////////////////////////////////////////////////////

// Allows to sync WLC, active sessions and resize session table.
// The balancer makes it himself with corresponding periods.
// We can add proto method to use this function in future.
func (balancer *Balancer) SyncActiveSessionsAndWlcAndResizeTableOnDemand(
	now time.Time,
) error {
	if err := balancer.moduleConfigState.SyncActiveSessionsAndResizeTableOnDemand(now); err != nil {
		return fmt.Errorf(
			"failed to scan sessions table to sync active sessions: %w",
			err,
		)
	}
	if _, err := balancer.moduleConfig.UpdateEffectiveWeights(); err != nil {
		return fmt.Errorf("failed to update WLC: %w", err)
	}
	return nil
}
