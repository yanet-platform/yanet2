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

	"github.com/c2h5oh/datasize"
	yanet "github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/go/ffi"
	"go.uber.org/zap"
)

type BalancerAgent struct {
	handle   *ffi.BalancerAgent
	managers map[string]*BalancerManager

	mu sync.Mutex

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
		handle:   handle,
		managers: managers,
		mu:       sync.Mutex{},
		log:      log,
	}, nil
}

func (a *BalancerAgent) NewBalancerManager(
	name string,
	config *balancerpb.BalancerConfig,
) error {
	a.mu.Lock()
	defer a.mu.Unlock()

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
