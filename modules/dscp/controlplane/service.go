package dscp

import (
	"cmp"
	"context"
	"errors"
	"net/netip"
	"slices"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/dscp/controlplane/dscppb/v1"
)

// ModuleHandle is a handle to a module configuration.
type ModuleHandle interface {
	Free() error
}

// Backend abstracts shared memory operations.
type Backend interface {
	// UpdateModule creates a module config, applies mutations, and publishes it
	// to the dataplane.
	UpdateModule(name string, prefixes []netip.Prefix, flag uint8, mark uint8) (ModuleHandle, error)
	// DeleteModule removes a module config.
	DeleteModule(name string) error
}

type DscpService struct {
	dscppb.UnimplementedDscpServiceServer

	mu sync.RWMutex

	// deferred holds superseded module handles whose free was refused
	// because a live configuration generation still referenced them.
	// This service is their owner: it retries them on its next update,
	// through ReclaimDeferred, and nothing else remembers them.
	deferred []ModuleHandle
	backend  Backend
	configs  map[string]*config
}

type config struct {
	// Prefixes4 is the sorted IPv4 prefix set.
	Prefixes4 []netip.Prefix
	// Prefixes6 is the sorted IPv6 prefix set.
	Prefixes6 []netip.Prefix
	// Config is the DSCP marking configuration.
	Config dscpConfig
	// Module is the shared-memory handle of the published config.
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

func (m *config) Clone() *config {
	return &config{
		Prefixes4: slices.Clone(m.Prefixes4),
		Prefixes6: slices.Clone(m.Prefixes6),
		Config:    m.Config,
		Module:    m.Module,
	}
}

type dscpConfig struct {
	Flag uint8
	Mark uint8
}

func NewDscpService(backend Backend) *DscpService {
	return &DscpService{
		backend: backend,
		configs: map[string]*config{},
	}
}

func (m *DscpService) ListConfigs(
	ctx context.Context,
	request *dscppb.ListConfigsRequest,
) (*dscppb.ListConfigsResponse, error) {
	response := &dscppb.ListConfigsResponse{
		Configs: make([]string, 0),
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	for name := range m.configs {
		response.Configs = append(response.Configs, name)
	}

	return response, nil
}

func (m *DscpService) ShowConfig(
	ctx context.Context,
	request *dscppb.ShowConfigRequest,
) (*dscppb.ShowConfigResponse, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}

	name := request.GetName()
	response := &dscppb.ShowConfigResponse{}

	m.mu.RLock()
	defer m.mu.RUnlock()

	config, ok := m.configs[name]
	if !ok {
		return nil, status.Error(codes.NotFound, "config not found")
	}

	prefixes4, err := commonpb.NewIPv4PrefixesFromPrefixes(config.Prefixes4)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to convert prefixes: %v", err)
	}
	prefixes6, err := commonpb.NewIPv6PrefixesFromPrefixes(config.Prefixes6)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to convert prefixes: %v", err)
	}

	response.Config = &dscppb.Config{
		Prefixes4: prefixes4,
		Prefixes6: prefixes6,
		DscpConfig: &dscppb.DscpConfig{
			Flag: uint32(config.Config.Flag),
			Mark: uint32(config.Config.Mark),
		},
	}

	return response, nil
}

func (m *DscpService) AddPrefixes(
	ctx context.Context,
	request *dscppb.AddPrefixesRequest,
) (*dscppb.AddPrefixesResponse, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}

	name := request.GetName()
	toAdd4, err := commonpb.PrefixesFromNetworks(request.GetPrefixes4())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to convert prefixes: %v", err)
	}
	toAdd6, err := commonpb.PrefixesFromNetworks(request.GetPrefixes6())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to convert prefixes: %v", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	cfg := &config{}
	if currConfig, ok := m.configs[name]; ok {
		cfg = currConfig.Clone()
	}

	cfg.Prefixes4 = mergePrefixes(cfg.Prefixes4, toAdd4)
	cfg.Prefixes6 = mergePrefixes(cfg.Prefixes6, toAdd6)

	if err := m.updateModuleConfig(name, cfg); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update module config %q: %v", name, err)
	}

	return &dscppb.AddPrefixesResponse{}, nil
}

// mergePrefixes folds new prefixes into an existing sorted set, keeping it
// sorted and duplicate-free.
func mergePrefixes(existing, toAdd []netip.Prefix) []netip.Prefix {
	return slices.Compact(
		slices.SortedFunc(
			slices.Values(slices.Concat(existing, toAdd)),
			comparePrefixes,
		),
	)
}

