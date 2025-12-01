package mbalancer

import (
	"context"
	"fmt"
	"sync"
	"time"

	commonpb "github.com/yanet-platform/yanet2/common/proto"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"go.uber.org/zap"
)

////////////////////////////////////////////////////////////////////////////////

// Module instance identifier
type moduleKey struct {
	name              string
	dataplaneInstance uint32
}

////////////////////////////////////////////////////////////////////////////////

// gRPC service for controlling balancer module instances
type BalancerService struct {
	balancerpb.UnimplementedBalancerServiceServer

	mu sync.Mutex

	/// FIXME: make separated locks for balancer instances

	instances map[moduleKey]*ModuleInstance
	agents    []*ffi.Agent
	log       *zap.SugaredLogger
}

////////////////////////////////////////////////////////////////////////////////

func NewBalancerService(agents []*ffi.Agent, log *zap.SugaredLogger) *BalancerService {
	return &BalancerService{
		mu:        sync.Mutex{},
		agents:    agents,
		log:       log,
		instances: make(map[moduleKey]*ModuleInstance),
	}
}

////////////////////////////////////////////////////////////////////////////////

// Enable balancing for the specified module.
// Creates balancer instance with provided config.
func (service *BalancerService) EnableBalancing(
	ctx context.Context,
	req *balancerpb.EnableBalancingRequest,
) (*balancerpb.EnableBalancingResponse, error) {
	name, inst, err := req.GetTarget().Validate(uint32(len(service.agents)))
	if err != nil {
		return nil, fmt.Errorf("incorrect target module: %v", err)
	}

	service.mu.Lock()
	defer service.mu.Unlock()

	key := moduleKey{name: name, dataplaneInstance: inst}
	_, exists := service.instances[key]
	if exists {
		return nil, fmt.Errorf(
			"balancing already enabled for the module [name=%s, inst=%d]",
			name,
			inst,
		)
	}

	config, err := NewModuleInstanceConfig(req.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config: %v", err)
	}

	timeouts := NewSessionsTimeoutsFromProto(req.SessionsTimeouts)
	instance, err := NewModuleInstance(
		service.agents[inst],
		name,
		config,
		req.SessionTableSize,
		timeouts,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create new balancer instance: %v", err)
	}

	service.instances[key] = instance

	return &balancerpb.EnableBalancingResponse{}, nil
}

////////////////////////////////////////////////////////////////////////////////

// Reload config for the balancer instance.,
func (service *BalancerService) ReloadConfig(
	ctx context.Context,
	req *balancerpb.ReloadConfigRequest,
) (*balancerpb.ReloadConfigResponse, error) {
	name, inst, err := req.GetTarget().Validate(uint32(len(service.agents)))
	if err != nil {
		return nil, fmt.Errorf("incorrect target module: %v", err)
	}

	config, err := NewModuleInstanceConfig(req.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config: %v", err)
	}

	service.mu.Lock()
	defer service.mu.Unlock()

	key := moduleKey{name: name, dataplaneInstance: inst}
	instance, exists := service.instances[key]

	if exists {
		if err = instance.UpdateConfig(config); err != nil {
			return nil, fmt.Errorf("failed to reload instance config: %v", err)
		}
		return &balancerpb.ReloadConfigResponse{}, nil
	} else {
		return nil, fmt.Errorf("module [name=%s, inst=%d] not exists", name, inst)
	}
}

////////////////////////////////////////////////////////////////////////////////

// Update reals for the instance config
func (service *BalancerService) UpdateReals(
	ctx context.Context,
	req *balancerpb.UpdateRealsRequest,
) (*balancerpb.UpdateRealsResponse, error) {
	name, inst, err := req.GetTarget().Validate(uint32(len(service.agents)))
	if err != nil {
		return nil, fmt.Errorf("incorrect target module: %v", err)
	}

	service.mu.Lock()
	defer service.mu.Unlock()

	key := moduleKey{name: name, dataplaneInstance: inst}
	instance, exists := service.instances[key]

	if !exists {
		return nil, fmt.Errorf("module [name=%s, inst=%d] not exists", name, inst)
	}

	updates := make([]*RealUpdate, 0, len(req.Updates))
	for idx, update := range req.Updates {
		if parsed, err := NewRealUpdateFromProto(update); err != nil {
			return nil, fmt.Errorf("failed to parse update %d: %v", idx, err)
		} else {
			updates = append(updates, parsed)
		}
	}

	if err = instance.UpdateReals(updates, req.Buffer); err != nil {
		return nil, fmt.Errorf("failed to handle real updates: %v", err)
	}
	return &balancerpb.UpdateRealsResponse{}, nil
}

////////////////////////////////////////////////////////////////////////////////

