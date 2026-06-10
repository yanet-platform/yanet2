package decap

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/go/xnetip"
	"github.com/yanet-platform/yanet2/modules/decap/controlplane/decappb/v1"
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

// DecapService implements the DecapService gRPC server.
type DecapService struct {
	decappb.UnimplementedDecapServiceServer

	mu      sync.Mutex
	backend Backend
	configs map[string]*config
}

// NewDecapService constructs a DecapService backed by the given Backend.
func NewDecapService(backend Backend) *DecapService {
	return &DecapService{
		backend: backend,
		configs: map[string]*config{},
	}
}

// ListConfigs returns all known config names across all dataplane instances.
func (m *DecapService) ListConfigs(
	ctx context.Context,
	req *decappb.ListConfigsRequest,
) (*decappb.ListConfigsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	names := make([]string, 0, len(m.configs))
	for name := range m.configs {
		names = append(names, name)
	}

	return &decappb.ListConfigsResponse{Configs: names}, nil
}

// ShowConfig returns the current prefix set for the named config.
func (m *DecapService) ShowConfig(
	ctx context.Context,
	req *decappb.ShowConfigRequest,
) (*decappb.ShowConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.configs[name]
	if !ok {
		return nil, status.Error(codes.NotFound, "no config found")
	}

	prefixes := make([]string, 0, len(entry.Prefixes))
	for _, p := range entry.Prefixes {
		prefixes = append(prefixes, p.String())
	}

	return &decappb.ShowConfigResponse{Prefixes: prefixes}, nil
}

// UpdateConfig atomically replaces the whole prefix set of the named config.
func (m *DecapService) UpdateConfig(
	ctx context.Context,
	req *decappb.UpdateConfigRequest,
) (*decappb.UpdateConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	prefixes := make([]netip.Prefix, 0, len(req.GetPrefixes()))
	for _, p := range req.GetPrefixes() {
		prefix, err := netip.ParsePrefix(p)
		if err != nil {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"failed to parse prefix %q: %v", p, err,
			)
		}
		prefixes = append(prefixes, prefix.Masked())
	}

	prefixes = slices.Compact(
		slices.SortedFunc(
			slices.Values(prefixes),
			xnetip.PrefixCompare,
		),
	)

	m.mu.Lock()
	defer m.mu.Unlock()

	cfg := &config{Prefixes: prefixes}
	if err := m.updateConfig(name, cfg); err != nil {
		return nil, status.Errorf(
			codes.Internal,
			"failed to update module config %q: %v", name, err,
		)
	}

	return &decappb.UpdateConfigResponse{}, nil
}

// updateConfig calls the backend to publish cfg and, on success, frees the old
// module handle and stores the new config. The caller must hold m.mu.
func (m *DecapService) updateConfig(name string, cfg *config) error {
	mod, err := m.backend.UpdateModule(name, cfg.Prefixes)
	if err != nil {
		return fmt.Errorf("failed to update module config %q: %w", name, err)
	}

	if old, ok := m.configs[name]; ok && old.Module != nil {
		old.Module.Free()
	}

	m.configs[name] = &config{
		Prefixes: cfg.Prefixes,
		Module:   mod,
	}

	return nil
}
