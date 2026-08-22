package decap

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"sync"

	"github.com/yanet-platform/yanet2/controlplane/ffi"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/modules/decap/controlplane/decappb/v1"
)

var (
	errConfigNameRequired = status.Error(codes.InvalidArgument, "config name is required")
)

// ModuleHandle is a handle to a module configuration.
type ModuleHandle interface {
	Free() error
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

// Free releases the module handle held by the config.
//
// It is safe to call even when no handle is held. The result is the
// handle's: nil when destroyed, ffi.ErrStillReferenced when a live
// generation still references it and the caller must remember it.
func (m *config) Free() error {
	if m.Module == nil {
		return nil
	}
	return m.Module.Free()
}

// DecapService implements the DecapService gRPC server.
type DecapService struct {
	decappb.UnimplementedDecapServiceServer

	mu sync.Mutex
	// deferred holds superseded module handles whose free was refused
	// because a live configuration generation still referenced them.
	// This service is their owner: it retries them on its next update,
	// through ReclaimDeferred, and nothing else remembers them.
	deferred []ModuleHandle
	backend  Backend
	configs  map[string]*config
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

	prefixes, err := commonpb.NetworksFromPrefixes(entry.Prefixes)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to convert prefixes: %v", err)
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

	prefixes, err := commonpb.PrefixesFromNetworks(req.GetPrefixes())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to convert prefixes: %v", err)
	}

	prefixes = slices.Compact(
		slices.SortedFunc(
			slices.Values(prefixes),
			comparePrefixes,
		),
	)

	m.mu.Lock()
	defer m.mu.Unlock()

	cfg := &config{Prefixes: prefixes}
	if err := m.updateConfig(name, cfg); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update module config %q: %v", name, err)
	}

	return &decappb.UpdateConfigResponse{}, nil
}

func comparePrefixes(first, second netip.Prefix) int {
	return cmp.Or(
		first.Addr().Compare(second.Addr()),
		cmp.Compare(first.Bits(), second.Bits()),
	)
}

// updateConfig calls the backend to publish cfg, retries this service's
// deferred handles (the publish retired the generations that were
// holding them), frees or defers the old module handle, and stores the
// new config. The caller must hold m.mu.
func (m *DecapService) updateConfig(name string, cfg *config) error {
	mod, err := m.backend.UpdateModule(name, cfg.Prefixes)
	if err != nil {
		return fmt.Errorf("failed to update module config %q: %w", name, err)
	}

	m.reclaimDeferred()

	if old, ok := m.configs[name]; ok {
		m.parkOrFree(old)
	}

	m.configs[name] = &config{
		Prefixes: cfg.Prefixes,
		Module:   mod,
	}

	return nil
}

// parkOrFree frees the handle when it is dangling and parks it for
// retry when a live generation still references it. The caller must hold
// m.mu.
func (m *DecapService) parkOrFree(handle ModuleHandle) {
	if err := handle.Free(); errors.Is(err, ffi.ErrStillReferenced) {
		m.deferred = append(m.deferred, handle)
	}
}

// ReclaimDeferred retries every deferred handle, dropping the ones whose
// generations have drained and keeping the rest deferred. It is the
// reclamation handler for this module's superseded configs; the service
// itself runs it after each successful publish, and anything else may
// call it at any time.
func (m *DecapService) ReclaimDeferred() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reclaimDeferred()
}

// reclaimDeferred is ReclaimDeferred without the lock. The caller must
// hold m.mu.
func (m *DecapService) reclaimDeferred() {
	kept := m.deferred[:0]
	for _, handle := range m.deferred {
		if err := handle.Free(); errors.Is(err, ffi.ErrStillReferenced) {
			kept = append(kept, handle)
		}
	}
	clear(m.deferred[len(kept):])
	m.deferred = kept
}
