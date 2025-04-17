package nat64

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/nat64/controlplane/nat64pb"
)

// NAT64Service implements the NAT64 gRPC service
type NAT64Service struct {
	nat64pb.UnimplementedNAT64ServiceServer

	mu      sync.Mutex
	agents  []*ffi.Agent
	log     *zap.SugaredLogger
	configs map[instanceKey]*NAT64Config
}

// NAT64Config represents the configuration for a NAT64 instance
type NAT64Config struct {
	Prefixes     [][]byte
	Mappings     []Mapping
	MTU          MTUConfig
	OptionsLimit uint32
}

// Mapping represents an IPv4-IPv6 address mapping
type Mapping struct {
	IPv4        []byte
	IPv6        []byte
	PrefixIndex uint32
}

// MTUConfig represents MTU configuration
type MTUConfig struct {
	IPv4MTU uint32
	IPv6MTU uint32
}

func NewNAT64Service(agents []*ffi.Agent, log *zap.SugaredLogger) *NAT64Service {
	return &NAT64Service{
		agents:  agents,
		log:     log,
		configs: make(map[instanceKey]*NAT64Config),
	}
}

func (s *NAT64Service) ShowConfig(ctx context.Context, req *nat64pb.ShowConfigRequest) (*nat64pb.ShowConfigResponse, error) {
	numaIndices, err := s.getNUMAIndices(req.Target.Numa)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	configs := make([]*nat64pb.InstanceConfig, 0)
	for key, config := range s.configs {
		// Skip if:
		// - NUMA is not in the requested list
		// - ModuleName is set and doesn't match current config
		if !slices.Contains(numaIndices, key.numaIdx) || (req.Target.ModuleName != "" && key.name != req.Target.ModuleName) {
			continue
		}

		instanceConfig := &nat64pb.InstanceConfig{
			Numa:     key.numaIdx,
			Prefixes: make([]*nat64pb.Prefix, 0, len(config.Prefixes)),
			Mappings: make([]*nat64pb.Mapping, 0, len(config.Mappings)),
			Mtu: &nat64pb.MTUConfig{
				Ipv4Mtu: config.MTU.IPv4MTU,
				Ipv6Mtu: config.MTU.IPv6MTU,
			},
		}

		for _, prefix := range config.Prefixes {
			instanceConfig.Prefixes = append(instanceConfig.Prefixes, &nat64pb.Prefix{
				Prefix: prefix,
			})
		}

		for _, mapping := range config.Mappings {
			instanceConfig.Mappings = append(instanceConfig.Mappings, &nat64pb.Mapping{
				Ipv4:        mapping.IPv4,
				Ipv6:        mapping.IPv6,
				PrefixIndex: mapping.PrefixIndex,
			})
		}

		configs = append(configs, instanceConfig)
	}

	return &nat64pb.ShowConfigResponse{
		Configs: configs,
	}, nil
}
func (s *NAT64Service) AddPrefix(ctx context.Context, req *nat64pb.AddPrefixRequest) (*nat64pb.AddPrefixResponse, error) {
	if len(req.Prefix) != 12 {
		return nil, status.Errorf(codes.InvalidArgument, "invalid prefix length: got %d, want 12", len(req.Prefix))
	}

	numaIndices, err := s.getNUMAIndices(req.Target.Numa)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Update in-memory configs
	for _, numaIdx := range numaIndices {
		key := instanceKey{name: req.Target.ModuleName, numaIdx: numaIdx}
		config := s.configs[key]
		if config == nil {
			config = &NAT64Config{}
			s.configs[key] = config
		}

		config.Prefixes = append(config.Prefixes, req.Prefix)
	}

	s.log.Infow("added prefix",
		zap.String("name", req.Target.ModuleName),
		zap.Binary("prefix", req.Prefix),
		zap.Uint32s("numa", numaIndices),
	)
	// Update module configs
	if err := s.updateModuleConfigs(req.Target.ModuleName, numaIndices); err != nil {
		return nil, fmt.Errorf("failed to update module configs: %w", err)
	}

	s.log.Infow("successfully added prefix",
		zap.String("name", req.Target.ModuleName),
		zap.Binary("prefix", req.Prefix),
		zap.Uint32s("numa", numaIndices),
	)

	return &nat64pb.AddPrefixResponse{}, nil
}

