package nat64

import (
	"bytes"
	"context"
	"maps"
	"math"
	"net/netip"
	"slices"
	"sync"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/modules/nat64/controlplane/nat64pb"
)

// NAT64ServiceOption configures the NAT64Service constructor.
type NAT64ServiceOption func(*nat64ServiceOptions)

type nat64ServiceOptions struct {
	Log *zap.Logger
}

func newNAT64ServiceOptions() *nat64ServiceOptions {
	return &nat64ServiceOptions{
		Log: zap.NewNop(),
	}
}

// WithNAT64ServiceLog sets the logger for the NAT64Service.
func WithNAT64ServiceLog(log *zap.Logger) NAT64ServiceOption {
	return func(o *nat64ServiceOptions) {
		o.Log = log
	}
}

// NAT64Service implements the NAT64 gRPC service
type NAT64Service struct {
	nat64pb.UnimplementedNAT64ServiceServer

	mu      sync.Mutex
	backend Backend
	configs map[string]config
	log     *zap.Logger
}

type config struct {
	config NAT64Config
	module ModuleHandle
}

func (m config) Clone() config {
	return config{
		config: m.config.Clone(),
		module: m.module,
	}
}

// NAT64Config represents the configuration for a NAT64 instance
type NAT64Config struct {
	Prefixes           [][]byte
	Mappings           []Mapping
	MTU                MTUConfig
	DropUnknownPrefix  bool
	DropUnknownMapping bool
}

func (m NAT64Config) Clone() NAT64Config {
	cfg := NAT64Config{
		Prefixes:           make([][]byte, len(m.Prefixes)),
		Mappings:           slices.Clone(m.Mappings),
		MTU:                m.MTU,
		DropUnknownPrefix:  m.DropUnknownPrefix,
		DropUnknownMapping: m.DropUnknownMapping,
	}
	for idx := range m.Prefixes {
		cfg.Prefixes[idx] = slices.Clone(m.Prefixes[idx])
	}
	return cfg
}

// Mapping represents an IPv4-IPv6 address mapping
type Mapping struct {
	IPv4        netip.Addr
	IPv6        netip.Addr
	PrefixIndex uint32
}

// MTUConfig represents MTU configuration
type MTUConfig struct {
	IPv4MTU uint32
	IPv6MTU uint32
}

const (
	defaultIPv4MTU uint32 = 1450
	defaultIPv6MTU uint32 = 1280
)

func defaultNAT64Config() NAT64Config {
	return NAT64Config{
		MTU: MTUConfig{
			IPv4MTU: defaultIPv4MTU,
			IPv6MTU: defaultIPv6MTU,
		},
	}
}

func NewNAT64Service(backend Backend, options ...NAT64ServiceOption) *NAT64Service {
	opts := newNAT64ServiceOptions()
	for _, o := range options {
		o(opts)
	}

	return &NAT64Service{
		backend: backend,
		log:     opts.Log,
		configs: map[string]config{},
	}
}

func (m *NAT64Service) ListConfigs(ctx context.Context, req *nat64pb.ListConfigsRequest) (*nat64pb.ListConfigsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return &nat64pb.ListConfigsResponse{
		Configs: slices.Sorted(maps.Keys(m.configs)),
	}, nil
}

