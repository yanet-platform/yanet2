package balancer

import (
	"context"
	"fmt"
	"math"
	"net/netip"
	"sync"
	"time"

	yanet "github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/agent/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"go.uber.org/zap"
)

// Balancer wraps an FFI balancer instance and manages its lifecycle
type Balancer struct {
	// FFI balancer handle
	balancer ffi.Balancer

	// Agent for shared memory operations
	agent *yanet.Agent

	// Current configuration
	config      *balancerpb.ModuleConfig
	stateConfig *balancerpb.ModuleStateConfig

	// Buffered real updates
	realUpdateBuffer []ffi.RealUpdate

	// Track enabled/disabled state of reals
	// Key is "vsIP:vsPort:proto:realIP"
	realEnabledState map[string]bool

	// Background task management
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex

	// Logger
	log *zap.SugaredLogger
}

// NewBalancerFromProto creates a new Balancer from protobuf configuration
func NewBalancerFromProto(
	agent yanet.Agent,
	name string,
	moduleConfig *balancerpb.ModuleConfig,
	moduleStateConfig *balancerpb.ModuleStateConfig,
	log *zap.SugaredLogger,
) (*Balancer, error) {
	log.Infow("creating balancer instance", "name", name)

	// Validate configurations
	if moduleConfig == nil {
		return nil, fmt.Errorf("module config is required")
	}
	if moduleStateConfig == nil {
		return nil, fmt.Errorf("module state config is required")
	}
	if moduleStateConfig.SessionTableScanPeriod == nil {
		return nil, fmt.Errorf("session table scan period is required")
	}

	// Convert protobuf config to FFI config
	ffiConfig, err := ProtoToFFIConfig(moduleConfig, moduleStateConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to convert config: %w", err)
	}

	// Create FFI balancer
	ffiBalancer, err := ffi.Create(&agent, name, ffiConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create FFI balancer: %w", err)
	}

	b := &Balancer{
		balancer:         ffiBalancer,
		agent:            &agent,
		config:           moduleConfig,
		stateConfig:      moduleStateConfig,
		realUpdateBuffer: make([]ffi.RealUpdate, 0),
		realEnabledState: make(map[string]bool),
		log:              log,
	}

	// Initialize enabled state for all reals (all enabled by default)
	for _, vs := range moduleConfig.VirtualServices {
		for _, real := range vs.Reals {
			key := makeRealKey(vs.Addr, vs.Port, vs.Proto, real.DstAddr)
			b.realEnabledState[key] = true
		}
	}

	// Start background tasks
	b.startBackgroundTasks()

	log.Infow("balancer instance created successfully", "name", name)
	return b, nil
}

