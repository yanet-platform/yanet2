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
	// Prefixes4 is the sorted IPv4 destination prefix set.
	Prefixes4 []netip.Prefix
	// Prefixes6 is the sorted IPv6 destination prefix set.
	Prefixes6 []netip.Prefix
	// SourcePrefixes4 is the sorted IPv4 source prefix set.
	SourcePrefixes4 []netip.Prefix
	// SourcePrefixes6 is the sorted IPv6 source prefix set.
	SourcePrefixes6 []netip.Prefix
	// Config is the DSCP marking configuration.
	Config dscpConfig
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
	sourcePrefixes4, err := commonpb.NewIPv4PrefixesFromPrefixes(published.SourcePrefixes4)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to convert source prefixes: %v", err)
	}
	sourcePrefixes6, err := commonpb.NewIPv6PrefixesFromPrefixes(published.SourcePrefixes6)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to convert source prefixes: %v", err)
	}

	response.Config = &dscppb.Config{
		Prefixes4:       prefixes4,
		Prefixes6:       prefixes6,
		SourcePrefixes4: sourcePrefixes4,
		SourcePrefixes6: sourcePrefixes6,
		DscpConfig: &dscppb.DscpConfig{
			Flag: uint32(published.Config.Flag),
			Mark: uint32(published.Config.Mark),
		},
	}

	return response, nil
}

// UpdateConfig replaces the whole named config, its prefix sets and its
// marking, with the one the request carries.
//
// The prefix sets are kept sorted and duplicate-free, and an invalid prefix
// publishes nothing.
func (m *DscpService) UpdateConfig(
	ctx context.Context,
	request *dscppb.UpdateConfigRequest,
) (*dscppb.UpdateConfigResponse, error) {
	name := request.GetName()
	desired := request.GetConfig()
	prefixes4, err := commonpb.PrefixesFromNetworks("prefixes4", desired.GetPrefixes4())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to convert prefixes: %v", err)
	}
	prefixes6, err := commonpb.PrefixesFromNetworks("prefixes6", desired.GetPrefixes6())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to convert prefixes: %v", err)
	}
	sourcePrefixes4, err := commonpb.PrefixesFromNetworks("source_prefixes4", desired.GetSourcePrefixes4())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to convert source prefixes: %v", err)
	}
	sourcePrefixes6, err := commonpb.PrefixesFromNetworks("source_prefixes6", desired.GetSourcePrefixes6())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to convert source prefixes: %v", err)
	}

	payload := &config{
		Prefixes4:       sortedPrefixes(prefixes4),
		Prefixes6:       sortedPrefixes(prefixes6),
		SourcePrefixes4: sortedPrefixes(sourcePrefixes4),
		SourcePrefixes6: sortedPrefixes(sourcePrefixes6),
		Config: dscpConfig{
			Flag: uint8(desired.GetDscpConfig().GetFlag()),
			Mark: uint8(desired.GetDscpConfig().GetMark()),
		},
	}
	if err := m.core.Replace(name, payload); err != nil {
		return nil, crud.PublishError(name, err)
	}

	return &dscppb.UpdateConfigResponse{}, nil
}

// sortedPrefixes returns a sorted, duplicate-free copy of the prefixes.
func sortedPrefixes(prefixes []netip.Prefix) []netip.Prefix {
	return slices.Compact(
		slices.SortedFunc(
			slices.Values(prefixes),
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
