package dscp

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

	backend Backend
	configs *configstore.Store[*config]
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

// Clone copies the prefix sets and marking but never the published handle,
// so a copy never inherits ownership of the shared-memory config.
func (m *config) Clone() *config {
	return &config{
		Prefixes4: slices.Clone(m.Prefixes4),
		Prefixes6: slices.Clone(m.Prefixes6),
		Config:    m.Config,
	}
}

type dscpConfig struct {
	Flag uint8
	Mark uint8
}

func NewDscpService(backend Backend) *DscpService {
	return &DscpService{
		backend: backend,
		configs: configstore.NewStore[*config](),
	}
}

func (m *DscpService) ListConfigs(
	ctx context.Context,
	request *dscppb.ListConfigsRequest,
) (*dscppb.ListConfigsResponse, error) {
	return &dscppb.ListConfigsResponse{Configs: m.configs.Names()}, nil
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

	config, ok := m.configs.Get(name)
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

	err = m.publish(name, func(cfg *config) {
		cfg.Prefixes4 = mergePrefixes(cfg.Prefixes4, toAdd4)
		cfg.Prefixes6 = mergePrefixes(cfg.Prefixes6, toAdd6)
	})
	if err != nil {
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

	err = m.publish(name, func(cfg *config) {
		cfg.Prefixes4 = removePrefixes(cfg.Prefixes4, toRemove4)
		cfg.Prefixes6 = removePrefixes(cfg.Prefixes6, toRemove6)
	})
	if err != nil {
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

	err := m.publish(name, func(cfg *config) {
		cfg.Config = dscpConfig{
			Flag: flag,
			Mark: mark,
		}
	})
	if err != nil {
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

	err := m.configs.Delete(name, func(*config) error {
		return m.backend.DeleteModule(name)
	})
	if errors.Is(err, configstore.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "config not found")
	}
	if err != nil {
		return nil, status.Errorf(
			codes.Internal,
			"failed to delete module config %q: %v", name, err,
		)
	}

	return &dscppb.DeleteConfigResponse{}, nil
}

// publish applies a mutation to a copy of the named config, starting from
// an empty one when the name is new, and publishes the result.
func (m *DscpService) publish(name string, mutate func(cfg *config)) error {
	return m.configs.Update(name, func(current *config, ok bool) (*config, error) {
		cfg := &config{}
		if ok {
			cfg = current.Clone()
		}
		mutate(cfg)

		module, err := m.backend.UpdateModule(
			name,
			slices.Concat(cfg.Prefixes4, cfg.Prefixes6),
			cfg.Config.Flag,
			cfg.Config.Mark,
		)
		if err != nil {
			return nil, err
		}
		cfg.Module = module

		return cfg, nil
	})
}

// ReclaimDeferred retries every superseded config whose free was refused,
// releasing the ones whose generations have drained.
//
// The service runs it after each successful publish, and anything else
// may call it at any time.
func (m *DscpService) ReclaimDeferred() {
	m.configs.ReclaimDeferred()
}