// Update updates the balancer configuration
func (b *Balancer) Update(
	moduleConfig *balancerpb.ModuleConfig,
	moduleStateConfig *balancerpb.ModuleStateConfig,
) error {
	b.log.Info("updating balancer configuration")

	// Validate configurations
	if moduleConfig == nil {
		return fmt.Errorf("module config is required")
	}
	if moduleStateConfig == nil {
		return fmt.Errorf("module state config is required")
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	// Resize session table if capacity is non-zero and different from current
	if moduleStateConfig.SessionTableCapacity != 0 &&
		moduleStateConfig.SessionTableCapacity != b.stateConfig.SessionTableCapacity {
		b.log.Infow("resizing session table",
			"old_capacity", b.stateConfig.SessionTableCapacity,
			"new_capacity", moduleStateConfig.SessionTableCapacity)

		now := uint32(time.Now().Unix())
		err := b.balancer.ResizeSessionTable(int(moduleStateConfig.SessionTableCapacity), now)
		if err != nil {
			b.log.Errorw("failed to resize session table",
				"requested_capacity", moduleStateConfig.SessionTableCapacity,
				"error", err)
			return fmt.Errorf("failed to resize session table: %w", err)
		}

		b.log.Infow("session table resized successfully",
			"new_capacity", moduleStateConfig.SessionTableCapacity)
	}

	// Convert protobuf config to FFI handler config
	handlerConfig, err := ProtoToHandlerConfig(moduleConfig)
	if err != nil {
		return fmt.Errorf("failed to convert handler config: %w", err)
	}

	// Update packet handler
	if err := b.balancer.UpdateHandler(handlerConfig); err != nil {
		return fmt.Errorf("failed to update packet handler: %w", err)
	}

	// Store new configuration
	b.config = moduleConfig
	b.stateConfig = moduleStateConfig

	// Restart background tasks with new configuration
	b.stopBackgroundTasks()
	b.startBackgroundTasks()

	b.log.Info("balancer configuration updated successfully")
	return nil
}

// UpdateReals updates real server weights and enabled status
func (b *Balancer) UpdateReals(updates []ffi.RealUpdate, buffer bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if buffer {
		// Buffer the updates for later application
		b.log.Infow("buffering real updates", "count", len(updates))
		b.realUpdateBuffer = append(b.realUpdateBuffer, updates...)
		return nil
	}

	// Apply updates immediately
	b.log.Infow("applying real updates immediately", "count", len(updates))

	b.applyRealUpdates(updates)

	return nil
}

// FlushRealUpdates flushes buffered real updates
func (b *Balancer) FlushRealUpdates() (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	count := len(b.realUpdateBuffer)

	b.log.Infow("flushing buffered real updates", "count", count)

	if count == 0 {
		b.log.Debug("no buffered real updates to flush")
		return 0, nil
	}

	b.applyRealUpdates(b.realUpdateBuffer)

	// Clear the buffer
	b.realUpdateBuffer = b.realUpdateBuffer[:0]

	b.log.Infow("successfully flushed real updates", "count", count)
	return count, nil
}

func (b *Balancer) applyRealUpdates(updates []ffi.RealUpdate) error {
	// Update reals in shm
	if err := b.balancer.UpdateReals(b.realUpdateBuffer); err != nil {
		b.log.Errorw("failed to update reals", "error", err)
		return fmt.Errorf("failed to update reals: %w", err)
	}

	// Update local state tracking
	for _, update := range b.realUpdateBuffer {
		if err := b.applyRealUpdate(update); err != nil {
			msg := fmt.Sprintf("failed to apply buffered real update to local state: %v", err)
			panic(msg)
		}
	}

	return nil
}

// GetConfig returns the current configuration
func (b *Balancer) GetConfig() (*balancerpb.ModuleConfig, *balancerpb.ModuleStateConfig) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.config, b.stateConfig
}

// GetStateInfo returns state information
func (b *Balancer) GetStateInfo(now time.Time) *ffi.BalancerInfo {
	b.mu.Lock()
	defer b.mu.Unlock()

	info, err := b.balancer.Info()
	if err != nil {
		b.log.Errorw("failed to get balancer info", "error", err)
		return &ffi.BalancerInfo{}
	}

	return &info
}

// GetSessionsInfo returns information about active sessions
func (b *Balancer) GetSessionsInfo(now time.Time) (*ffi.SessionsInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	count, sessions, err := b.balancer.Sessions(uint32(now.Unix()), false)
	if err != nil {
		return nil, fmt.Errorf("failed to get sessions: %w", err)
	}

	return &ffi.SessionsInfo{
		SessionsCount: uint(count),
		Sessions:      sessions,
	}, nil
}

// GetConfigStats returns configuration statistics
func (b *Balancer) GetConfigStats(device, pipeline, function, chain string) *ffi.BalancerInfo {
	b.mu.Lock()
	defer b.mu.Unlock()

	info, err := b.balancer.Info()
	if err != nil {
		b.log.Errorw("failed to get balancer info for stats", "error", err)
		return &ffi.BalancerInfo{}
	}

	return &info
}

// Helper functions

func (b *Balancer) applyRealUpdate(update ffi.RealUpdate) error {
	// Find the virtual service and real in config
	for _, vs := range b.config.VirtualServices {
		vsIP, _ := netip.AddrFromSlice(vs.Addr)
		if vsIP.Compare(update.Identifier.Vs.Ip) == 0 &&
			vs.Port == uint32(update.Identifier.Vs.Port) &&
			vs.Proto == ConvertFFIProtoToProto(update.Identifier.Vs.Proto) {

			// Find the real
			for i, real := range vs.Reals {
				realIP, _ := netip.AddrFromSlice(real.DstAddr)
				if realIP.Compare(update.Identifier.Ip) == 0 {
					// Update weight if provided
					if update.Weight != nil {
						vs.Reals[i].Weight = uint32(*update.Weight)
					}
					// Update enabled state if provided
					if update.Enabled != nil {
						key := makeRealKey(vs.Addr, vs.Port, vs.Proto, real.DstAddr)
						b.realEnabledState[key] = *update.Enabled
					}
					return nil
				}
			}
			return fmt.Errorf("real not found: %s", update.Identifier.Ip)
		}
	}
	return fmt.Errorf("virtual service not found: %s:%d", update.Identifier.Vs.Ip, update.Identifier.Vs.Port)
}

