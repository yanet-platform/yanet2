package balancer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/go/ffi"
	"go.uber.org/zap"
)

type BalancerManager struct {
	handle *ffi.BalancerManager

	realUpdateBuffer []ffi.RealUpdate

	// Background task management
	ctx    context.Context
	cancel context.CancelFunc

	mu sync.Mutex

	// Logger
	log *zap.SugaredLogger
}

func NewBalancerManager(
	handle *ffi.BalancerManager,
	log *zap.SugaredLogger,
) *BalancerManager {
	name := handle.Name()
	manager := &BalancerManager{
		handle:           handle,
		realUpdateBuffer: []ffi.RealUpdate{},
		log:              log.With("balancer", name),
	}
	manager.startBackgroundTasks()
	return manager
}

func (b *BalancerManager) Name() string {
	return b.handle.Name()
}

func (b *BalancerManager) Update(
	config *balancerpb.BalancerConfig,
	now time.Time,
) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.log.Debugw("updating balancer configuration")

	// Merge new config with current config for UPDATE mode
	mergedConfig, err := mergeBalancerConfig(config, b.handle.Config())
	if err != nil {
		b.log.Errorw("failed to merge config", "error", err)
		return fmt.Errorf("failed to merge config: %w", err)
	}

	// Convert merged protobuf to FFI config
	ffiConfig, err := ProtoToFFIConfig(mergedConfig)
	if err != nil {
		b.log.Errorw("failed to convert config", "error", err)
		return fmt.Errorf("failed to convert config: %w", err)
	}

	// Create WLC configuration with validation
	wlcConfig, err := createWlcConfig(mergedConfig)
	if err != nil {
		b.log.Errorw("failed to create WLC config", "error", err)
		return fmt.Errorf("failed to create WLC config: %w", err)
	}

	// Create manager config
	managerConfig := &ffi.BalancerManagerConfig{
		Balancer:      ffiConfig,
		RefreshPeriod: mergedConfig.State.RefreshPeriod.AsDuration(),
		MaxLoadFactor: *mergedConfig.State.SessionTableMaxLoadFactor,
		Wlc:           wlcConfig,
	}

	// Update via FFI
	if err := b.handle.Update(managerConfig, now); err != nil {
		b.log.Errorw("failed to update manager", "error", err)
		return fmt.Errorf("failed to update manager: %w", err)
	}

	b.log.Infow("balancer configuration updated successfully")
	return nil
}

func (b *BalancerManager) UpdateReals(
	updates []*balancerpb.RealUpdate,
	buffer bool,
) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.log.Debugw("updating reals", "count", len(updates), "buffer", buffer)

	// Convert protobuf updates to FFI updates
	ffiUpdates := make([]ffi.RealUpdate, 0, len(updates))
	for i, update := range updates {
		ffiUpdate, err := NewRealUpdateFromProto(update)
		if err != nil {
			b.log.Errorw("failed to convert update", "index", i, "error", err)
			return 0, fmt.Errorf(
				"failed to convert update at index %d: %w",
				i,
				err,
			)
		}
		ffiUpdates = append(ffiUpdates, *ffiUpdate)
	}

	if buffer {
		// Buffer the updates
		b.realUpdateBuffer = append(b.realUpdateBuffer, ffiUpdates...)
		b.log.Debugw(
			"real updates buffered",
			"count",
			len(updates),
			"total_buffered",
			len(b.realUpdateBuffer),
		)
		return len(updates), nil
	}

	// Apply immediately
	if err := b.handle.UpdateReals(ffiUpdates); err != nil {
		b.log.Errorw("failed to update reals", "error", err)
		return 0, err
	}

	b.log.Infow("real updates applied", "count", len(updates))
	return len(updates), nil
}

func (b *BalancerManager) FlushRealUpdates() (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	count := len(b.realUpdateBuffer)
	if count == 0 {
		b.log.Debugw("no buffered updates to flush")
		return 0, nil
	}

	b.log.Debugw("flushing buffered real updates", "count", count)

	// Apply buffered updates
	if err := b.handle.UpdateReals(b.realUpdateBuffer); err != nil {
		b.log.Errorw("failed to flush real updates", "error", err)
		return 0, err
	}

	// Clear buffer
	b.realUpdateBuffer = b.realUpdateBuffer[:0]

	b.log.Infow("buffered real updates flushed", "count", count)
	return count, nil
}

func (b *BalancerManager) Config() *balancerpb.BalancerConfig {
	b.mu.Lock()
	defer b.mu.Unlock()

	return ConvertBalancerConfigToProto(b.handle.Config())
}

func (b *BalancerManager) BufferedUpdates() []*balancerpb.RealUpdate {
	b.mu.Lock()
	defer b.mu.Unlock()

	updates := make([]*balancerpb.RealUpdate, len(b.realUpdateBuffer))
	for i := range b.realUpdateBuffer {
		updates[i] = ConvertFFIRealUpdateToProto(&b.realUpdateBuffer[i])
	}
	return updates
}

