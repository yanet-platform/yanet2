package decap

import (
	"cmp"
	"context"
	"errors"
	"net/netip"
	"slices"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/configstore"
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
	// DeleteModule removes a module config.
	DeleteModule(name string) error
}

type config struct {
	// Prefixes4 is the sorted IPv4 prefix set.
	Prefixes4 []netip.Prefix
	// Prefixes6 is the sorted IPv6 prefix set.
	Prefixes6 []netip.Prefix
	// Module is the shared-memory handle of the published config.
	Module ModuleHandle
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

	backend Backend
	configs *configstore.Store[*config]
}

// NewDecapService constructs a DecapService backed by the given Backend.
func NewDecapService(backend Backend) *DecapService {
	return &DecapService{
		backend: backend,
		configs: configstore.NewStore[*config](),
	}
}

// ListConfigs returns all known config names across all dataplane instances.
func (m *DecapService) ListConfigs(
	ctx context.Context,
	req *decappb.ListConfigsRequest,
) (*decappb.ListConfigsResponse, error) {
	return &decappb.ListConfigsResponse{Configs: m.configs.Names()}, nil
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

	entry, ok := m.configs.Get(name)
	if !ok {
		return nil, status.Error(codes.NotFound, "no config found")
	}

	prefixes4, err := commonpb.NewIPv4PrefixesFromPrefixes(entry.Prefixes4)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to convert prefixes: %v", err)
	}
	prefixes6, err := commonpb.NewIPv6PrefixesFromPrefixes(entry.Prefixes6)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to convert prefixes: %v", err)
	}

	return &decappb.ShowConfigResponse{Prefixes4: prefixes4, Prefixes6: prefixes6}, nil
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

	prefixes4, err := commonpb.PrefixesFromNetworks(req.GetPrefixes4())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to convert prefixes: %v", err)
	}
	prefixes6, err := commonpb.PrefixesFromNetworks(req.GetPrefixes6())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to convert prefixes: %v", err)
	}

	cfg := &config{
		Prefixes4: normalizePrefixes(prefixes4),
		Prefixes6: normalizePrefixes(prefixes6),
	}
	err = m.configs.Update(name, func(*config, bool) (*config, error) {
		module, err := m.backend.UpdateModule(name, slices.Concat(cfg.Prefixes4, cfg.Prefixes6))
		if err != nil {
			return nil, err
		}
		cfg.Module = module
		return cfg, nil
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update module config %q: %v", name, err)
	}

	return &decappb.UpdateConfigResponse{}, nil
}

// DeleteConfig removes the named config if it is not referenced by any
// pipeline.
func (m *DecapService) DeleteConfig(
	ctx context.Context,
	req *decappb.DeleteConfigRequest,
) (*decappb.DeleteConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	err := m.configs.Delete(name, func(*config) error {
		return m.backend.DeleteModule(name)
	})
	if errors.Is(err, configstore.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "no config found")
	}
	if err != nil {
		return nil, status.Errorf(
			codes.Internal,
			"failed to delete module config %q: %v", name, err,
		)
	}

	return &decappb.DeleteConfigResponse{}, nil
}

// ReclaimDeferred retries every superseded config whose free was refused,
// releasing the ones whose generations have drained. The service runs it
// after each successful publish, and anything else may call it at any
// time.
func (m *DecapService) ReclaimDeferred() {
	m.configs.ReclaimDeferred()
}

func comparePrefixes(first, second netip.Prefix) int {
	return cmp.Or(
		first.Addr().Compare(second.Addr()),
		cmp.Compare(first.Bits(), second.Bits()),
	)
}

// normalizePrefixes sorts a prefix set and drops duplicates.
func normalizePrefixes(prefixes []netip.Prefix) []netip.Prefix {
	return slices.Compact(
		slices.SortedFunc(
			slices.Values(prefixes),
			comparePrefixes,
		),
	)
}