// makeRealKey creates a unique key for tracking real enabled state
func makeRealKey(vsAddr []byte, vsPort uint32, vsProto balancerpb.TransportProto, realAddr []byte) string {
	vsIP, _ := netip.AddrFromSlice(vsAddr)
	realIP, _ := netip.AddrFromSlice(realAddr)
	protoStr := "tcp"
	if vsProto == balancerpb.TransportProto_UDP {
		protoStr = "udp"
	}
	return fmt.Sprintf("%s:%d:%s:%s", vsIP, vsPort, protoStr, realIP)
}

// isRealEnabled checks if a real is enabled
func (b *Balancer) isRealEnabled(vsAddr []byte, vsPort uint32, vsProto balancerpb.TransportProto, realAddr []byte) bool {
	key := makeRealKey(vsAddr, vsPort, vsProto, realAddr)
	enabled, exists := b.realEnabledState[key]
	if !exists {
		// Default to enabled if not found
		return true
	}
	return enabled
}

func (b *Balancer) startBackgroundTasks() {
	b.ctx, b.cancel = context.WithCancel(context.Background())

	// Start session table monitoring task
	if b.stateConfig.SessionTableScanPeriod != nil {
		period := b.stateConfig.SessionTableScanPeriod.AsDuration()
		go b.sessionTableMonitorTask(period)
	}

	// Start WLC update task if configured
	if b.config.Wlc != nil && b.config.Wlc.UpdatePeriod != nil {
		period := b.config.Wlc.UpdatePeriod.AsDuration()
		go b.wlcUpdateTask(period)
	}
}

func (b *Balancer) stopBackgroundTasks() {
	if b.cancel != nil {
		b.cancel()
	}
}

func (b *Balancer) sessionTableMonitorTask(period time.Duration) {
	ticker := time.NewTicker(period)
	defer ticker.Stop()

	b.log.Infow("starting session table monitor task", "period", period)

	for {
		select {
		case <-b.ctx.Done():
			b.log.Info("session table monitor task stopped")
			return
		case <-ticker.C:
			b.monitorSessionTable(time.Now())
		}
	}
}

func (b *Balancer) monitorSessionTable(t time.Time) {
	now := uint32(t.Unix())

	// Get session count only
	count, _, err := b.balancer.Sessions(now, true)
	if err != nil {
		b.log.Errorw("failed to get session count", "error", err)
		return
	}

	// Check if we need to resize based on load factor
	capacity := b.stateConfig.SessionTableCapacity
	if capacity == 0 {
		return
	}

	loadFactor := float32(count) / float32(capacity)
	maxLoadFactor := b.stateConfig.SessionTableMaxLoadFactor

	if loadFactor > maxLoadFactor {
		// Calculate new capacity based on current and previous session counts
		// Using formula from old implementation: 2*current - previous / maxLoadFactor
		newCapacity := uint64(math.Ceil(float64(count) * 2.0 / float64(maxLoadFactor)))
		// Ensure we at least double the capacity
		if newCapacity < capacity*2 {
			newCapacity = capacity * 2
		}

		b.log.Infow("session table load factor exceeded, resizing",
			"current_count", count,
			"capacity", capacity,
			"load_factor", loadFactor,
			"max_load_factor", maxLoadFactor,
			"new_capacity", newCapacity)

		// Resize the session table
		err := b.balancer.ResizeSessionTable(int(newCapacity), now)
		if err != nil {
			b.log.Errorw("failed to resize session table on demand",
				"requested_capacity", newCapacity,
				"error", err)
			// Don't return error, just log it - we'll try again next time
			return
		}

		// Update state config with new capacity
		b.stateConfig.SessionTableCapacity = newCapacity

		b.log.Infow("session table resized successfully on demand",
			"old_capacity", capacity,
			"new_capacity", newCapacity)
	}
}

func (b *Balancer) wlcUpdateTask(period time.Duration) {
	ticker := time.NewTicker(period)
	defer ticker.Stop()

	b.log.Infow("starting WLC update task", "period", period)

	for {
		select {
		case <-b.ctx.Done():
			b.log.Info("WLC update task stopped")
			return
		case <-ticker.C:
			b.updateWLC()
		}
	}
}

