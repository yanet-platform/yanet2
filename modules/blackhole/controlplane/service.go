package blackhole

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/crud"
	blackholepb "github.com/yanet-platform/yanet2/modules/blackhole/controlplane/blackholepb/v1"
)

// Backend abstracts shared memory operations.
type Backend interface {
	// Update creates a module config, which carries no fields, and
	// publishes it to the dataplane.
	Update(name string, payload struct{}) (crud.Handle, error)
	// Delete removes a module config.
	Delete(name string) error
}

// BlackholeService implements the BlackholeService gRPC server.
type BlackholeService struct {
	blackholepb.UnimplementedBlackholeServiceServer

	core *crud.Core[struct{}]
}

// NewBlackholeService constructs a BlackholeService backed by the given Backend.
func NewBlackholeService(backend Backend) *BlackholeService {
	return &BlackholeService{
		core: crud.NewCore[struct{}](backend),
	}
}

// ListConfigs returns all known config names across all dataplane instances.
func (m *BlackholeService) ListConfigs(
	ctx context.Context,
	req *blackholepb.ListConfigsRequest,
) (*blackholepb.ListConfigsResponse, error) {
	return &blackholepb.ListConfigsResponse{Configs: m.core.List()}, nil
}

// ShowConfig returns the named config when it exists.
func (m *BlackholeService) ShowConfig(
	ctx context.Context,
	req *blackholepb.ShowConfigRequest,
) (*blackholepb.ShowConfigResponse, error) {
	name := req.GetName()
	if _, ok := m.core.Get(name); !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	return &blackholepb.ShowConfigResponse{Name: name}, nil
}

// UpdateConfig creates or replaces the named config and publishes it to the
// dataplane.
func (m *BlackholeService) UpdateConfig(
	ctx context.Context,
	req *blackholepb.UpdateConfigRequest,
) (*blackholepb.UpdateConfigResponse, error) {
	name := req.GetName()
	if err := m.core.Replace(name, struct{}{}); err != nil {
		return nil, status.Errorf(
			codes.Internal,
			"failed to update module config %q: %v", name, err,
		)
	}

	return &blackholepb.UpdateConfigResponse{}, nil
}

// DeleteConfig removes the named config if it is not referenced by any
// pipeline.
func (m *BlackholeService) DeleteConfig(
	ctx context.Context,
	req *blackholepb.DeleteConfigRequest,
) (*blackholepb.DeleteConfigResponse, error) {
	name := req.GetName()
	if err := m.core.Delete(name); err != nil {
		return nil, crud.DeleteError(name, err)
	}

	return &blackholepb.DeleteConfigResponse{}, nil
}
