package nat64

import (
	"context"
	"fmt"
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
	agent   *ffi.Agent
	log     *zap.SugaredLogger
	configs map[string]*NAT64Config
}

// NAT64Config represents the configuration for a NAT64 instance
type NAT64Config struct {
	Prefixes           [][]byte
	Mappings           []Mapping
	MTU                MTUConfig
	DropUnknownPrefix  bool
	DropUnknownMapping bool
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

func NewNAT64Service(agent *ffi.Agent, log *zap.SugaredLogger) *NAT64Service {
	return &NAT64Service{
		agent:   agent,
		log:     log,
		configs: make(map[string]*NAT64Config),
	}
}

func (m *NAT64Service) ListConfigs(ctx context.Context, req *nat64pb.ListConfigsRequest) (*nat64pb.ListConfigsResponse, error) {
	response := &nat64pb.ListConfigsResponse{
		Configs: make([]string, 0),
	}

	// Lock instances store and module updates
	m.mu.Lock()
	defer m.mu.Unlock()

	for name := range m.configs {
		response.Configs = append(response.Configs, name)
	}

	return response, nil
}

func (m *NAT64Service) ShowConfig(ctx context.Context, req *nat64pb.ShowConfigRequest) (*nat64pb.ShowConfigResponse, error) {
	name, err := req.GetTarget().Validate()
	if err != nil {
		return nil, err
	}

	response := &nat64pb.ShowConfigResponse{}

	m.mu.Lock()
	defer m.mu.Unlock()

	config := m.configs[name]
	if config != nil {
		response.Config = &nat64pb.Config{
			Prefixes: make([]*nat64pb.Prefix, 0, len(config.Prefixes)),
			Mappings: make([]*nat64pb.Mapping, 0, len(config.Mappings)),
			Mtu: &nat64pb.MTUConfig{
				Ipv4Mtu: config.MTU.IPv4MTU,
				Ipv6Mtu: config.MTU.IPv6MTU,
			},
			DropUnknownPrefix:  config.DropUnknownPrefix,
			DropUnknownMapping: config.DropUnknownMapping,
		}

		for _, prefix := range config.Prefixes {
			response.Config.Prefixes = append(response.Config.Prefixes, &nat64pb.Prefix{
				Prefix: prefix,
			})
		}

		for _, mapping := range config.Mappings {
			response.Config.Mappings = append(response.Config.Mappings, &nat64pb.Mapping{
				Ipv4:        mapping.IPv4,
				Ipv6:        mapping.IPv6,
				PrefixIndex: mapping.PrefixIndex,
			})
		}
	}

	return response, nil
}
func (m *NAT64Service) AddPrefix(ctx context.Context, req *nat64pb.AddPrefixRequest) (*nat64pb.AddPrefixResponse, error) {
	if len(req.Prefix) != 12 {
		return nil, status.Errorf(codes.InvalidArgument, "invalid prefix length: got %d, want 12", len(req.Prefix))
	}

	name, err := req.GetTarget().Validate()
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Update in-memory config
	config := m.configs[name]
	if config == nil {
		config = &NAT64Config{}
		m.configs[name] = config
	}

	config.Prefixes = append(config.Prefixes, req.Prefix)

	// Update module config
	if err := m.updateModuleConfig(name); err != nil {
		return nil, fmt.Errorf("failed to update module config: %w", err)
	}

	m.log.Infow("successfully added prefix",
		zap.String("name", name),
		zap.Binary("prefix", req.Prefix),
	)

	return &nat64pb.AddPrefixResponse{}, nil
}

func (m *NAT64Service) AddMapping(ctx context.Context, req *nat64pb.AddMappingRequest) (*nat64pb.AddMappingResponse, error) {
	if len(req.Ipv4) != 4 {
		return nil, status.Errorf(codes.InvalidArgument, "invalid IPv4 address length: got %d, want 4", len(req.Ipv4))
	}
	if len(req.Ipv6) != 16 {
		return nil, status.Errorf(codes.InvalidArgument, "invalid IPv6 address length: got %d, want 16", len(req.Ipv6))
	}

	name, err := req.GetTarget().Validate()
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Update in-memory config
	config := m.configs[name]
	if config == nil {
		config = &NAT64Config{}
		m.configs[name] = config
	}

	config.Mappings = append(config.Mappings, Mapping{
		IPv4:        req.Ipv4,
		IPv6:        req.Ipv6,
		PrefixIndex: req.PrefixIndex,
	})

	// Update module config
	if err := m.updateModuleConfig(name); err != nil {
		return nil, fmt.Errorf("failed to update module config: %w", err)
	}

	m.log.Infow("successfully added mapping",
		zap.String("name", name),
		zap.Binary("ipv4", req.Ipv4),
		zap.Binary("ipv6", req.Ipv6),
		zap.Uint32("prefix_index", req.PrefixIndex),
	)

	return &nat64pb.AddMappingResponse{}, nil
}

func (m *NAT64Service) SetMTU(ctx context.Context, req *nat64pb.SetMTURequest) (*nat64pb.SetMTUResponse, error) {
	if req.Mtu == nil {
		return nil, status.Error(codes.InvalidArgument, "mtu config is required")
	}

	name, err := req.GetTarget().Validate()
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Update in-memory config
	config := m.configs[name]
	if config == nil {
		config = &NAT64Config{}
		m.configs[name] = config
	}

	config.MTU = MTUConfig{
		IPv4MTU: req.Mtu.Ipv4Mtu,
		IPv6MTU: req.Mtu.Ipv6Mtu,
	}

	// Update module config
	if err := m.updateModuleConfig(name); err != nil {
		return nil, fmt.Errorf("failed to update module config: %w", err)
	}

	m.log.Infow("successfully set MTU",
		zap.String("name", name),
		zap.Uint32("ipv4_mtu", req.Mtu.Ipv4Mtu),
		zap.Uint32("ipv6_mtu", req.Mtu.Ipv6Mtu),
	)

	return &nat64pb.SetMTUResponse{}, nil
}

func (m *NAT64Service) SetDropUnknown(ctx context.Context, req *nat64pb.SetDropUnknownRequest) (*nat64pb.SetDropUnknownResponse, error) {
	name, err := req.GetTarget().Validate()
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Update in-memory config
	config := m.configs[name]
	if config == nil {
		config = &NAT64Config{}
		m.configs[name] = config
	}

	config.DropUnknownPrefix = req.DropUnknownPrefix
	config.DropUnknownMapping = req.DropUnknownMapping

	// Update module config
	if err := m.updateModuleConfig(name); err != nil {
		return nil, fmt.Errorf("failed to update module config: %w", err)
	}

	m.log.Infow("successfully set drop unknown flags",
		zap.String("name", name),
		zap.Bool("drop_unknown_prefix", req.DropUnknownPrefix),
		zap.Bool("drop_unknown_mapping", req.DropUnknownMapping),
	)

	return &nat64pb.SetDropUnknownResponse{}, nil
}

func (m *NAT64Service) updateModuleConfig(name string) error {
	moduleConfig, err := NewModuleConfig(m.agent, name)
	if err != nil {
		return fmt.Errorf("failed to create module config: %w", err)
	}

	config := m.configs[name]
	if config == nil {
		config = &NAT64Config{}
		m.configs[name] = config
	}

	// Configure all prefixes.
	for _, prefix := range config.Prefixes {
		if err := moduleConfig.AddPrefix(prefix); err != nil {
			return fmt.Errorf("failed to add prefix: %w", err)
		}
	}

	// Configure all mappings.
	for _, mapping := range config.Mappings {
		if err := moduleConfig.AddMapping(mapping.IPv4, mapping.IPv6, mapping.PrefixIndex); err != nil {
			return fmt.Errorf("failed to add mapping: %w", err)
		}
	}

	// Set drop unknown flags.
	if err := moduleConfig.SetDropUnknown(config.DropUnknownPrefix, config.DropUnknownMapping); err != nil {
		return fmt.Errorf("failed to set drop unknown flags: %w", err)
	}

	if err := m.agent.UpdateModules([]ffi.ModuleConfig{moduleConfig.AsFFIModule()}); err != nil {
		return fmt.Errorf("failed to update module: %w", err)
	}

	return nil
}
