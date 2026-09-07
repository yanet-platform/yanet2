package nat64

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

// configEntry is the per-name lock anchor of a nat64 config.
//
// Entries are append-only: deleting a config clears its published slot
// instead of removing the entry, keeping the entry as the lock anchor.
// Acquiring a name's lock is a two-step operation — fetch the entry, then
// lock it — and removing entries would let two goroutines serialize the
// same name on two different entry objects during exactly that window.
type configEntry struct {
	// updateMu serializes mutations of this name for the entry's whole
	// life, across the whole operation including the backend publish.
	updateMu sync.Mutex
	// published is the config currently active for this name, or nil
	// when the name is absent. It is written only while holding both the
	// entry's update lock and the service write lock; an update-lock
	// holder may read it without the service lock.
	published *config
}

func (m *configEntry) LockUpdate() {
	m.updateMu.Lock()
}

func (m *configEntry) UnlockUpdate() {
	m.updateMu.Unlock()
}

func (m *configEntry) Published() *config {
	return m.published
}

func (m *configEntry) Publish(config *config) {
	m.published = config
}

// NAT64Service implements the NAT64 gRPC service
type NAT64Service struct {
	nat64pb.UnimplementedNAT64ServiceServer

	// mu guards configs and deferred. Critical sections are short map
	// and slice work only; the backend publish runs under the target
	// entry's update lock, outside any service-lock section, so a
	// stalled publish never blocks a read.
	mu sync.RWMutex
	// reclaimMu serializes handle reclamation — draining the deferred
	// list and parking a superseded handle — across its free attempts,
	// so a handle refused by a draining generation is retried before the
	// drain that could miss it completes. It is never taken by a read
	// path, and a handle's destruction under it may block on the shared
	// C-side configuration lock, which only stalls other reclamations.
	reclaimMu sync.Mutex

	// deferred holds superseded module handles whose free was refused
	// because a live configuration generation still referenced them.
	// This service is their owner: it retries them on its next update,
	// through ReclaimDeferred, and nothing else remembers them.
	deferred []ModuleHandle
	backend  Backend
	// configs maps a name to its append-only entry. See configEntry.
	configs map[string]*configEntry
	log     *zap.Logger
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
		configs: map[string]*configEntry{},
	}
}

func (m *NAT64Service) ListConfigs(ctx context.Context, req *nat64pb.ListConfigsRequest) (*nat64pb.ListConfigsResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.configs))
	for name, entry := range m.configs {
		if entry.Published() == nil {
			continue
		}
		names = append(names, name)
	}

	return &nat64pb.ListConfigsResponse{
		Configs: slices.Sorted(slices.Values(names)),
	}, nil
}