// Flush buffered update real requests for instance config
func (service *BalancerService) FlushRealUpdates(
	ctx context.Context,
	req *balancerpb.FlushRealUpdatesRequest,
) (*balancerpb.FlushRealUpdatesResponse, error) {
	name, inst, err := req.GetTarget().Validate(uint32(len(service.agents)))
	if err != nil {
		return nil, fmt.Errorf("incorrect target module: %v", err)
	}

	service.mu.Lock()
	defer service.mu.Unlock()

	key := moduleKey{name: name, dataplaneInstance: inst}
	instance, exists := service.instances[key]

	if !exists {
		return nil, fmt.Errorf("module [name=%s, inst=%d] not exists", name, inst)
	}

	flushed, err := instance.FlushRealUpdatesBuffer()
	if err != nil {
		return nil, fmt.Errorf("failed to flush real updates: %s", err)
	}

	return &balancerpb.FlushRealUpdatesResponse{
		UpdatesFlushed: flushed,
	}, nil
}

////////////////////////////////////////////////////////////////////////////////

func (service *BalancerService) StateInfo(
	ctx context.Context,
	req *balancerpb.StateInfoRequest,
) (*balancerpb.StateInfoResponse, error) {
	name, inst, err := req.GetTarget().Validate(uint32(len(service.agents)))
	if err != nil {
		return nil, fmt.Errorf("incorrect target module: %v", err)
	}

	service.mu.Lock()
	defer service.mu.Unlock()

	key := moduleKey{name: name, dataplaneInstance: inst}
	instance, exists := service.instances[key]

	if !exists {
		return nil, fmt.Errorf("module [name=%s, inst=%d] not exists", name, inst)
	}

	stateInfo, err := instance.StateInfo()
	if err != nil {
		return nil, fmt.Errorf("failed to get state info: %v", err)
	}

	return &balancerpb.StateInfoResponse{
		Info: stateInfo.IntoProto(),
	}, nil
}

func (service *BalancerService) ConfigInfo(
	ctx context.Context,
	req *balancerpb.ConfigInfoRequest,
) (*balancerpb.ConfigInfoResponse, error) {
	service.mu.Lock()
	defer service.mu.Unlock()

	key := moduleKey{name: req.Config, dataplaneInstance: req.DataplaneInstance}
	instance, exists := service.instances[key]

	if !exists {
		return nil, fmt.Errorf(
			"module [name=%s, inst=%d] not exists",
			req.Config,
			req.DataplaneInstance,
		)
	}

	configInfo, err := instance.ConfigInfo(req.Device, req.Pipeline, req.Function, req.Chain)
	if err != nil {
		return nil, fmt.Errorf("failed to get state info: %v", err)
	}

	return &balancerpb.ConfigInfoResponse{Info: configInfo.IntoProto()}, nil
}

////////////////////////////////////////////////////////////////////////////////

// Show config for the specified balancer instance
func (service *BalancerService) ShowConfig(
	ctx context.Context,
	req *balancerpb.ShowConfigRequest,
) (*balancerpb.ShowConfigResponse, error) {
	name, inst, err := req.GetTarget().Validate(uint32(len(service.agents)))
	if err != nil {
		return nil, fmt.Errorf("incorrect target module: %w", err)
	}

	service.mu.Lock()
	defer service.mu.Unlock()

	key := moduleKey{name: name, dataplaneInstance: inst}
	instance, exists := service.instances[key]
	if exists {
		return &balancerpb.ShowConfigResponse{
			Config: instance.GetConfig().IntoProto(),
		}, nil
	} else {
		return nil, fmt.Errorf("module [name=%s, inst=%d] not exists", name, inst)
	}
}

////////////////////////////////////////////////////////////////////////////////

// List configs for the existing balancer instances
func (service *BalancerService) ListConfigs(
	ctx context.Context,
	req *balancerpb.ListConfigsRequest,
) (*balancerpb.ListConfigsResponse, error) {
	service.mu.Lock()
	defer service.mu.Unlock()

	configs := make([]*balancerpb.BalancerInstanceConfigInfo, 0)

	for key, value := range service.instances {
		config := &balancerpb.BalancerInstanceConfigInfo{
			Module: &commonpb.TargetModule{
				ConfigName:        key.name,
				DataplaneInstance: key.dataplaneInstance,
			},
			Config: value.GetConfig().IntoProto(),
		}
		configs = append(configs, config)
	}

	return &balancerpb.ListConfigsResponse{
		Configs: configs,
	}, nil
}

// Make periodical check for the session table of all balancer instances.
// Periodically try to extend tables if it is needed and free unused data.
// Also, periodically update WLC.
func (service *BalancerService) Background(ctx context.Context, period time.Duration) error {
	ticker := time.NewTicker(period)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		service.mu.Lock()

		for m, instance := range service.instances {
			// check session table
			if err := instance.CheckSessionTable(); err != nil {
				service.log.Errorf(
					"failed to check session table for module [name=%s, instance=%d]: %s",
					m.name,
					m.dataplaneInstance,
					err,
				)
			}

			// update wlc
			if err := instance.UpdateWlc(); err != nil {
				service.log.Errorf(
					"failed to update wlc for module [name=%s, instance=%d]: %s",
					m.name,
					m.dataplaneInstance,
					err,
				)
			}
		}

		service.mu.Unlock()
	}
}