func (m *NAT64Service) ShowConfig(ctx context.Context, req *nat64pb.ShowConfigRequest) (*nat64pb.ShowConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	response := &nat64pb.ShowConfigResponse{}

	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.configs[name]
	if !ok {
		return nil, status.Error(codes.NotFound, "config not found")
	}

	cfg := inst.config
	response.Config = &nat64pb.Config{
		Prefixes: make([]*nat64pb.Prefix, 0, len(cfg.Prefixes)),
		Mappings: make([]*nat64pb.Mapping, 0, len(cfg.Mappings)),
		Mtu: &nat64pb.MTUConfig{
			Ipv4Mtu: cfg.MTU.IPv4MTU,
			Ipv6Mtu: cfg.MTU.IPv6MTU,
		},
		DropUnknownPrefix:  cfg.DropUnknownPrefix,
		DropUnknownMapping: cfg.DropUnknownMapping,
	}

	for _, prefix := range cfg.Prefixes {
		response.Config.Prefixes = append(response.Config.Prefixes, &nat64pb.Prefix{
			Prefix: slices.Clone(prefix),
		})
	}

	for _, mapping := range cfg.Mappings {
		response.Config.Mappings = append(response.Config.Mappings, &nat64pb.Mapping{
			Ipv4:        commonpb.NewIPAddressFromAddr(mapping.IPv4),
			Ipv6:        commonpb.NewIPAddressFromAddr(mapping.IPv6),
			PrefixIndex: mapping.PrefixIndex,
		})
	}

	return response, nil
}
func (m *NAT64Service) AddPrefix(ctx context.Context, req *nat64pb.AddPrefixRequest) (*nat64pb.AddPrefixResponse, error) {
	if len(req.Prefix) != 12 {
		return nil, status.Errorf(codes.InvalidArgument, "invalid prefix length: got %d, want 12", len(req.Prefix))
	}

	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	inst := m.instanceFor(name).Clone()
	inst.config.Prefixes = append(inst.config.Prefixes, slices.Clone(req.Prefix))

	if err := m.updateModuleConfig(name, inst); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update module config: %v", err)
	}

	return &nat64pb.AddPrefixResponse{}, nil
}

func (m *NAT64Service) RemovePrefix(ctx context.Context, req *nat64pb.RemovePrefixRequest) (*nat64pb.RemovePrefixResponse, error) {
	if len(req.Prefix) != 12 {
		return nil, status.Errorf(codes.InvalidArgument, "invalid prefix length: got %d, want 12", len(req.Prefix))
	}

	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.configs[name]
	if !ok {
		return &nat64pb.RemovePrefixResponse{}, nil
	}
	next := inst.Clone()

	removeIdx := -1
	for idx, prefix := range next.config.Prefixes {
		if bytes.Equal(prefix, req.Prefix) {
			removeIdx = idx
			break
		}
	}
	if removeIdx == -1 {
		return &nat64pb.RemovePrefixResponse{}, nil
	}

	next.config.Prefixes = slices.Delete(next.config.Prefixes, removeIdx, removeIdx+1)
	next.config.Mappings = adjustMappingsAfterPrefixRemove(next.config.Mappings, uint32(removeIdx))

	if err := m.updateModuleConfig(name, next); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update module config: %v", err)
	}

	return &nat64pb.RemovePrefixResponse{}, nil
}

func (m *NAT64Service) AddMapping(ctx context.Context, req *nat64pb.AddMappingRequest) (*nat64pb.AddMappingResponse, error) {
	ipv4, err := req.GetIpv4().ToAddr()
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid ipv4 (bytes=%x): %v", req.GetIpv4().GetAddr(), err)
	}
	if !ipv4.Is4() {
		return nil, status.Errorf(codes.InvalidArgument, "ipv4 %q is not an IPv4 address", ipv4)
	}
	ipv6, err := req.GetIpv6().ToAddr()
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid ipv6 (bytes=%x): %v", req.GetIpv6().GetAddr(), err)
	}
	if !ipv6.Is6() || ipv6.Is4In6() {
		return nil, status.Errorf(codes.InvalidArgument, "ipv6 %q is not a pure IPv6 address", ipv6)
	}

	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	inst := m.instanceFor(name).Clone()
	if req.PrefixIndex >= uint32(len(inst.config.Prefixes)) {
		return nil, status.Errorf(
			codes.InvalidArgument,
			"invalid prefix index: got %d, prefixes count %d",
			req.PrefixIndex,
			len(inst.config.Prefixes),
		)
	}
	inst.config.Mappings = append(inst.config.Mappings, Mapping{
		IPv4:        ipv4,
		IPv6:        ipv6,
		PrefixIndex: req.PrefixIndex,
	})

	if err := m.updateModuleConfig(name, inst); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update module config: %v", err)
	}

	return &nat64pb.AddMappingResponse{}, nil
}

