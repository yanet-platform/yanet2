package balancer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"sync"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

type StateConfig struct {
	TcpSynAckTtl uint32
	TcpSynTtl    uint32
	TcpFinTtl    uint32
	TcpTtl       uint32
	UdpTtl       uint32
	DefaultTtl   uint32
}

type Real struct {
	Weight uint16

	DstAddr netip.Addr

	SrcAddr netip.Addr
	SrcMask netip.Addr
}

type ForwardingMethod string

func (fm ForwardingMethod) String() string {
	return string(fm)
}

const (
	TUN ForwardingMethod = "TUN"
	GRE ForwardingMethod = "GRE"
)

type Service struct {
	Addr             netip.Addr
	Prefixes         []netip.Prefix
	Reals            []Real
	ForwardingMethod ForwardingMethod
}

// BalancerConfig represents the configuration for a Balancer instance
type BalancerConfig struct {
	StateConfig  StateConfig
	Services     []Service
	ModuleConfig *ModuleConfig
}

func (cfg *BalancerConfig) DeepCopy() *BalancerConfig {
	if cfg == nil {
		return nil
	}
	newCfg := &BalancerConfig{
		StateConfig: cfg.StateConfig,
		Services:    make([]Service, 0, len(cfg.Services)),
	}
	for _, s := range cfg.Services {
		newService := Service{
			Addr:             s.Addr,
			Prefixes:         make([]netip.Prefix, len(s.Prefixes)),
			Reals:            make([]Real, len(s.Reals)),
			ForwardingMethod: s.ForwardingMethod,
		}
		copy(newService.Prefixes, s.Prefixes)
		copy(newService.Reals, s.Reals)
		newCfg.Services = append(newCfg.Services, newService)
	}
	return newCfg
}

// BalancerService implements the Balancer gRPC service
type BalancerService struct {
	balancerpb.UnimplementedBalancerServiceServer

	mu      sync.Mutex
	agents  []*ffi.Agent
	log     *zap.SugaredLogger
	configs map[instanceKey]*BalancerConfig
}

func NewBalancerService(
	agents []*ffi.Agent,
	log *zap.SugaredLogger,
) *BalancerService {
	return &BalancerService{
		agents:  agents,
		log:     log,
		configs: make(map[instanceKey]*BalancerConfig),
	}
}

func (s *BalancerService) getConfig(name string, inst uint32) *BalancerConfig {
	key := instanceKey{name: name, dataplaneInstance: inst}

	cfg := s.configs[key]
	return cfg
}

func (s *BalancerService) getConfigCopy(name string, inst uint32) *BalancerConfig {
	cfg := s.getConfig(name, inst)
	if cfg != nil {
		return cfg.DeepCopy()
	}
	return new(BalancerConfig)
}

func (s *BalancerService) setConfig(name string, inst uint32, cfg *BalancerConfig) {
	key := instanceKey{name: name, dataplaneInstance: inst}
	s.configs[key] = cfg
}