func (b *Balancer) updateWLC() {
	// Check if WLC is configured
	if b.config.Wlc == nil {
		return
	}

	// Get current session information
	now := uint32(time.Now().Unix())
	_, sessions, err := b.balancer.Sessions(now, false)
	if err != nil {
		b.log.Errorw("failed to get sessions for WLC", "error", err)
		return
	}

	// Count active sessions per real
	realSessions := make(map[ffi.RealIdentifier]uint)
	for _, session := range sessions {
		realSessions[session.Real]++
	}

	// Track if any weights changed
	weightsChanged := false

	// Update effective weights for each virtual service using WLC algorithm
	for _, vs := range b.config.VirtualServices {
		if vs.Scheduler != balancerpb.VsScheduler_WLC {
			continue
		}

		// Calculate sums for WLC formula
		var connectionsSum uint64
		var weightsSum uint64

		vsIP, _ := netip.AddrFromSlice(vs.Addr)
		vsProto := convertProtoToFFIProto(vs.Proto)

		for _, real := range vs.Reals {
			// Skip disabled reals
			if !b.isRealEnabled(vs.Addr, vs.Port, vs.Proto, real.DstAddr) {
				continue
			}

			realIP, _ := netip.AddrFromSlice(real.DstAddr)
			realId := ffi.RealIdentifier{
				Vs: ffi.VsIdentifier{
					Ip:    vsIP,
					Port:  uint16(vs.Port),
					Proto: vsProto,
				},
				Ip: realIP,
			}
			connectionsSum += uint64(realSessions[realId])
			weightsSum += uint64(real.Weight)
		}

		// Calculate new effective weights
		for i, real := range vs.Reals {
			// Skip disabled reals
			if !b.isRealEnabled(vs.Addr, vs.Port, vs.Proto, real.DstAddr) {
				continue
			}

			realIP, _ := netip.AddrFromSlice(real.DstAddr)
			realId := ffi.RealIdentifier{
				Vs: ffi.VsIdentifier{
					Ip:    vsIP,
					Port:  uint16(vs.Port),
					Proto: vsProto,
				},
				Ip: realIP,
			}

			newWeight := b.calcWlcWeight(
				uint16(real.Weight),
				realSessions[realId],
				weightsSum,
				connectionsSum,
			)

			// Update weight if changed
			if uint32(newWeight) != real.Weight {
				vs.Reals[i].Weight = uint32(newWeight)
				weightsChanged = true
			}
		}
	}

	// If weights changed, update the handler
	if weightsChanged {
		b.log.Debugw("WLC weights updated, applying changes")
		handlerConfig, err := ProtoToHandlerConfig(b.config)
		if err != nil {
			b.log.Errorw("failed to convert handler config for WLC update", "error", err)
			return
		}

		if err := b.balancer.UpdateHandler(handlerConfig); err != nil {
			b.log.Errorw("failed to update handler with WLC weights", "error", err)
			return
		}
		b.log.Debugw("WLC update applied successfully")
	}
}

// calcWlcWeight calculates the effective weight using WLC algorithm
// Based on the old implementation in modules/balancer/old/lib/wlc.go
func (b *Balancer) calcWlcWeight(
	weight uint16,
	connections uint,
	weightSum uint64,
	connectionsSum uint64,
) uint16 {
	if b.config.Wlc == nil || weight == 0 || weightSum == 0 || connectionsSum < weightSum {
		return weight
	}

	wlc := b.config.Wlc
	scaledConnections := float64(connections) * float64(weightSum)
	scaledWeight := float64(connectionsSum) * float64(weight)
	connectionsRatio := scaledConnections / scaledWeight

	const minRatio = 1.0
	wlcRatio := math.Max(minRatio, float64(wlc.WlcPower)*(1.0-connectionsRatio))

	newWeight := uint64(math.Round(float64(weight) * wlcRatio))
	newWeight = min(newWeight, uint64(wlc.MaxRealWeight))

	return uint16(newWeight)
}

// convertProtoToFFIProto converts protobuf TransportProto to FFI VsProto
func convertProtoToFFIProto(proto balancerpb.TransportProto) ffi.VsProto {
	if proto == balancerpb.TransportProto_TCP {
		return ffi.ProtoTcp
	}
	return ffi.ProtoUdp
}

// Free releases resources
func (b *Balancer) Free() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.stopBackgroundTasks()
	// Note: FFI balancer cleanup would happen here if needed
}
