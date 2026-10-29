package dscp

import (
	"cmp"
	"context"
	"net/netip"
	"slices"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/crud"
	"github.com/yanet-platform/yanet2/modules/dscp/controlplane/dscppb/v1"
)

// Backend abstracts shared memory operations.
type Backend interface {
	// Update builds a module config from the prefix sets and the marking
	// and publishes it to the dataplane.
	Update(name string, payload *config) (crud.Handle, error)
	// Delete removes a module config.
	Delete(name string) error
}

type DscpService struct {
	dscppb.UnimplementedDscpServiceServer

	core *crud.Core[*config]
}

type config struct {
	// Prefixes4 is the sorted IPv4 prefix set.
	Prefixes4 []netip.Prefix
	// Prefixes6 is the sorted IPv6 prefix set.
	Prefixes6 []netip.Prefix
	// Config is the DSCP marking configuration.
	Config dscpConfig
}

// Clone copies the prefix sets and marking.
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
		core: crud.NewCore(backend),
	}
}

func (m *DscpService) ListConfigs(
	ctx context.Context,
	request *dscppb.ListConfigsRequest,
) (*dscppb.ListConfigsResponse, error) {
	return &dscppb.ListConfigsResponse{Configs: m.core.List()}, nil
}

func (m *DscpService) ShowConfig(
	ctx context.Context,
	request *dscppb.ShowConfigRequest,
) (*dscppb.ShowConfigResponse, error) {
	name := request.GetName()
	response := &dscppb.ShowConfigResponse{}

	published, ok := m.core.Get(name)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	prefixes4, err := commonpb.NewIPv4PrefixesFromPrefixes(published.Prefixes4)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to convert prefixes: %v", err)
	}
	prefixes6, err := commonpb.NewIPv6PrefixesFromPrefixes(published.Prefixes6)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to convert prefixes: %v", err)
	}

	response.Config = &dscppb.Config{
		Prefixes4: prefixes4,
		Prefixes6: prefixes6,
		DscpConfig: &dscppb.DscpConfig{
			Flag: uint32(published.Config.Flag),
			Mark: uint32(published.Config.Mark),
		},
	}

	return response, nil
}

func (m *DscpService) AddPrefixes(
	ctx context.Context,
	request *dscppb.AddPrefixesRequest,
) (*dscppb.AddPrefixesResponse, error) {
	name := request.GetName()
	toAdd4, err := commonpb.PrefixesFromNetworks("prefixes4", request.GetPrefixes4())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to convert prefixes: %v", err)
	}
	toAdd6, err := commonpb.PrefixesFromNetworks("prefixes6", request.GetPrefixes6())
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
	name := request.GetName()
	toRemove4, err := commonpb.PrefixesFromNetworks("prefixes4", request.GetPrefixes4())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to convert prefixes: %v", err)
	}
	toRemove6, err := commonpb.PrefixesFromNetworks("prefixes6", request.GetPrefixes6())
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
	name := request.GetName()
	if err := m.core.Delete(name); err != nil {
		return nil, crud.DeleteError(name, err)
	}

	return &dscppb.DeleteConfigResponse{}, nil
}

// publish applies a mutation to a copy of the named config, starting from
// an empty one when the name is new, and publishes the result.
func (m *DscpService) publish(name string, mutate func(cfg *config)) error {
	return m.core.Mutate(name, func(current *config, ok bool) (*config, error) {
		cfg := &config{}
		if ok {
			cfg = current.Clone()
		}
		mutate(cfg)
		return cfg, nil
	})
}