func (s *NAT64Service) AddMapping(ctx context.Context, req *nat64pb.AddMappingRequest) (*nat64pb.AddMappingResponse, error) {
	if len(req.Ipv4) != 4 {
		return nil, status.Errorf(codes.InvalidArgument, "invalid IPv4 address length: got %d, want 4", len(req.Ipv4))
	}
	if len(req.Ipv6) != 16 {
		return nil, status.Errorf(codes.InvalidArgument, "invalid IPv6 address length: got %d, want 16", len(req.Ipv6))
	}

	numaIndices, err := s.getNUMAIndices(req.Target.Numa)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Update in-memory configs
	for _, numaIdx := range numaIndices {
		key := instanceKey{name: req.Target.ModuleName, numaIdx: numaIdx}
		config := s.configs[key]
		if config == nil {
			config = &NAT64Config{}
			s.configs[key] = config
		}

		config.Mappings = append(config.Mappings, Mapping{
			IPv4:        req.Ipv4,
			IPv6:        req.Ipv6,
			PrefixIndex: req.PrefixIndex,
		})
	}

	// Update module configs
	if err := s.updateModuleConfigs(req.Target.ModuleName, numaIndices); err != nil {
		return nil, fmt.Errorf("failed to update module configs: %w", err)
	}

	s.log.Infow("successfully added mapping",
		zap.String("name", req.Target.ModuleName),
		zap.Binary("ipv4", req.Ipv4),
		zap.Binary("ipv6", req.Ipv6),
		zap.Uint32("prefix_index", req.PrefixIndex),
		zap.Uint32s("numa", numaIndices),
	)

	return &nat64pb.AddMappingResponse{}, nil
}

func (s *NAT64Service) SetMTU(ctx context.Context, req *nat64pb.SetMTURequest) (*nat64pb.SetMTUResponse, error) {
	if req.Mtu == nil {
		return nil, status.Error(codes.InvalidArgument, "mtu config is required")
	}

	numaIndices, err := s.getNUMAIndices(req.Target.Numa)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Update in-memory configs
	for _, numaIdx := range numaIndices {
		key := instanceKey{name: req.Target.ModuleName, numaIdx: numaIdx}
		config := s.configs[key]
		if config == nil {
			config = &NAT64Config{}
			s.configs[key] = config
		}

		config.MTU = MTUConfig{
			IPv4MTU: req.Mtu.Ipv4Mtu,
			IPv6MTU: req.Mtu.Ipv6Mtu,
		}
	}

	// Update module configs
	if err := s.updateModuleConfigs(req.Target.ModuleName, numaIndices); err != nil {
		return nil, fmt.Errorf("failed to update module configs: %w", err)
	}

	s.log.Infow("successfully set MTU",
		zap.String("name", req.Target.ModuleName),
		zap.Uint32("ipv4_mtu", req.Mtu.Ipv4Mtu),
		zap.Uint32("ipv6_mtu", req.Mtu.Ipv6Mtu),
		zap.Uint32s("numa", numaIndices),
	)

	return &nat64pb.SetMTUResponse{}, nil
}

