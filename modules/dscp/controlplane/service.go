package dscp

import (
	"cmp"
	"context"
	"net/netip"
	"slices"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/modules/dscp/controlplane/dscppb/v1"
)

// ModuleHandle is a handle to a module configuration.
type ModuleHandle interface {
	Free()
}

// Backend abstracts shared memory operations.
type Backend interface {
	// UpdateModule creates a module config, applies mutations, and publishes it
	// to the dataplane.
	UpdateModule(name string, prefixes []netip.Prefix, flag uint8, mark uint8) (ModuleHandle, error)
}

type DscpService struct {
	dscppb.UnimplementedDscpServiceServer

	mu      sync.RWMutex
	backend Backend
	configs map[string]*config
}

type config struct {
	Prefixes []netip.Prefix
	Config   dscpConfig
	Module   ModuleHandle
}

// Free releases the module handle held by the config.
//
// It is safe to call even when no handle is held.
func (m *config) Free() {
	if m.Module != nil {
		m.Module.Free()
	}
}

func (m *config) Clone() *config {
	return &config{
		Prefixes: slices.Clone(m.Prefixes),
		Config:   m.Config,
		Module:   m.Module,
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

	prefixes, err := commonpb.NetworksFromPrefixes(config.Prefixes)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to convert prefixes: %v", err)
	}

	response.Config = &dscppb.Config{
		Prefixes: prefixes,
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
	toAdd, err := commonpb.PrefixesFromNetworks(request.GetPrefixes())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to convert prefixes: %v", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	cfg := &config{}
	if currConfig, ok := m.configs[name]; ok {
		cfg = currConfig.Clone()
	}

	cfg.Prefixes = slices.Compact(
		slices.SortedFunc(
			slices.Values(slices.Concat(cfg.Prefixes, toAdd)),
			comparePrefixes,
		),
	)

	if err := m.updateModuleConfig(name, cfg); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update module config %q: %v", name, err)
	}

	return &dscppb.AddPrefixesResponse{}, nil
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
	toRemove, err := commonpb.PrefixesFromNetworks(request.GetPrefixes())
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

	cfg.Prefixes = slices.DeleteFunc(
		cfg.Prefixes,
		func(prefix netip.Prefix) bool {
			return slices.Contains(toRemove, prefix)
		},
	)

	if err := m.updateModuleConfig(name, cfg); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update module config %q: %v", name, err)
	}

	return &dscppb.RemovePrefixesResponse{}, nil
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

func (m *DscpService) updateModuleConfig(name string, cfg *config) error {
	module, err := m.backend.UpdateModule(
		name,
		cfg.Prefixes,
		cfg.Config.Flag,
		cfg.Config.Mark,
	)
	if err != nil {
		return err
	}

	cfg.Free()

	m.configs[name] = &config{
		Prefixes: cfg.Prefixes,
		Config:   cfg.Config,
		Module:   module,
	}

	return nil
}
