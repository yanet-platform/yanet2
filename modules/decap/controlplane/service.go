package decap

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"sync"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/go/xnetip"
	"github.com/yanet-platform/yanet2/modules/decap/controlplane/decappb"
)

var (
	errConfigNameRequired = status.Error(codes.InvalidArgument, "config name is required")
)

// ModuleHandle is a handle to a module configuration.
type ModuleHandle interface {
	Free()
}

// Backend abstracts shared memory operations.
type Backend interface {
	// UpdateModule creates a module config, adds prefixes, and publishes
	// it to the dataplane.
	UpdateModule(name string, prefixes []netip.Prefix) (ModuleHandle, error)
}

type config struct {
	Prefixes []netip.Prefix
	Module   ModuleHandle
}

func (m *config) Clone() *config {
	return &config{
		Prefixes: slices.Clone(m.Prefixes),
		Module:   m.Module,
	}
}

type DecapService struct {
	decappb.UnimplementedDecapServiceServer

	mu      sync.RWMutex
	backend Backend
	configs map[string]*config
	log     *zap.SugaredLogger
}

func NewDecapService(backend Backend, log *zap.SugaredLogger) *DecapService {
	return &DecapService{
		backend: backend,
		configs: map[string]*config{},
		log:     log,
	}
}

func (m *DecapService) ListConfigs(
	ctx context.Context,
	request *decappb.ListConfigsRequest,
) (*decappb.ListConfigsResponse, error) {
	response := &decappb.ListConfigsResponse{
		Configs: make([]string, 0),
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	for name := range m.configs {
		response.Configs = append(response.Configs, name)
	}

	return response, nil
}

func (m *DecapService) ShowConfig(
	ctx context.Context,
	request *decappb.ShowConfigRequest,
) (*decappb.ShowConfigResponse, error) {
	name := request.GetName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.configs[name]
	if !ok {
		return nil, status.Error(codes.NotFound, "no config found")
	}

	response := &decappb.ShowConfigResponse{
		Prefixes: make([]string, 0, len(entry.Prefixes)),
	}

	for _, p := range entry.Prefixes {
		response.Prefixes = append(response.Prefixes, p.String())
	}

	return response, nil
}

func (m *DecapService) AddPrefixes(
	ctx context.Context,
	request *decappb.AddPrefixesRequest,
) (*decappb.AddPrefixesResponse, error) {
	name := request.GetName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	toAdd := make([]netip.Prefix, 0, len(request.GetPrefixes()))
	for _, p := range request.GetPrefixes() {
		prefix, err := netip.ParsePrefix(p)
		if err != nil {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"failed to parse prefix %q: %v", p, err,
			)
		}

		toAdd = append(toAdd, prefix.Masked())
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Create a new config to-be-updated either from scratch or from the
	// current config.
	cfg := &config{}
	if currConfig, ok := m.configs[name]; ok {
		cfg = currConfig.Clone()
	}

	cfg.Prefixes = slices.Compact(
		slices.SortedFunc(
			slices.Values(slices.Concat(cfg.Prefixes, toAdd)),
			xnetip.PrefixCompare,
		),
	)

	if err := m.updateConfig(name, cfg); err != nil {
		return nil, status.Errorf(
			codes.Internal,
			"failed to update module config %q: %v", name, err,
		)
	}

	return &decappb.AddPrefixesResponse{}, nil
}

func (m *DecapService) RemovePrefixes(
	ctx context.Context,
	request *decappb.RemovePrefixesRequest,
) (*decappb.RemovePrefixesResponse, error) {
	name := request.GetName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	toRemove := make([]netip.Prefix, 0, len(request.GetPrefixes()))
	for _, p := range request.GetPrefixes() {
		prefix, err := netip.ParsePrefix(p)
		if err != nil {
			return nil, err
		}
		toRemove = append(toRemove, prefix.Masked())
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

	// Note that we don't remove the FFI config in case of empty prefixes left.

	if err := m.updateConfig(name, cfg); err != nil {
		return nil, status.Errorf(
			codes.Internal,
			"failed to update module config %q: %v", name, err,
		)
	}

	return &decappb.RemovePrefixesResponse{}, nil
}

func (m *DecapService) updateConfig(name string, cfg *config) error {
	m.log.Debugw("updating config",
		zap.String("config", name),
	)

	mod, err := m.backend.UpdateModule(name, cfg.Prefixes)
	if err != nil {
		return fmt.Errorf("failed to update module config %q: %w", name, err)
	}

	if cfg.Module != nil {
		cfg.Module.Free()
	}

	// Atomic semantics: either we update the config or we don't in case of
	// errors.
	m.configs[name] = &config{
		Prefixes: cfg.Prefixes,
		Module:   mod,
	}

	return nil
}
