package balancer

import (
	"context"
	"fmt"
	"sync"
	"time"

	yanet "github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/agent/go/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"go.uber.org/zap"
)

type RealState struct {
	enabled         bool
	activeSessions  uint64
	effectiveWeight uint64
}

// Balancer wraps an FFI balancer instance and manages its lifecycle
type Balancer struct {
	// Agent for shared memory operations
	agent *yanet.Agent

	// balancer handle
	balancer ffi.Balancer

	// Current configuration
	config *ffi.BalancerConfig

	// Buffered real updates
	realUpdateBuffer []ffi.RealUpdate

	reals map[ffi.RealIdentifier]RealState

	// Background task management
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex

	// Logger
	log *zap.SugaredLogger
}

// ListBalancers returns a list of all balancer instances registered in the agent.
// It retrieves existing balancers from shared memory, populates them with their
// NewBalancerFromProto creates a new Balancer from protobuf configuration
// This is a stub that returns an error - protobuf config support not implemented
func NewBalancerFromProto(
	agent yanet.Agent,
	name string,
	config *balancerpb.BalancerConfig,
	log *zap.SugaredLogger,
) (*Balancer, error) {
	return nil, fmt.Errorf("NewBalancerFromProto not implemented - use FFI config directly")
}

// Update updates the balancer configuration
// This is a stub that returns an error - protobuf config support not implemented
func (b *Balancer) Update(config *balancerpb.BalancerConfig) error {
	return fmt.Errorf("Update with protobuf config not implemented")
}

// current configuration and real server state, but does not start background tasks.
// This function is useful for discovering and inspecting existing balancer instances.
func ListBalancers(
	agent *yanet.Agent,
	log *zap.SugaredLogger,
) ([]*Balancer, error) {
	log.Info("listing balancers from agent")

	// Get all FFI balancer handles from the agent
	ffiBalancers := ffi.ListBalancers(agent)
	if len(ffiBalancers) == 0 {
		log.Info("no balancers found in agent")
		return nil, nil
	}

	log.Infow("found balancers", "count", len(ffiBalancers))

	balancers := make([]*Balancer, 0, len(ffiBalancers))

	for _, ffiBalancer := range ffiBalancers {
		name := ffiBalancer.Name()
		balancerLog := log.With("balancer", name)
		balancerLog.Debug("processing balancer")

		// Get the balancer's current configuration
		ffiConfig := ffiBalancer.Config()

		// Get the balancer's graph (VS -> Reals topology with state)
		graph := ffiBalancer.Graph()

		// Create the Balancer instance without starting background tasks
		b := &Balancer{
			agent:            agent,
			balancer:         ffiBalancer,
			config:           ffiConfig,
			realUpdateBuffer: make([]ffi.RealUpdate, 0),
			reals:            make(map[ffi.RealIdentifier]RealState),
			log:              balancerLog,
		}

		// Populate the reals map from the graph
		for _, vs := range graph.VirtualServices {
			for _, graphReal := range vs.Reals {
				// Construct the full RealIdentifier
				realId := ffi.RealIdentifier{
					Vs: vs.Identifier,
					Relative: ffi.RelativeRealIdentifier{
						Ip:   graphReal.Identifier,
						Port: vs.Identifier.Port, // Use VS port as default
					},
				}

				// Store the real's state
				b.reals[realId] = RealState{
					enabled:         graphReal.Enabled,
					activeSessions:  0, // Not available from graph
					effectiveWeight: uint64(graphReal.Weight),
				}
			}
		}

		balancerLog.Infow("loaded balancer",
			"vs_count", len(graph.VirtualServices),
			"real_count", len(b.reals))

		balancers = append(balancers, b)
	}

	log.Infow("successfully listed balancers", "count", len(balancers))
	return balancers, nil
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

	return b.applyRealUpdates(updates)
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

	if err := b.applyRealUpdates(b.realUpdateBuffer); err != nil {
		return 0, err
	}

	// Clear the buffer
	b.realUpdateBuffer = b.realUpdateBuffer[:0]

	b.log.Infow("successfully flushed real updates", "count", count)
	return count, nil
}

func (b *Balancer) applyRealUpdates(updates []ffi.RealUpdate) error {
	// Update reals in shm
	if err := b.balancer.UpdateReals(updates); err != nil {
		b.log.Errorw("failed to update reals", "error", err)
		return fmt.Errorf("failed to update reals: %w", err)
	}

	// Update local state tracking
	for _, update := range updates {
		if state, exists := b.reals[update.Identifier]; exists {
			if update.Weight != nil {
				state.effectiveWeight = uint64(*update.Weight)
			}
			if update.Enabled != nil {
				state.enabled = *update.Enabled
			}
			b.reals[update.Identifier] = state
		}
	}

	return nil
}

// GetConfig returns the current configuration
func (b *Balancer) GetConfig() *ffi.BalancerConfig {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.config
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
func (b *Balancer) GetSessionsInfo(now time.Time) ([]ffi.SessionInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	sessions, err := b.balancer.Sessions(uint32(now.Unix()))
	if err != nil {
		return nil, fmt.Errorf("failed to get sessions: %w", err)
	}

	return sessions, nil
}

// GetConfigStats returns configuration statistics
func (b *Balancer) GetConfigStats(
	device, pipeline, function, chain string,
) *ffi.BalancerInfo {
	b.mu.Lock()
	defer b.mu.Unlock()

	info, err := b.balancer.Info()
	if err != nil {
		b.log.Errorw("failed to get balancer info for stats", "error", err)
		return &ffi.BalancerInfo{}
	}

	return &info
}

// Free releases resources
func (b *Balancer) Free() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.stopBackgroundTasks()
	// Note: FFI balancer cleanup would happen here if needed
}

func (b *Balancer) startBackgroundTasks() {
	b.ctx, b.cancel = context.WithCancel(context.Background())
	// Background tasks can be added here if needed
}

func (b *Balancer) stopBackgroundTasks() {
	if b.cancel != nil {
		b.cancel()
	}
}
