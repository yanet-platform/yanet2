package proxy

import (
	"context"
	"fmt"
	"sync"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/proxy/controlplane/proxypb"
	"go.uber.org/zap"
)

type ProxyService struct {
	proxypb.UnimplementedProxyServiceServer

	mu      sync.Mutex
	log     *zap.SugaredLogger
	agents  []*ffi.Agent
	configs map[instanceKey]ProxyConfig
}

func NewProxyService(agents []*ffi.Agent, log *zap.SugaredLogger) *ProxyService {
	return &ProxyService{
		log:     log,
		agents:  agents,
		configs: make(map[instanceKey]ProxyConfig),
	}
}

func (s *ProxyService) ListConfigs(
	ctx context.Context, request *proxypb.ListConfigsRequest,
) (*proxypb.ListConfigsResponse, error) {

	response := &proxypb.ListConfigsResponse{
		InstanceConfigs: make([]*proxypb.InstanceConfigs, len(s.agents)),
	}
	for inst := range s.agents {
		response.InstanceConfigs[inst] = &proxypb.InstanceConfigs{
			Instance: uint32(inst),
		}
	}

	// Lock instances store and module updates
	s.mu.Lock()
	defer s.mu.Unlock()

	for key := range s.configs {
		instConfig := response.InstanceConfigs[key.dataplaneInstance]
		instConfig.Configs = append(instConfig.Configs, key.name)
	}

	return response, nil
}

func (s *ProxyService) ShowConfig(ctx context.Context, req *proxypb.ShowConfigRequest) (*proxypb.ShowConfigResponse, error) {
	_, inst, err := req.GetTarget().Validate(uint32(len(s.agents)))
	if err != nil {
		return nil, err
	}

	// key := instanceKey{name: name, dataplaneInstance: inst}
	response := &proxypb.ShowConfigResponse{Instance: inst}

	s.mu.Lock()
	defer s.mu.Unlock()

	// config := m.configs[key]
	response.Config = &proxypb.Config{ProxyConfig: &proxypb.ProxyConfig{}}

	return response, nil
}

func (s *ProxyService) DeleteConfig(ctx context.Context, req *proxypb.DeleteConfigRequest) (*proxypb.DeleteConfigResponse, error) {
	name, inst, err := req.GetTarget().Validate(uint32(len(s.agents)))
	if err != nil {
		return nil, err
	}
	// Remove module configuration from the control plane.
	delete(s.configs, instanceKey{name, inst})

	deleted := DeleteConfig(s, name, inst)

	response := &proxypb.DeleteConfigResponse{
		Deleted: deleted,
	}
	return response, nil
}

func (s *ProxyService) SetAddr(ctx context.Context, req *proxypb.SetAddrRequest) (*proxypb.SetAddrResponse, error) {
	name, inst, err := req.GetTarget().Validate(uint32(len(s.agents)))
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := instanceKey{name: name, dataplaneInstance: inst}
	config, ok := s.configs[key]
	if !ok {
		config = ProxyConfig{}
		s.configs[key] = config
	}

	config.Addr = req.Addr

	if err = s.updateModuleConfig(name, inst); err != nil {
		return nil, fmt.Errorf("failed to update module config: %w", err)
	}

	s.log.Infow("successfully set address",
		zap.String("name", name),
		zap.Uint32("addr", req.Addr),
		zap.Uint32("instance", inst),
	)

	return &proxypb.SetAddrResponse{}, nil
}

func (s *ProxyService) updateModuleConfig(name string, instance uint32) error {
	s.log.Debugw("updating configuration",
		zap.String("module", name),
		zap.Uint32("instance", instance),
	)

	if int(instance) >= len(s.agents) {
		return fmt.Errorf("instance index %d is out of range (agents length: %d)", instance, len(s.agents))
	}
	agent := s.agents[instance]
	if agent == nil {
		return fmt.Errorf("agent for instance %d is nil", instance)
	}

	moduleConfig, err := NewModuleConfig(agent, name)
	if err != nil {
		return fmt.Errorf("failed to create module config for instance %d: %w", instance, err)
	}

	key := instanceKey{name: name, dataplaneInstance: instance}
	config, ok := s.configs[key]
	if !ok {
		config = ProxyConfig{}
		s.configs[key] = config
	}

	if err := moduleConfig.SetAddr(config.Addr); err != nil {
		return fmt.Errorf("failed to set addr on instance %d: %w", instance, err)
	}

	if err := agent.UpdateModules([]ffi.ModuleConfig{moduleConfig.AsFFIModule()}); err != nil {
		return fmt.Errorf("failed to update module on instance %d: %w", instance, err)
	}

	s.log.Debugw("successfully updated module config",
		zap.String("name", name),
		zap.Uint32("instance", instance),
	)

	return nil
}