// Lists existing configurations per dataplane instance.
func (s *BalancerService) ListConfigs(
	_ context.Context,
	_ *balancerpb.ListConfigsRequest,
) (*balancerpb.ListConfigsResponse, error) {
	response := &balancerpb.ListConfigsResponse{
		InstanceConfigs: make([]*balancerpb.InstanceConfigs, len(s.agents)),
	}
	for inst := range s.agents {
		response.InstanceConfigs[inst] = &balancerpb.InstanceConfigs{
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

func forwardingMethodToProto(forwardingMethod ForwardingMethod) balancerpb.ForwardingMethod {
	switch forwardingMethod {
	case TUN:
		return balancerpb.ForwardingMethod_FORWARDING_METHOD_TUN
	case GRE:
		return balancerpb.ForwardingMethod_FORWARDING_METHOD_GRE
	}
	return balancerpb.ForwardingMethod_FORWARDING_METHOD_UNSPECIFIED
}

func forwardingMethodFromProto(forwardingMethod balancerpb.ForwardingMethod) (ForwardingMethod, error) {
	switch forwardingMethod {
	case balancerpb.ForwardingMethod_FORWARDING_METHOD_TUN:
		return TUN, nil
	case balancerpb.ForwardingMethod_FORWARDING_METHOD_GRE:
		return GRE, nil
	}
	return "", fmt.Errorf("Unknown forwarding method: %v", forwardingMethod)
}

// ShowConfig returns the current configuration of the balancer module.
func (s *BalancerService) ShowConfig(
	_ context.Context,
	req *balancerpb.ShowConfigRequest,
) (*balancerpb.ShowConfigResponse, error) {
	name, inst, err := req.GetTarget().Validate(uint32(len(s.agents)))
	if err != nil {
		return nil, err
	}

	response := &balancerpb.ShowConfigResponse{Instance: inst}

	s.mu.Lock()
	defer s.mu.Unlock()

	config := s.getConfig(name, inst)
	if config == nil {
		return response, nil
	}

	response.Config = &balancerpb.Config{
		StateConfig: &balancerpb.StateConfig{
			TcpSynAckTtl: config.StateConfig.TcpSynAckTtl,
			TcpSynTtl:    config.StateConfig.TcpSynTtl,
			TcpFinTtl:    config.StateConfig.TcpFinTtl,
			TcpTtl:       config.StateConfig.TcpTtl,
			UdpTtl:       config.StateConfig.UdpTtl,
			DefaultTtl:   config.StateConfig.DefaultTtl,
		},
		Services: make([]*balancerpb.Service, 0, len(config.Services)),
	}
	for _, s := range config.Services {
		service := balancerpb.Service{
			Addr:             s.Addr.AsSlice(),
			Prefixes:         make([]*balancerpb.Prefix, 0, len(s.Prefixes)),
			Reals:            make([]*balancerpb.Real, 0, len(s.Reals)),
			ForwardingMethod: forwardingMethodToProto(s.ForwardingMethod),
		}
		for _, p := range s.Prefixes {
			service.Prefixes = append(service.Prefixes, &balancerpb.Prefix{
				Addr: p.Addr().AsSlice(),
				Size: uint32(p.Bits()),
			})
		}
		for _, r := range s.Reals {
			service.Reals = append(service.Reals, &balancerpb.Real{
				Weight:  uint32(r.Weight),
				DstAddr: r.DstAddr.AsSlice(),
				SrcAddr: r.SrcAddr.AsSlice(),
				SrcMask: r.SrcMask.AsSlice(),
			})
		}
		response.Config.Services = append(response.Config.Services, &service)
	}
	return response, nil
}

// AddService a new virtual service to the configuration.
func (s *BalancerService) AddService(
	_ context.Context,
	req *balancerpb.AddServiceRequest,
) (*balancerpb.AddServiceResponse, error) {
	name, inst, err := req.GetTarget().Validate(uint32(len(s.agents)))
	if err != nil {
		return nil, err
	}

	forwardingMethod, err := forwardingMethodFromProto(req.GetService().GetForwardingMethod())
	newService := Service{
		Prefixes:         make([]netip.Prefix, 0, len(req.GetService().GetPrefixes())),
		Reals:            make([]Real, 0, len(req.GetService().GetReals())),
		ForwardingMethod: forwardingMethod,
	}
	var ok bool
	newService.Addr, ok = netip.AddrFromSlice(req.GetService().GetAddr())
	if !ok {
		return nil, errors.New("Address invalid")
	}
	for _, p := range req.GetService().GetPrefixes() {
		addr, ok := netip.AddrFromSlice(p.GetAddr())
		if !ok {
			return nil, errors.New("Prefix address invalid")
		}
		prefix := netip.PrefixFrom(addr, int(p.GetSize()))
		if prefix.Bits() == -1 {
			return nil, errors.New("Prefix size invalid")
		}
		newService.Prefixes = append(newService.Prefixes, prefix.Masked())
	}

	for _, r := range req.GetService().GetReals() {
		if r.GetWeight() > math.MaxUint16 {
			return nil, errors.New("Real weight is too big")
		}
		real := Real{
			Weight: uint16(r.GetWeight()),
		}
		real.DstAddr, ok = netip.AddrFromSlice(r.GetDstAddr())
		if !ok {
			return nil, errors.New("Real dst addr invalid")
		}
		real.SrcAddr, ok = netip.AddrFromSlice(r.GetSrcAddr())
		if !ok {
			return nil, errors.New("Real src addr invalid")
		}
		real.SrcMask, ok = netip.AddrFromSlice(r.GetSrcMask())
		if !ok {
			return nil, errors.New("Real src mask invalid")
		}
		newService.Reals = append(newService.Reals, real)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cfg := s.getConfigCopy(name, inst)
	cfg.Services = append(cfg.Services, newService)

	if err := s.updateModuleConfig(name, inst, cfg); err != nil {
		return nil, fmt.Errorf("failed to update module config: %w", err)
	}
	s.log.Infow("successfully added service",
		zap.String("name", name),
		zap.Uint32("instance", inst),
		zap.Stringer("address", newService.Addr),
		zap.Stringer("forwarding_method", newService.ForwardingMethod),
		zap.Int("reals_count", len(newService.Reals)),
		zap.Int("prefixes_count", len(newService.Prefixes)),
	)
	return new(balancerpb.AddServiceResponse), nil
}

// RemoveService removes a virtual service from the configuration.
func (s *BalancerService) RemoveService(
	_ context.Context,
	req *balancerpb.RemoveServiceRequest,
) (*balancerpb.RemoveServiceResponse, error) {
	name, inst, err := req.GetTarget().Validate(uint32(len(s.agents)))
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cfg := s.getConfigCopy(name, inst)

	addr, ok := netip.AddrFromSlice(req.GetServiceAddr())
	if !ok {
		return nil, errors.New("Service Address invalid")
	}
	var found bool
	for i, s := range cfg.Services {
		if s.Addr == addr {
			found = true
			cfg.Services = append(cfg.Services[:i], cfg.Services[i+1:]...)
			break
		}
	}
	if !found {
		s.log.Warnw("remove service: service not found",
			zap.String("name", name),
			zap.Uint32("instance", inst),
			zap.Stringer("address", addr),
		)
		return new(balancerpb.RemoveServiceResponse), nil
	}

	if err := s.updateModuleConfig(name, inst, cfg); err != nil {
		return nil, fmt.Errorf("failed to update module config: %w", err)
	}
	s.log.Infow("successfully removed service",
		zap.String("name", name),
		zap.Uint32("instance", inst),
		zap.Stringer("address", addr),
	)
	return new(balancerpb.RemoveServiceResponse), nil
}

// SetRealWeight sets weight a real server.
func (s *BalancerService) SetRealWeight(
	_ context.Context,
	req *balancerpb.SetRealWeightRequest,
) (*balancerpb.SetRealWeightResponse, error) {
	name, inst, err := req.GetTarget().Validate(uint32(len(s.agents)))
	if err != nil {
		return nil, err
	}

	if req.GetWeight() > math.MaxUint16 {
		return nil, errors.New("Real weight is too big")
	}
	weight := uint16(req.GetWeight())

	serviceAddr, ok := netip.AddrFromSlice(req.GetServiceAddr())
	if !ok {
		return nil, errors.New("Service Address invalid")
	}

	realAddr, ok := netip.AddrFromSlice(req.GetRealAddr())
	if !ok {
		return nil, errors.New("Real Address invalid")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cfg := s.getConfigCopy(name, inst)

	var found bool
	for i, s := range cfg.Services {
		if s.Addr == serviceAddr {
			for j, r := range s.Reals {
				if r.DstAddr == realAddr {
					found = true
					s.Reals[i].Weight = weight
					if err := cfg.ModuleConfig.UpdateRealWeight(i, j, weight); err != nil {
						return nil, fmt.Errorf("failed to update real weight: %w", err)
					}
					break
				}
			}
			if found {
				break
			}
		}
	}
	if !found {
		return nil, errors.New("failed to update real weight: real not found")
	}
	s.setConfig(name, inst, cfg)
	return new(balancerpb.SetRealWeightResponse), nil
}

// SetStateConfig sets state TTLs config to the configuration.
func (s *BalancerService) SetStateConfig(
	_ context.Context,
	req *balancerpb.SetStateConfigRequest,
) (*balancerpb.SetStateConfigResponse, error) {
	name, inst, err := req.GetTarget().Validate(uint32(len(s.agents)))
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cfg := s.getConfigCopy(name, inst)

	cfg.StateConfig = StateConfig{
		TcpSynAckTtl: req.GetStateConfig().TcpSynAckTtl,
		TcpSynTtl:    req.GetStateConfig().TcpSynTtl,
		TcpFinTtl:    req.GetStateConfig().TcpFinTtl,
		TcpTtl:       req.GetStateConfig().TcpTtl,
		UdpTtl:       req.GetStateConfig().UdpTtl,
		DefaultTtl:   req.GetStateConfig().DefaultTtl,
	}

	if err := s.updateModuleConfig(name, inst, cfg); err != nil {
		return nil, fmt.Errorf("failed to update module config: %w", err)
	}

	s.log.Infow("successfully set state config",
		zap.String("name", name),
		zap.Uint32("instance", inst),
		zap.Uint32("tcp_syn_ack_ttl", cfg.StateConfig.TcpSynAckTtl),
		zap.Uint32("tcp_syn_ttl", cfg.StateConfig.TcpSynTtl),
		zap.Uint32("tcp_fin_ttl", cfg.StateConfig.TcpFinTtl),
		zap.Uint32("tcp_ttl", cfg.StateConfig.TcpTtl),
		zap.Uint32("udp_ttl", cfg.StateConfig.UdpTtl),
		zap.Uint32("default_ttl", cfg.StateConfig.DefaultTtl),
	)

	return &balancerpb.SetStateConfigResponse{}, nil
}

func (s *BalancerService) updateModuleConfig(
	name string,
	inst uint32,
	cfg *BalancerConfig,
) error {
	s.log.Debugw("updating configuration",
		zap.String("module", name),
		zap.Uint32("instance", inst),
	)

	if int(inst) >= len(s.agents) {
		return fmt.Errorf("instance index %d is out of range (agents length: %d)", inst, len(s.agents))
	}
	agent := s.agents[inst]
	if agent == nil {
		return fmt.Errorf("agent for instance %d is nil", inst)
	}

	moduleConfig, err := NewModuleConfig(agent, name)
	if err != nil {
		return fmt.Errorf("failed to create module config for instance %d: %w", inst, err)
	}

	for _, service := range cfg.Services {
		if err := moduleConfig.AddService(service); err != nil {
			return fmt.Errorf("failed to add prefix on instance %d: %w", inst, err)
		}
	}
	moduleConfig.SetStateConfig(cfg.StateConfig)

	if err := agent.UpdateModules([]ffi.ModuleConfig{moduleConfig.AsFFIModule()}); err != nil {
		return fmt.Errorf("failed to update module on instance %d: %w", inst, err)
	}

	cfg.ModuleConfig = moduleConfig
	s.setConfig(name, inst, cfg)
	s.log.Debugw("successfully updated module config",
		zap.String("name", name),
		zap.Uint32("instance", inst),
	)

	return nil
}