func (m *NAT64Service) ShowConfig(ctx context.Context, req *nat64pb.ShowConfigRequest) (*nat64pb.ShowConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	response := &nat64pb.ShowConfigResponse{}

	m.mu.RLock()
	entry, ok := m.configs[name]
	if !ok || entry.Published() == nil {
		m.mu.RUnlock()
		return nil, status.Error(codes.NotFound, "config not found")
	}
	cfg := entry.Published().Config
	m.mu.RUnlock()

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

	err = m.withEntry(name, func(entry *configEntry) error {
		inst := instanceFor(entry).Clone()
		if slices.ContainsFunc(inst.Config.Prefixes, func(existing []byte) bool { return bytes.Equal(existing, prefix) }) {
			return nil
		}
		inst.Config.Prefixes = append(inst.Config.Prefixes, prefix)

		return m.updateModuleConfig(name, inst, entry)
	})
	if err != nil {
		return nil, err
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

	// Rejecting an unknown name here keeps the locked path from interning
	// an entry for a name that never existed; the locked re-check below
	// stays authoritative for the name that waited on an in-flight update.
	if !m.hasEntry(name) {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	err = m.withEntry(name, func(entry *configEntry) error {
		if entry.Published() == nil {
			return status.Errorf(codes.NotFound, "config %q not found", name)
		}
		next := instanceFor(entry).Clone()

		removeIdx := -1
		for idx, storedPrefix := range next.Config.Prefixes {
			if bytes.Equal(storedPrefix, prefix) {
				removeIdx = idx
				break
			}
		}
		if removeIdx == -1 {
			return status.Errorf(codes.NotFound, "prefix not found in config %q", name)
		}

		next.Config.Prefixes = slices.Delete(next.Config.Prefixes, removeIdx, removeIdx+1)
		next.Config.Mappings = adjustMappingsAfterPrefixRemove(next.Config.Mappings, uint32(removeIdx))

		return m.updateModuleConfig(name, next, entry)
	})
	if err != nil {
		return nil, err
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

	err := m.withEntry(name, func(entry *configEntry) error {
		inst := instanceFor(entry).Clone()
		if req.PrefixIndex >= uint32(len(inst.Config.Prefixes)) {
			return status.Errorf(
				codes.InvalidArgument,
				"invalid prefix index: got %d, prefixes count %d",
				req.PrefixIndex,
				len(inst.Config.Prefixes),
			)
		}
		inst.Config.Mappings = slices.DeleteFunc(inst.Config.Mappings, func(existing Mapping) bool { return existing.IPv4 == ipv4 })
		inst.Config.Mappings = append(inst.Config.Mappings, Mapping{
			IPv4:        ipv4,
			IPv6:        ipv6,
			PrefixIndex: req.PrefixIndex,
		})

		return m.updateModuleConfig(name, inst, entry)
	})
	if err != nil {
		return nil, err
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

	// Rejecting an unknown name here keeps the locked path from interning
	// an entry for a name that never existed; the locked re-check below
	// stays authoritative for the name that waited on an in-flight update.
	if !m.hasEntry(name) {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	err := m.withEntry(name, func(entry *configEntry) error {
		if entry.Published() == nil {
			return status.Errorf(codes.NotFound, "config %q not found", name)
		}
		next := instanceFor(entry).Clone()

		next.Config.Mappings = slices.DeleteFunc(next.Config.Mappings, func(mapping Mapping) bool {
			return mapping.IPv4 == ipv4
		})
		if len(next.Config.Mappings) == len(entry.Published().Config.Mappings) {
			return status.Errorf(codes.NotFound, "mapping for %s not found in config %q", ipv4, name)
		}

		return m.updateModuleConfig(name, next, entry)
	})
	if err != nil {
		return nil, err
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

	err := m.withEntry(name, func(entry *configEntry) error {
		inst := instanceFor(entry).Clone()
		inst.Config.MTU = MTUConfig{
			IPv4MTU: req.Mtu.Ipv4Mtu,
			IPv6MTU: req.Mtu.Ipv6Mtu,
		}

		return m.updateModuleConfig(name, inst, entry)
	})
	if err != nil {
		return nil, err
	}

	return &nat64pb.SetMTUResponse{}, nil
}

func (m *NAT64Service) SetDropUnknown(ctx context.Context, req *nat64pb.SetDropUnknownRequest) (*nat64pb.SetDropUnknownResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	err := m.withEntry(name, func(entry *configEntry) error {
		inst := instanceFor(entry).Clone()
		inst.Config.DropUnknownPrefix = req.DropUnknownPrefix
		inst.Config.DropUnknownMapping = req.DropUnknownMapping

		return m.updateModuleConfig(name, inst, entry)
	})
	if err != nil {
		return nil, err
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
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	// Rejecting an unknown name here keeps the locked path from interning
	// an entry for a name that never existed; the locked re-check below
	// stays authoritative for the name that waited on an in-flight update.
	if !m.hasEntry(name) {
		return nil, status.Error(codes.NotFound, "config not found")
	}

	err := m.withEntry(name, func(entry *configEntry) error {
		oldConfig := entry.Published()
		if oldConfig == nil {
			return status.Error(codes.NotFound, "config not found")
		}

		if err := m.backend.DeleteModule(name); err != nil {
			return status.Errorf(codes.Internal, "failed to delete module config: %v", err)
		}

		m.setPublished(entry, nil)

		// The delete retired the generation holding the published
		// module; retry the deferred ones, then retire this one.
		m.ReclaimDeferred()
		m.parkOrFree(oldConfig.Module)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &nat64pb.DeleteConfigResponse{}, nil
}

// setPublished swaps the entry's published config under the service lock.
func (m *NAT64Service) setPublished(entry *configEntry, config *config) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry.Publish(config)
}

// entry fetches or creates the lock anchor of the named config. The caller
// must not hold the service lock.
func (m *NAT64Service) entry(name string) *configEntry {
	m.mu.Lock()
	defer m.mu.Unlock()

	if entry, ok := m.configs[name]; ok {
		return entry
	}
	entry := &configEntry{}
	m.configs[name] = entry
	return entry
}

// hasEntry reports whether the named config already has an entry, live or
// tombstoned. It is the read-only pre-check that keeps the deleting paths
// from interning an entry for a name that never existed.
func (m *NAT64Service) hasEntry(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, ok := m.configs[name]
	return ok
}

// withEntry fetches or creates the entry for the name, holds its update
// lock for the duration of the call, then returns the call's error. The
// backend publish runs here, under the entry lock only.
func (m *NAT64Service) withEntry(name string, fn func(*configEntry) error) error {
	entry := m.entry(name)
	entry.LockUpdate()
	defer entry.UnlockUpdate()

	return fn(entry)
}

// instanceFor returns the config an in-place mutation starts from: the
// entry's published one, or defaults for a name seen for the first time.
func instanceFor(entry *configEntry) config {
	if entry.Published() != nil {
		return *entry.Published()
	}
	return config{
		Config: defaultNAT64Config(),
	}
}

// updateModuleConfig calls the backend to publish the instance, retries
// this service's deferred handles (the publish retired the generations
// that were holding them), frees or defers the old module handle, and
// stores the new config. The caller must hold the entry's update lock.
func (m *NAT64Service) updateModuleConfig(name string, inst config, entry *configEntry) error {
	module, err := m.backend.UpdateModule(name, &inst.Config)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to update module config: %v", err)
	}

	oldConfig := entry.Published()

	inst.Module = module
	m.setPublished(entry, &inst)

	// The publish retired the generation holding the published module;
	// retry the deferred ones, then retire the displaced one.
	m.ReclaimDeferred()
	if oldConfig != nil {
		m.parkOrFree(oldConfig.Module)
	}

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
// retry when a live generation still references it. The whole cycle runs
// under the reclamation lock so it cannot interleave with a concurrent
// drain that would miss the survivor.
func (m *NAT64Service) parkOrFree(handle ModuleHandle) {
	if handle == nil {
		return
	}

	m.reclaimMu.Lock()
	defer m.reclaimMu.Unlock()

	if err := handle.Free(); errors.Is(err, ffi.ErrStillReferenced) {
		m.park(handle)
	}
}

// ReclaimDeferred retries every deferred handle, dropping the ones whose
// generations have drained and keeping the rest deferred. It is the
// reclamation handler for this module's superseded configs; the service
// itself runs it after each successful publish, and anything else may
// call it at any time.
//
// The frees run without the service lock: a handle's destruction takes
// the shared C-side configuration lock, which an in-flight publish of
// another name may hold, so freeing under the service lock would let
// that publish stall every read again. The reclamation lock still
// serializes the whole cycle against a concurrent park, so a survivor
// can never be missed by the drain that precedes its park.
func (m *NAT64Service) ReclaimDeferred() {
	m.reclaimMu.Lock()
	defer m.reclaimMu.Unlock()

	handles := m.drainDeferred()
	for _, handle := range handles {
		if err := handle.Free(); errors.Is(err, ffi.ErrStillReferenced) {
			m.park(handle)
		}
	}
}

func (m *NAT64Service) park(handle ModuleHandle) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.deferred = append(m.deferred, handle)
}

func (m *NAT64Service) drainDeferred() []ModuleHandle {
	m.mu.Lock()
	defer m.mu.Unlock()

	handles := m.deferred
	m.deferred = nil
	return handles
}
