package decap

import (
	"cmp"
	"context"
	"net/netip"
	"slices"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/crud"
	"github.com/yanet-platform/yanet2/modules/decap/controlplane/decappb/v1"
)

// PrefixSets is the published payload: the sorted, deduplicated
// prefix sets.
type PrefixSets struct {
	// Prefixes4 is the sorted IPv4 prefix set.
	Prefixes4 []netip.Prefix
	// Prefixes6 is the sorted IPv6 prefix set.
	Prefixes6 []netip.Prefix
}

// Backend abstracts shared memory operations.
type Backend interface {
	// Update builds a module config from the prefix sets and publishes
	// it to the dataplane.
	Update(name string, payload PrefixSets) (crud.Handle, error)
	// Delete removes a module config.
	Delete(name string) error
}

// DecapService implements the DecapService gRPC server.
type DecapService struct {
	decappb.UnimplementedDecapServiceServer

	core *crud.Core[PrefixSets]
}

// NewDecapService constructs a DecapService backed by the given Backend.
func NewDecapService(backend Backend) *DecapService {
	return &DecapService{
		core: crud.NewCore(backend),
	}
}

// ListConfigs returns all known config names across all dataplane instances.
func (m *DecapService) ListConfigs(
	ctx context.Context,
	req *decappb.ListConfigsRequest,
) (*decappb.ListConfigsResponse, error) {
	return &decappb.ListConfigsResponse{Configs: m.core.List()}, nil
}

// ShowConfig returns the current prefix set for the named config.
func (m *DecapService) ShowConfig(
	ctx context.Context,
	req *decappb.ShowConfigRequest,
) (*decappb.ShowConfigResponse, error) {
	name := req.GetName()
	entry, ok := m.core.Get(name)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
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
	prefixes4, err := commonpb.PrefixesFromNetworks("prefixes4", req.GetPrefixes4())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to convert prefixes: %v", err)
	}
	prefixes6, err := commonpb.PrefixesFromNetworks("prefixes6", req.GetPrefixes6())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to convert prefixes: %v", err)
	}

	payload := PrefixSets{
		Prefixes4: normalizePrefixes(prefixes4),
		Prefixes6: normalizePrefixes(prefixes6),
	}
	if err := m.core.Replace(name, payload); err != nil {
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
	if err := m.core.Delete(name); err != nil {
		return nil, crud.DeleteError(name, err)
	}

	return &decappb.DeleteConfigResponse{}, nil
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