func (m *NAT64Service) RemoveMapping(ctx context.Context, req *nat64pb.RemoveMappingRequest) (*nat64pb.RemoveMappingResponse, error) {
	ipv4, err := req.GetIpv4().ToAddr()
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid ipv4 (bytes=%x): %v", req.GetIpv4().GetAddr(), err)
	}
	if !ipv4.Is4() {
		return nil, status.Errorf(codes.InvalidArgument, "ipv4 %q is not an IPv4 address", ipv4)
	}

	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.configs[name]
	if !ok {
		return &nat64pb.RemoveMappingResponse{}, nil
	}
	next := inst.Clone()

	next.config.Mappings = slices.DeleteFunc(next.config.Mappings, func(mapping Mapping) bool {
		return mapping.IPv4 == ipv4
	})
	if len(next.config.Mappings) == len(inst.config.Mappings) {
		return &nat64pb.RemoveMappingResponse{}, nil
	}

	if err := m.updateModuleConfig(name, next); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update module config: %v", err)
	}

	return &nat64pb.RemoveMappingResponse{}, nil
}

func (m *NAT64Service) SetMTU(ctx context.Context, req *nat64pb.SetMTURequest) (*nat64pb.SetMTUResponse, error) {
	if req.Mtu == nil {
		return nil, status.Error(codes.InvalidArgument, "mtu config is required")
	}
	if req.Mtu.Ipv4Mtu > math.MaxUint16 {
		return nil, status.Errorf(codes.InvalidArgument, "invalid IPv4 MTU: got %d, max %d", req.Mtu.Ipv4Mtu, math.MaxUint16)
	}
	if req.Mtu.Ipv6Mtu > math.MaxUint16 {
		return nil, status.Errorf(codes.InvalidArgument, "invalid IPv6 MTU: got %d, max %d", req.Mtu.Ipv6Mtu, math.MaxUint16)
	}

	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	inst := m.instanceFor(name).Clone()
	inst.config.MTU = MTUConfig{
		IPv4MTU: req.Mtu.Ipv4Mtu,
		IPv6MTU: req.Mtu.Ipv6Mtu,
	}

	if err := m.updateModuleConfig(name, inst); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update module config: %v", err)
	}

	return &nat64pb.SetMTUResponse{}, nil
}

func (m *NAT64Service) SetDropUnknown(ctx context.Context, req *nat64pb.SetDropUnknownRequest) (*nat64pb.SetDropUnknownResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	inst := m.instanceFor(name).Clone()
	inst.config.DropUnknownPrefix = req.DropUnknownPrefix
	inst.config.DropUnknownMapping = req.DropUnknownMapping

	if err := m.updateModuleConfig(name, inst); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update module config: %v", err)
	}

	return &nat64pb.SetDropUnknownResponse{}, nil
}

func (m *NAT64Service) instanceFor(name string) config {
	inst, ok := m.configs[name]
	if !ok {
		return config{
			config: defaultNAT64Config(),
		}
	}
	return inst
}

func (m *NAT64Service) updateModuleConfig(name string, inst config) error {
	module, err := m.backend.UpdateModule(name, &inst.config)
	if err != nil {
		return err
	}

	if inst.module != nil {
		inst.module.Free()
	}
	inst.module = module
	m.configs[name] = inst

	return nil
}

func adjustMappingsAfterPrefixRemove(mappings []Mapping, removed uint32) []Mapping {
	out := make([]Mapping, 0, len(mappings))
	for _, mapping := range mappings {
		switch {
		case mapping.PrefixIndex == removed:
			continue
		case mapping.PrefixIndex > removed:
			mapping.PrefixIndex--
		}
		out = append(out, mapping)
	}
	return out
}