func (b *BalancerManager) Graph() *balancerpb.Graph {
	b.mu.Lock()
	defer b.mu.Unlock()
	cfg := b.handle.Config()
	graph := b.handle.Graph()

	return ConvertGraphToProtoWithConfig(graph, cfg)
}

func (b *BalancerManager) Info(
	now time.Time,
) (*balancerpb.BalancerInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	ffiInfo, err := b.handle.Info(now)
	if err != nil {
		return nil, err
	}

	return ConvertBalancerInfoToProto(ffiInfo), nil
}

func (b *BalancerManager) Stats(
	ref *balancerpb.PacketHandlerRef,
) (*balancerpb.BalancerStats, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Convert protobuf ref to FFI ref
	ffiRef := &ffi.PacketHandlerRef{
		Device:   ref.Device,
		Pipeline: ref.Pipeline,
		Function: ref.Function,
		Chain:    ref.Chain,
	}

	ffiStats, err := b.handle.Stats(ffiRef)
	if err != nil {
		return nil, err
	}

	return ConvertBalancerStatsToProto(ffiStats), nil
}

func (b *BalancerManager) Sessions(
	now time.Time,
) ([]*balancerpb.SessionInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	ffiSessions := b.handle.Sessions(now)

	sessions := make([]*balancerpb.SessionInfo, 0, len(ffiSessions.Sessions))
	for i := range ffiSessions.Sessions {
		sessions = append(sessions, ConvertSessionInfoToProto(
			&ffiSessions.Sessions[i].Identifier,
			&ffiSessions.Sessions[i].Info,
		))
	}

	return sessions, nil
}

func (b *BalancerManager) startBackgroundTasks() {
	b.ctx, b.cancel = context.WithCancel(context.Background())

	if b.handle.Config().RefreshPeriod == 0 {
		return
	}

	// Start background refresh task
	go b.backgroundRefreshTask()
}

// backgroundRefreshTask runs periodically to:
// 1. Get balancer info
// 2. Resize session table if load factor exceeds threshold
// 3. Adjust WLC weights based on active connections
// 4. Apply real updates if needed
func (b *BalancerManager) backgroundRefreshTask() {
	for {
		// Get current config to check refresh period
		b.mu.Lock()
		config := b.handle.Config()
		refreshPeriod := config.RefreshPeriod
		b.mu.Unlock()

		// If refresh period is zero, stop the task
		if refreshPeriod == 0 {
			b.log.Debugw(
				"background refresh task stopped (refresh_period is zero)",
			)
			return
		}

		// Wait for refresh period or context cancellation
		select {
		case <-b.ctx.Done():
			b.log.Debugw("background refresh task stopped (context cancelled)")
			return
		case <-time.After(refreshPeriod):
			// Continue with refresh
		}

		now := time.Now()

		// Perform refresh with error handling
		if err := b.Refresh(now); err != nil {
			b.log.Errorw("background refresh failed", "error", err)
		}
	}
}

// Refresh executes the refresh logic
func (b *BalancerManager) Refresh(now time.Time) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.log.Debug("refreshing")

	// Get current config
	config := b.handle.Config()

	// Get balancer info
	info, err := b.handle.Info(now)
	if err != nil {
		return fmt.Errorf("failed to get info: %w", err)
	}

	// Check if session table needs resizing
	capacity := config.Balancer.State.TableCapacity
	activeSessions := info.ActiveSessions
	maxLoadFactor := config.MaxLoadFactor

	currentLoadFactor := float32(activeSessions) / float32(capacity)

	b.log.Debugw("fetched balancer info",
		"current_capacity", capacity,
		"active_sessions", activeSessions,
		"current_load_factor", currentLoadFactor,
		"max_load_factor", maxLoadFactor)

	if currentLoadFactor > maxLoadFactor {
		newCapacity := capacity * 2
		b.log.Infow("resizing session table",
			"current_capacity", capacity,
			"new_capacity", newCapacity,
			"active_sessions", activeSessions,
			"current_load_factor", currentLoadFactor,
			"max_load_factor", maxLoadFactor)

		if err := b.handle.ResizeSessionTable(newCapacity, now); err != nil {
			b.log.Errorw("failed to resize session table", "error", err)
		} else {
			b.log.Infow("session table resized successfully", "new_capacity", newCapacity)
		}
	}

	// WLC real updates - use UpdateRealsWlc to preserve config weights
	updates := WlcUpdates(b.handle.Config(), b.handle.Graph(), info)

	b.log.Debugw("calculated WLC updates", "count", len(updates))

	if len(updates) > 0 {
		b.log.Infow("applying WLC updates", "count", len(updates))
		if err := b.handle.UpdateRealsWlc(updates); err != nil {
			b.log.Errorw("failed to apply WLC updates", "error", err)
		} else {
			b.log.Infow("WLC updates applied successfully", "count", len(updates))
		}
	}

	return nil
}

func (b *BalancerManager) stopBackgroundTasks() {
	if b.cancel != nil {
		b.cancel()
	}
}

func (b *BalancerManager) Free() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.stopBackgroundTasks()
}
