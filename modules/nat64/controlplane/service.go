package nat64

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/netip"
	"slices"
	"sync"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
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

	mu sync.Mutex

	// deferred holds superseded module handles whose free was refused
	// because a live configuration generation still referenced them.
	// This service is their owner: it retries them on its next update,
	// through ReclaimDeferred, and nothing else remembers them.
	deferred []ModuleHandle
	backend  Backend
	configs  map[string]config
	log      *zap.Logger
}

type config struct {
	Config NAT64Config
	Module ModuleHandle
}

// Free releases the module handle held by the config.
//
// It is safe to call even when no handle is held.
func (m *config) Free() error {
	if m.Module == nil {
		return nil
	}
	return m.Module.Free()
}

func (m config) Clone() config {
	return config{
		Config: m.Config.Clone(),
		Module: m.Module,
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

	cfg := inst.Config
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
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	inst := m.instanceFor(name).Clone()
	inst.Config.Prefixes = append(inst.Config.Prefixes, prefix)

	if err := m.updateModuleConfig(name, inst); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update module config: %v", err)
	}

	return &nat64pb.AddPrefixResponse{}, nil
}

func (m *NAT64Service) RemovePrefix(ctx context.Context, req *nat64pb.RemovePrefixRequest) (*nat64pb.RemovePrefixResponse, error) {
	prefix, err := decodeNAT64Prefix(req.GetPrefix())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
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
	for idx, storedPrefix := range next.Config.Prefixes {
		if bytes.Equal(storedPrefix, prefix) {
			removeIdx = idx
			break
		}
	}
	if removeIdx == -1 {
		return &nat64pb.RemovePrefixResponse{}, nil
	}

	next.Config.Prefixes = slices.Delete(next.Config.Prefixes, removeIdx, removeIdx+1)
	next.Config.Mappings = adjustMappingsAfterPrefixRemove(next.Config.Mappings, uint32(removeIdx))

	if err := m.updateModuleConfig(name, next); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update module config: %v", err)
	}

	return &nat64pb.RemovePrefixResponse{}, nil
}

func (m *NAT64Service) AddMapping(ctx context.Context, req *nat64pb.AddMappingRequest) (*nat64pb.AddMappingResponse, error) {
	if req.GetIpv4() == nil {
		return nil, status.Error(codes.InvalidArgument, "ipv4 address is required")
	}
	if req.GetIpv6() == nil {
		return nil, status.Error(codes.InvalidArgument, "ipv6 address is required")
	}
	ipv4 := req.GetIpv4().ToAddr()
	ipv6 := req.GetIpv6().ToAddr()
	if ipv6.Is4In6() {
		return nil, status.Errorf(codes.InvalidArgument, "ipv6 %q is not a pure IPv6 address", ipv6)
	}

	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	inst := m.instanceFor(name).Clone()
	if req.PrefixIndex >= uint32(len(inst.Config.Prefixes)) {
		return nil, status.Errorf(
			codes.InvalidArgument,
			"invalid prefix index: got %d, prefixes count %d",
			req.PrefixIndex,
			len(inst.Config.Prefixes),
		)
	}
	inst.Config.Mappings = append(inst.Config.Mappings, Mapping{
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
	if req.GetIpv4() == nil {
		return nil, status.Error(codes.InvalidArgument, "ipv4 address is required")
	}
	ipv4 := req.GetIpv4().ToAddr()

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

	next.Config.Mappings = slices.DeleteFunc(next.Config.Mappings, func(mapping Mapping) bool {
		return mapping.IPv4 == ipv4
	})
	if len(next.Config.Mappings) == len(inst.Config.Mappings) {
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
	inst.Config.MTU = MTUConfig{
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
	inst.Config.DropUnknownPrefix = req.DropUnknownPrefix
	inst.Config.DropUnknownMapping = req.DropUnknownMapping

	if err := m.updateModuleConfig(name, inst); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update module config: %v", err)
	}

	return &nat64pb.SetDropUnknownResponse{}, nil
}

func decodeNAT64Prefix(prefix *commonpb.IPv6Prefix) ([]byte, error) {
	if prefix == nil {
		return nil, fmt.Errorf("prefix is required")
	}

	network, err := prefix.ToPrefix()
	if err != nil {
		return nil, fmt.Errorf("invalid prefix: %w", err)
	}
	if network.Bits() != nat64PrefixBits {
		return nil, fmt.Errorf(
			"invalid prefix length: got %d, want %d",
			network.Bits(),
			nat64PrefixBits,
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

func (m *NAT64Service) instanceFor(name string) config {
	inst, ok := m.configs[name]
	if !ok {
		return config{
			Config: defaultNAT64Config(),
		}
	}
	return inst
}

func (m *NAT64Service) updateModuleConfig(name string, inst config) error {
	module, err := m.backend.UpdateModule(name, &inst.Config)
	if err != nil {
		return err
	}

	m.reclaimDeferred()
	m.parkOrFree(inst.Module)
	inst.Module = module
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

// parkOrFree frees the handle when it is dangling and parks it for
// retry when a live generation still references it. The caller must
// hold m.mu.
func (m *NAT64Service) parkOrFree(handle ModuleHandle) {
	if handle == nil {
		return
	}
	if err := handle.Free(); errors.Is(err, ffi.ErrStillReferenced) {
		m.deferred = append(m.deferred, handle)
	}
}

// ReclaimDeferred retries every deferred handle, dropping the ones whose
// generations have drained and keeping the rest deferred. It is the
// reclamation handler for this module's superseded configs; the service
// itself runs it after each successful publish, and anything else may
// call it at any time.
func (m *NAT64Service) ReclaimDeferred() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reclaimDeferred()
}

// reclaimDeferred is ReclaimDeferred without the lock. The caller must
// hold m.mu.
func (m *NAT64Service) reclaimDeferred() {
	kept := m.deferred[:0]
	for _, handle := range m.deferred {
		if err := handle.Free(); errors.Is(err, ffi.ErrStillReferenced) {
			kept = append(kept, handle)
		}
	}
	clear(m.deferred[len(kept):])
	m.deferred = kept
}