func comparePrefixes(first, second netip.Prefix) int {
	return cmp.Or(
		first.Addr().Compare(second.Addr()),
		cmp.Compare(first.Bits(), second.Bits()),
	)
}

func (m *DscpService) RemovePrefixes(
	ctx context.Context,
	request *dscppb.RemovePrefixesRequest,
) (*dscppb.RemovePrefixesResponse, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}

	name := request.GetName()
	toRemove4, err := commonpb.PrefixesFromNetworks(request.GetPrefixes4())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to convert prefixes: %v", err)
	}
	toRemove6, err := commonpb.PrefixesFromNetworks(request.GetPrefixes6())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to convert prefixes: %v", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Create a new config to-be-updated either from scratch or from the
	// current config.
	cfg := &config{}
	if currConfig, ok := m.configs[name]; ok {
		cfg = currConfig.Clone()
	}

	cfg.Prefixes4 = removePrefixes(cfg.Prefixes4, toRemove4)
	cfg.Prefixes6 = removePrefixes(cfg.Prefixes6, toRemove6)

	if err := m.updateModuleConfig(name, cfg); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update module config %q: %v", name, err)
	}

	return &dscppb.RemovePrefixesResponse{}, nil
}

// removePrefixes drops every listed prefix from an existing set.
func removePrefixes(existing, toRemove []netip.Prefix) []netip.Prefix {
	return slices.DeleteFunc(
		existing,
		func(prefix netip.Prefix) bool {
			return slices.Contains(toRemove, prefix)
		},
	)
}

func (m *DscpService) SetDscpMarking(
	ctx context.Context,
	request *dscppb.SetDscpMarkingRequest,
) (*dscppb.SetDscpMarkingResponse, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}

	name := request.GetName()
	flag := uint8(request.GetDscpConfig().GetFlag())
	mark := uint8(request.GetDscpConfig().GetMark())

	m.mu.Lock()
	defer m.mu.Unlock()

	cfg := &config{}
	if currConfig, ok := m.configs[name]; ok {
		cfg = currConfig.Clone()
	}
	cfg.Config = dscpConfig{
		Flag: flag,
		Mark: mark,
	}

	if err := m.updateModuleConfig(name, cfg); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update module config %q: %v", name, err)
	}

	return &dscppb.SetDscpMarkingResponse{}, nil
}

// DeleteConfig removes the named config if it is not referenced by any
// pipeline.
func (m *DscpService) DeleteConfig(
	ctx context.Context,
	request *dscppb.DeleteConfigRequest,
) (*dscppb.DeleteConfigResponse, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}

	name := request.GetName()

	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.configs[name]
	if !ok {
		return nil, status.Error(codes.NotFound, "config not found")
	}

	if err := m.backend.DeleteModule(name); err != nil {
		return nil, status.Errorf(
			codes.Internal,
			"failed to delete module config %q: %v", name, err,
		)
	}

	// The delete retired the generation holding the published module.
	// Retry the deferred ones, then retire this one.
	m.reclaimDeferred()
	m.parkOrFree(entry.Module)

	delete(m.configs, name)

	return &dscppb.DeleteConfigResponse{}, nil
}

func (m *DscpService) updateModuleConfig(name string, cfg *config) error {
	module, err := m.backend.UpdateModule(
		name,
		slices.Concat(cfg.Prefixes4, cfg.Prefixes6),
		cfg.Config.Flag,
		cfg.Config.Mark,
	)
	if err != nil {
		return err
	}

	m.reclaimDeferred()
	m.parkOrFree(cfg.Module)

	m.configs[name] = &config{
		Prefixes4: cfg.Prefixes4,
		Prefixes6: cfg.Prefixes6,
		Config:    cfg.Config,
		Module:    module,
	}

	return nil
}

// parkOrFree frees the handle when it is dangling and parks it for
// retry when a live generation still references it. The caller must
// hold m.mu.
func (m *DscpService) parkOrFree(handle ModuleHandle) {
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
func (m *DscpService) ReclaimDeferred() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reclaimDeferred()
}

// reclaimDeferred is ReclaimDeferred without the lock. The caller must
// hold m.mu.
func (m *DscpService) reclaimDeferred() {
	kept := m.deferred[:0]
	for _, handle := range m.deferred {
		if err := handle.Free(); errors.Is(err, ffi.ErrStillReferenced) {
			kept = append(kept, handle)
		}
	}
	clear(m.deferred[len(kept):])
	m.deferred = kept
}
