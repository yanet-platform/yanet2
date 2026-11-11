package nat64

import (
	"bytes"
	"context"
	"fmt"
	"net/netip"
	"slices"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/crud"
	nat64pb "github.com/yanet-platform/yanet2/modules/nat64/controlplane/nat64pb/v1"
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

	core *crud.Core[*NAT64Config]
	log  *zap.Logger
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
	nat64PrefixBits  = 96
	nat64PrefixBytes = nat64PrefixBits / 8

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
		core: crud.NewCore(backend),
		log:  opts.Log,
	}
}

func (m *NAT64Service) ListConfigs(ctx context.Context, req *nat64pb.ListConfigsRequest) (*nat64pb.ListConfigsResponse, error) {
	return &nat64pb.ListConfigsResponse{
		Configs: m.core.List(),
	}, nil
}

func (m *NAT64Service) ShowConfig(ctx context.Context, req *nat64pb.ShowConfigRequest) (*nat64pb.ShowConfigResponse, error) {
	name := req.GetName()
	response := &nat64pb.ShowConfigResponse{}

	cfg, ok := m.core.Get(name)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	response.Config = &nat64pb.Config{
		Prefixes: make([]*commonpb.IPv6Prefix, 0, len(cfg.Prefixes)),
		Mappings: make([]*nat64pb.Mapping, 0, len(cfg.Mappings)),
		Mtu: &nat64pb.MTUConfig{
			Ipv4Mtu: cfg.MTU.IPv4MTU,
			Ipv6Mtu: cfg.MTU.IPv6MTU,
		},
		DropUnknownPrefix:  cfg.DropUnknownPrefix,
		DropUnknownMapping: cfg.DropUnknownMapping,
	}

	for _, prefix := range cfg.Prefixes {
		wirePrefix, err := encodeNAT64Prefix(prefix)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to encode prefix: %v", err)
		}
		response.Config.Prefixes = append(response.Config.Prefixes, wirePrefix)
	}

	for _, mapping := range cfg.Mappings {
		response.Config.Mappings = append(response.Config.Mappings, &nat64pb.Mapping{
			Ipv4:        commonpb.NewIPv4Address(mapping.IPv4.As4()),
			Ipv6:        commonpb.NewIPv6Address(mapping.IPv6.As16()),
			PrefixIndex: mapping.PrefixIndex,
		})
	}

	return response, nil
}

// UpdateConfig replaces the whole named config with the one the request
// carries, publishing nothing when that config is invalid.
func (m *NAT64Service) UpdateConfig(ctx context.Context, req *nat64pb.UpdateConfigRequest) (*nat64pb.UpdateConfigResponse, error) {
	config, err := decodeConfig(req.GetConfig())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	name := req.GetName()
	if err := m.core.Replace(name, config); err != nil {
		return nil, crud.PublishError(name, err)
	}

	return &nat64pb.UpdateConfigResponse{}, nil
}

func decodeNAT64Prefix(prefix *commonpb.IPv6Prefix) ([]byte, error) {
	network, err := prefix.ToPrefix()
	if err != nil {
		return nil, fmt.Errorf("invalid prefix: %w", err)
	}
	if network.Bits() != nat64PrefixBits {
		return nil, fmt.Errorf(
			"prefix must be a /%d, got /%d",
			nat64PrefixBits,
			network.Bits(),
		)
	}

	address := network.Addr().As16()
	return slices.Clone(address[:nat64PrefixBytes]), nil
}

func encodeNAT64Prefix(prefix []byte) (*commonpb.IPv6Prefix, error) {
	if len(prefix) != nat64PrefixBytes {
		return nil, fmt.Errorf(
			"invalid stored prefix length: got %d, want %d",
			len(prefix),
			nat64PrefixBytes,
		)
	}

	var address [16]byte
	copy(address[:], prefix)
	return &commonpb.IPv6Prefix{
		Addr:      commonpb.NewIPv6Address(address),
		PrefixLen: nat64PrefixBits,
	}, nil
}

// decodeConfig converts a whole wire config, starting from the defaults a
// new config has.
//
// A prefix or an IPv4 address listed twice is rejected: a whole config
// states each once, and mappings refer to prefixes by their position.
func decodeConfig(wireConfig *nat64pb.Config) (*NAT64Config, error) {
	config := defaultNAT64Config()
	if wireMTU := wireConfig.GetMtu(); wireMTU != nil {
		config.MTU = MTUConfig{
			IPv4MTU: wireMTU.GetIpv4Mtu(),
			IPv6MTU: wireMTU.GetIpv6Mtu(),
		}
	}
	config.DropUnknownPrefix = wireConfig.GetDropUnknownPrefix()
	config.DropUnknownMapping = wireConfig.GetDropUnknownMapping()

	for idx, wirePrefix := range wireConfig.GetPrefixes() {
		prefix, err := decodeNAT64Prefix(wirePrefix)
		if err != nil {
			return nil, fmt.Errorf("prefixes[%d]: %w", idx, err)
		}
		if slices.ContainsFunc(config.Prefixes, func(existing []byte) bool { return bytes.Equal(existing, prefix) }) {
			return nil, fmt.Errorf("prefixes[%d]: prefix is listed twice", idx)
		}
		config.Prefixes = append(config.Prefixes, prefix)
	}

	mapped := map[netip.Addr]struct{}{}
	for idx, wireMapping := range wireConfig.GetMappings() {
		mapping, err := decodeMapping(wireMapping.GetIpv4(), wireMapping.GetIpv6(), wireMapping.GetPrefixIndex())
		if err != nil {
			return nil, fmt.Errorf("mappings[%d]: %w", idx, err)
		}
		if err := checkPrefixIndex(&config, mapping); err != nil {
			return nil, fmt.Errorf("mappings[%d]: %w", idx, err)
		}
		if _, ok := mapped[mapping.IPv4]; ok {
			return nil, fmt.Errorf("mappings[%d]: ipv4 %s is listed twice", idx, mapping.IPv4)
		}
		mapped[mapping.IPv4] = struct{}{}
		config.Mappings = append(config.Mappings, mapping)
	}

	return &config, nil
}

// decodeMapping converts the addresses and the prefix index of a mapping,
// rejecting an IPv4-mapped IPv6 address.
func decodeMapping(ipv4 *commonpb.IPv4Address, ipv6 *commonpb.IPv6Address, prefixIndex uint32) (Mapping, error) {
	mapping := Mapping{
		IPv4:        ipv4.ToAddr(),
		IPv6:        ipv6.ToAddr(),
		PrefixIndex: prefixIndex,
	}
	if mapping.IPv6.Is4In6() {
		return Mapping{}, fmt.Errorf("ipv6 %q is not a pure IPv6 address", mapping.IPv6)
	}

	return mapping, nil
}

// DeleteConfig removes the named config when it is no longer referenced
// by any pipeline.
func (m *NAT64Service) DeleteConfig(ctx context.Context, req *nat64pb.DeleteConfigRequest) (*nat64pb.DeleteConfigResponse, error) {
	name := req.GetName()
	if err := m.core.Delete(name); err != nil {
		return nil, crud.DeleteError(name, err)
	}

	return &nat64pb.DeleteConfigResponse{}, nil
}

// checkPrefixIndex rejects a mapping whose prefix index names no prefix of
// the config.
func checkPrefixIndex(config *NAT64Config, mapping Mapping) error {
	if mapping.PrefixIndex >= uint32(len(config.Prefixes)) {
		return fmt.Errorf(
			"invalid prefix index: got %d, prefixes count %d",
			mapping.PrefixIndex,
			len(config.Prefixes),
		)
	}

	return nil
}
