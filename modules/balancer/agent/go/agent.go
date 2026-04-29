// Package balancer provides the load balancer agent implementation for YANET.
// This package manages balancer instances, virtual services, and real servers,
// coordinating between the control plane and data plane for packet distribution.
//
// The BalancerAgent manages multiple BalancerManager instances, each representing
// a separate load balancer configuration with its own virtual services and real servers.
package balancer

import (
	"fmt"
	"sync"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	yanet "github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/go/ffi"
	"go.uber.org/zap"
)

type BalancerAgent struct {
	handle   *ffi.BalancerAgent
	managers map[string]*BalancerManager

	mu sync.Mutex

	handlersMetrics handlersMetrics

	log *zap.SugaredLogger
}

func NewBalancerAgent(
	shm *yanet.SharedMemory,
	memory datasize.ByteSize,
	log *zap.SugaredLogger,
) (*BalancerAgent, error) {
	if log == nil {
		return nil, fmt.Errorf("logger cannot be nil")
	}

	handle, err := ffi.NewBalancerAgent(shm, uint(memory.Bytes()))
	if err != nil {
		return nil, err
	}
	managerHandles := handle.Managers()
	managers := make(map[string]*BalancerManager)
	for _, managerHandle := range managerHandles {
		manager := NewBalancerManager(&managerHandle, log)
		managers[manager.Name()] = manager
	}
	return &BalancerAgent{
		handle:          handle,
		managers:        managers,
		mu:              sync.Mutex{},
		log:             log,
		handlersMetrics: newHandlersMetrics(),
	}, nil
}

func (a *BalancerAgent) NewBalancerManager(
	name string,
	config *balancerpb.BalancerConfig,
) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	tracker := newHandlerMetricTracker(
		"create",
		&a.handlersMetrics,
		defaultLatencyBoundsMS,
		metrics.Labels{"config": name},
	)
	defer tracker.Fix()

	a.log.Infow("creating new balancer manager", "name", name)

	if _, ok := a.managers[name]; ok {
		a.log.Warnw("balancer manager already exists", "name", name)
		return fmt.Errorf(
			"balancer manager with name '%s' already exists",
			name,
		)
	}

	// Convert and validate config
	managerConfig, err := ProtoToManagerConfig(config)
	if err != nil {
		a.log.Errorw("failed to convert config", "name", name, "error", err)
		return fmt.Errorf("config is invalid: %w", err)
	}

	managerHandle, err := a.handle.NewManager(name, managerConfig)
	if err != nil {
		a.log.Errorw(
			"failed to create balancer manager",
			"name",
			name,
			"error",
			err,
		)
		return fmt.Errorf("failed to create new balancer manager: %v", err)
	}

	a.managers[name] = NewBalancerManager(
		managerHandle,
		a.log.With("balancer", name),
	)
	a.log.Infow("balancer manager created successfully", "name", name)
	return nil
}

func (a *BalancerAgent) BalancerManager(name string) (*BalancerManager, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	manager, ok := a.managers[name]
	if !ok {
		return nil, fmt.Errorf(
			"balancer manager with name '%s' not found",
			name,
		)
	}
	return manager, nil
}

func (a *BalancerAgent) Managers() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	res := []string{}
	for name := range a.managers {
		res = append(res, name)
	}
	return res
}

func (a *BalancerAgent) Inspect() *balancerpb.AgentInspect {
	a.mu.Lock()
	defer a.mu.Unlock()

	ffiInspect := a.handle.Inspect()
	return ConvertAgentInspectToProto(ffiInspect)
}

func (a *BalancerAgent) Metrics() ([]*commonpb.Metric, error) {
	dpConfig := a.handle.DPConfig()
	positions := dpConfig.AllModulePositions("balancer")

	managers := make([]*BalancerManager, 0, len(positions))
	{
		a.mu.Lock()

		for idx := range positions {
			position := &positions[idx]
			manager := a.managers[positions[idx].ModuleName]
			if manager == nil {
				a.log.Warnw(
					"metrics: balancer manager not found",
					"config",
					position.ModuleName,
				)
			}
			managers = append(managers, manager)
		}

		a.mu.Unlock()
	}

	result := make([]*commonpb.Metric, 0, len(managers)*200)

	for idx := range positions {
		manager := managers[idx]
		if manager == nil {
			continue
		}
		position := positions[idx]
		ref := balancerpb.PacketHandlerRef{
			Device:   &position.Device,
			Pipeline: &position.Pipeline,
			Function: &position.Function,
			Chain:    &position.Chain,
		}

		metrics, err := manager.Metrics(time.Now(), &ref)
		if err != nil {
			a.log.Errorf("failed to get metrics", "balancer", manager.Name())
		} else {
			result = append(result, metrics...)
		}
	}

	// append agent metrics
	result = append(result, a.handlersMetrics.collect()...)

	return result, nil
}

// StatsEntries enumerates dataplane balancer positions,
// optionally filters by balancer name and packet-handler ref fields, selects the
// corresponding manager for each position, and returns a list of (name, ref,
// stats) entries.
//
// Filtering rules:
// - if name is specified: only positions with ModuleName == name are included
// - for PacketHandlerRef: each specified field (device/pipeline/function/chain) is matched by strict equality.
func (a *BalancerAgent) StatsEntries(
	name *string,
	refFilter *balancerpb.PacketHandlerRef,
) ([]*balancerpb.StatsEntry, error) {
	dpConfig := a.handle.DPConfig()
	positions := dpConfig.AllModulePositions("balancer")

	// Snapshot managers under lock to avoid holding agent mutex during per-position stats reads.
	managersByName := make(map[string]*BalancerManager, len(a.managers))
	{
		a.mu.Lock()
		for k, v := range a.managers {
			managersByName[k] = v
		}
		a.mu.Unlock()
	}

	matchesRef := func(posDevice, posPipeline, posFunction, posChain string) bool {
		if refFilter == nil {
			return true
		}
		if refFilter.Device != nil && *refFilter.Device != posDevice {
			return false
		}
		if refFilter.Pipeline != nil && *refFilter.Pipeline != posPipeline {
			return false
		}
		if refFilter.Function != nil && *refFilter.Function != posFunction {
			return false
		}
		if refFilter.Chain != nil && *refFilter.Chain != posChain {
			return false
		}
		return true
	}

	entries := make([]*balancerpb.StatsEntry, 0)

	for idx := range positions {
		position := &positions[idx]

		// Optional manager-name filter
		if name != nil && position.ModuleName != *name {
			continue
		}

		// Optional packet-handler ref filter
		if !matchesRef(position.Device, position.Pipeline, position.Function, position.Chain) {
			continue
		}

		manager := managersByName[position.ModuleName]
		if manager == nil {
			a.log.Warnw(
				"stats: balancer manager not found",
				"config",
				position.ModuleName,
			)
			continue
		}

		ref := &balancerpb.PacketHandlerRef{
			Device:   &position.Device,
			Pipeline: &position.Pipeline,
			Function: &position.Function,
			Chain:    &position.Chain,
		}

		stats, err := manager.Stats(ref)
		if err != nil {
			a.log.Warnw(
				"failed to get stats for position",
				"config", position.ModuleName,
				"device", position.Device,
				"pipeline", position.Pipeline,
				"function", position.Function,
				"chain", position.Chain,
				"error", err,
			)
			continue
		}

		entries = append(entries, &balancerpb.StatsEntry{
			Name:  position.ModuleName,
			Ref:   ref,
			Stats: stats,
		})
	}

	return entries, nil
}
