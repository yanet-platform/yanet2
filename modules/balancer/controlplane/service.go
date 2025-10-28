package balancer

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

type moduleKey struct {
	name              string
	dataplaneInstance uint32
}

////////////////////////////////////////////////////////////////////////////////

type BalancerService struct {
	balancerpb.UnimplementedBalancerServiceServer

	mu sync.Mutex

	/// FIXME: make separated locks for balancer instances

	instances map[moduleKey]*BalancerInstance
	agents    []*ffi.Agent
	log       *zap.SugaredLogger
}

////////////////////////////////////////////////////////////////////////////////

func NewBalancerService(agents []*ffi.Agent, log *zap.SugaredLogger) *BalancerService {
	return &BalancerService{
		mu:        sync.Mutex{},
		agents:    agents,
		log:       log,
		instances: make(map[moduleKey]*BalancerInstance),
	}
}

////////////////////////////////////////////////////////////////////////////////

func (service *BalancerService) EnableBalancing(
	ctx context.Context,
	req *balancerpb.EnableBalancingRequest,
) (*balancerpb.EnableBalancingResponse, error) {
	name, inst, err := req.GetTarget().Validate(uint32(len(service.agents)))
	if err != nil {
		return nil, fmt.Errorf("incorrect target module: %w", err)
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

	config, err := NewBalancerConfigFromProto(req.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse provided config: %w", err)
	}

	instance, err := NewBalancerInstance(service.agents[inst], name, config, req.SessionTableSize)
	if err != nil {
		return nil, fmt.Errorf("failed to create new balancer instance: %w", err)
	}

	err = instance.UpdateModules()
	if err != nil {
		return nil, fmt.Errorf("failed to update modules: %w", err)
	}

	service.instances[key] = instance

	return &balancerpb.EnableBalancingResponse{}, nil
}

////////////////////////////////////////////////////////////////////////////////

func (service *BalancerService) ReloadConfig(
	ctx context.Context,
	req *balancerpb.ReloadConfigRequest,
) (*balancerpb.ReloadConfigResponse, error) {
	name, inst, err := req.GetTarget().Validate(uint32(len(service.agents)))
	if err != nil {
		return nil, fmt.Errorf("incorrect target module: %w", err)
	}

	config, err := NewBalancerConfigFromProto(req.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	service.mu.Lock()
	defer service.mu.Unlock()

	key := moduleKey{name: name, dataplaneInstance: inst}
	instance, exists := service.instances[key]

	if exists {
		prevInstance := *instance
		err = instance.UpdateConfig(config)
		if err != nil {
			return nil, fmt.Errorf("failed to reload instance config: %w", err)
		}
		err = instance.UpdateModules()
		if err != nil {
			*instance = prevInstance
			return nil, fmt.Errorf("failed to update modules: %w", err)
		}
		return &balancerpb.ReloadConfigResponse{}, nil
	} else {
		return nil, fmt.Errorf("module [name=%s, inst=%d] not exists", name, inst)
	}
}

////////////////////////////////////////////////////////////////////////////////

func (service *BalancerService) UpdateReals(
	ctx context.Context,
	req *balancerpb.UpdateRealsRequest,
) (*balancerpb.UpdateRealsResponse, error) {
	// TODO: implement
	return nil, fmt.Errorf("not implelemented")
}

////////////////////////////////////////////////////////////////////////////////

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
		config := instance.GetConfig()
		return &balancerpb.ShowConfigResponse{
			Config: config.IntoProto(),
		}, nil
	} else {
		return nil, fmt.Errorf("module [name=%s, inst=%d] not exists", name, inst)
	}
}

////////////////////////////////////////////////////////////////////////////////

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

////////////////////////////////////////////////////////////////////////////////

func (service *BalancerService) RunChecks(ctx context.Context, period time.Duration) error {
	ticker := time.NewTicker(period)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			println("herer!")
			return nil
		case <-ticker.C:
		}

		service.mu.Lock()

		for _, value := range service.instances {
			if err := value.CheckSessionTable(); err != nil {
				println("failed to check session table!")
			}
		}

		service.mu.Unlock()
	}
}