func (s *NAT64Service) SetOptionsLimit(ctx context.Context, req *nat64pb.SetOptionsLimitRequest) (*nat64pb.SetOptionsLimitResponse, error) {
	if req.OptionsLimit == 0 {
		return nil, status.Error(codes.InvalidArgument, "options limit should be greater then zero")
	}

	numaIndices, err := s.getNUMAIndices(req.Target.Numa)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Update in-memory configs
	for _, numaIdx := range numaIndices {
		key := instanceKey{name: req.Target.ModuleName, numaIdx: numaIdx}
		config := s.configs[key]
		if config == nil {
			config = &NAT64Config{}
			s.configs[key] = config
		}

		config.OptionsLimit = req.OptionsLimit
	}

	// Update module configs
	if err := s.updateModuleConfigs(req.Target.ModuleName, numaIndices); err != nil {
		return nil, fmt.Errorf("failed to update module configs: %w", err)
	}

	s.log.Infow("successfully set OptionsLimit",
		zap.String("name", req.Target.ModuleName),
		zap.Uint32("options_limit", req.OptionsLimit),
		zap.Uint32s("numa", numaIndices),
	)

	return &nat64pb.SetOptionsLimitResponse{}, nil
}

func (s *NAT64Service) updateModuleConfigs(name string, numaIndices []uint32) error {
	s.log.Debugw("updating configuration",
		zap.String("module", name),
		zap.Uint32s("numa", numaIndices),
	)

	// Create module configs for each NUMA node
	configs := make([]*ModuleConfig, len(numaIndices))
	for i, numaIdx := range numaIndices {
		if int(numaIdx) >= len(s.agents) {
			return fmt.Errorf("NUMA index %d is out of range (agents length: %d)", numaIdx, len(s.agents))
		}
		agent := s.agents[numaIdx]
		if agent == nil {
			return fmt.Errorf("agent for NUMA %d is nil", numaIdx)
		}

		moduleConfig, err := NewModuleConfig(agent, name)
		if err != nil {
			return fmt.Errorf("failed to create module config for NUMA %d: %w", numaIdx, err)
		}

		key := instanceKey{name: name, numaIdx: numaIdx}
		config := s.configs[key]
		if config == nil {
			config = &NAT64Config{}
			s.configs[key] = config
		}

		// Configure all prefixes
		for _, prefix := range config.Prefixes {
			if err := moduleConfig.AddPrefix(prefix); err != nil {
				return fmt.Errorf("failed to add prefix on NUMA %d: %w", numaIdx, err)
			}
		}

		// Configure all mappings
		for _, mapping := range config.Mappings {
			if err := moduleConfig.AddMapping(mapping.IPv4, mapping.IPv6, mapping.PrefixIndex); err != nil {
				return fmt.Errorf("failed to add mapping on NUMA %d: %w", numaIdx, err)
			}
		}

		configs[i] = moduleConfig
	}

	// Apply all configurations
	for i, numaIdx := range numaIndices {
		agent := s.agents[numaIdx]
		config := configs[i]

		if err := agent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}); err != nil {
			return fmt.Errorf("failed to update module on NUMA %d: %w", numaIdx, err)
		}

		s.log.Debugw("successfully updated module config",
			zap.String("name", name),
			zap.Uint32("numa", numaIdx),
			zap.Int("prefix_count", len(s.configs[instanceKey{name: name, numaIdx: numaIdx}].Prefixes)),
			zap.Int("mapping_count", len(s.configs[instanceKey{name: name, numaIdx: numaIdx}].Mappings)),
		)
	}

	s.log.Infow("successfully updated all module configurations",
		zap.String("name", name),
		zap.Uint32s("numa", numaIndices),
	)

	return nil
}

func (s *NAT64Service) getNUMAIndices(requestedNuma []uint32) ([]uint32, error) {
	if len(requestedNuma) == 0 {
		indices := make([]uint32, len(s.agents))
		for i := range s.agents {
			indices[i] = uint32(i)
		}
		return indices, nil
	}

	// Validate requested NUMA indices
	for _, numa := range requestedNuma {
		if int(numa) >= len(s.agents) {
			return nil, status.Errorf(codes.InvalidArgument, "invalid NUMA index: %d", numa)
		}
	}

	return requestedNuma, nil
}
