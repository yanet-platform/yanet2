package nat64

import (
	"bytes"
	"context"
	"errors"
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

// errUnchanged is reported by a mutation callback that found nothing to
// publish, so the current config stays as it is.
var errUnchanged = errors.New("config unchanged")

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

func (m *NAT64Service) AddPrefix(ctx context.Context, req *nat64pb.AddPrefixRequest) (*nat64pb.AddPrefixResponse, error) {
	prefix, err := decodeNAT64Prefix(req.GetPrefix())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	name := req.GetName()
	err = m.core.Mutate(name, func(current *NAT64Config, ok bool) (*NAT64Config, error) {
		next := nextConfig(current, ok)
		if slices.ContainsFunc(next.Prefixes, func(existing []byte) bool { return bytes.Equal(existing, prefix) }) {
			return nil, errUnchanged
		}
		next.Prefixes = append(next.Prefixes, prefix)

		return next, nil
	})
	if err != nil && !errors.Is(err, errUnchanged) {
		return nil, crud.MutationError(err)
	}

	return &nat64pb.AddPrefixResponse{}, nil
}

func (m *NAT64Service) RemovePrefix(ctx context.Context, req *nat64pb.RemovePrefixRequest) (*nat64pb.RemovePrefixResponse, error) {
	prefix, err := decodeNAT64Prefix(req.GetPrefix())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	name := req.GetName()
	err = m.core.Mutate(name, func(current *NAT64Config, ok bool) (*NAT64Config, error) {
		if !ok {
			return nil, status.Errorf(codes.NotFound, "config %q not found", name)
		}
		next := nextConfig(current, ok)

		removeIdx := -1
		for idx, storedPrefix := range next.Prefixes {
			if bytes.Equal(storedPrefix, prefix) {
				removeIdx = idx
				break
			}
		}
		if removeIdx == -1 {
			return nil, status.Errorf(codes.NotFound, "prefix not found in config %q", name)
		}

		next.Prefixes = slices.Delete(next.Prefixes, removeIdx, removeIdx+1)
		next.Mappings = adjustMappingsAfterPrefixRemove(next.Mappings, uint32(removeIdx))

		return next, nil
	})
	if err != nil {
		return nil, crud.MutationError(err)
	}

	return &nat64pb.RemovePrefixResponse{}, nil
}

func (m *NAT64Service) AddMapping(ctx context.Context, req *nat64pb.AddMappingRequest) (*nat64pb.AddMappingResponse, error) {
	ipv4 := req.GetIpv4().ToAddr()
	ipv6 := req.GetIpv6().ToAddr()
	if ipv6.Is4In6() {
		return nil, status.Errorf(codes.InvalidArgument, "ipv6 %q is not a pure IPv6 address", ipv6)
	}

	name := req.GetName()
	err := m.core.Mutate(name, func(current *NAT64Config, ok bool) (*NAT64Config, error) {
		next := nextConfig(current, ok)
		if req.PrefixIndex >= uint32(len(next.Prefixes)) {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"invalid prefix index: got %d, prefixes count %d",
				req.PrefixIndex,
				len(next.Prefixes),
			)
		}
		next.Mappings = slices.DeleteFunc(next.Mappings, func(existing Mapping) bool { return existing.IPv4 == ipv4 })
		next.Mappings = append(next.Mappings, Mapping{
			IPv4:        ipv4,
			IPv6:        ipv6,
			PrefixIndex: req.PrefixIndex,
		})

		return next, nil
	})
	if err != nil {
		return nil, crud.MutationError(err)
	}

	return &nat64pb.AddMappingResponse{}, nil
}

func (m *NAT64Service) RemoveMapping(ctx context.Context, req *nat64pb.RemoveMappingRequest) (*nat64pb.RemoveMappingResponse, error) {
	ipv4 := req.GetIpv4().ToAddr()

	name := req.GetName()
	err := m.core.Mutate(name, func(current *NAT64Config, ok bool) (*NAT64Config, error) {
		if !ok {
			return nil, status.Errorf(codes.NotFound, "config %q not found", name)
		}
		next := nextConfig(current, ok)

		next.Mappings = slices.DeleteFunc(next.Mappings, func(mapping Mapping) bool {
			return mapping.IPv4 == ipv4
		})
		if len(next.Mappings) == len(current.Mappings) {
			return nil, status.Errorf(codes.NotFound, "mapping for %s not found in config %q", ipv4, name)
		}

		return next, nil
	})
	if err != nil {
		return nil, crud.MutationError(err)
	}

	return &nat64pb.RemoveMappingResponse{}, nil
}

func (m *NAT64Service) SetMTU(ctx context.Context, req *nat64pb.SetMTURequest) (*nat64pb.SetMTUResponse, error) {
	name := req.GetName()
	err := m.core.Mutate(name, func(current *NAT64Config, ok bool) (*NAT64Config, error) {
		next := nextConfig(current, ok)
		next.MTU = MTUConfig{
			IPv4MTU: req.Mtu.Ipv4Mtu,
			IPv6MTU: req.Mtu.Ipv6Mtu,
		}

		return next, nil
	})
	if err != nil {
		return nil, crud.MutationError(err)
	}

	return &nat64pb.SetMTUResponse{}, nil
}

func (m *NAT64Service) SetDropUnknown(ctx context.Context, req *nat64pb.SetDropUnknownRequest) (*nat64pb.SetDropUnknownResponse, error) {
	name := req.GetName()
	err := m.core.Mutate(name, func(current *NAT64Config, ok bool) (*NAT64Config, error) {
		next := nextConfig(current, ok)
		next.DropUnknownPrefix = req.DropUnknownPrefix
		next.DropUnknownMapping = req.DropUnknownMapping

		return next, nil
	})
	if err != nil {
		return nil, crud.MutationError(err)
	}

	return &nat64pb.SetDropUnknownResponse{}, nil
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

// DeleteConfig removes the named config when it is no longer referenced
// by any pipeline.
func (m *NAT64Service) DeleteConfig(ctx context.Context, req *nat64pb.DeleteConfigRequest) (*nat64pb.DeleteConfigResponse, error) {
	name := req.GetName()
	if err := m.core.Delete(name); err != nil {
		return nil, crud.DeleteError(name, err)
	}

	return &nat64pb.DeleteConfigResponse{}, nil
}

// nextConfig returns the config a mutation starts from: a deep copy of the
// current one, or the defaults when the name is new.
func nextConfig(current *NAT64Config, ok bool) *NAT64Config {
	if !ok {
		defaults := defaultNAT64Config()
		return &defaults
	}
	next := current.Clone()
	return &next
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
