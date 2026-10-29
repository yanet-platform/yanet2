package unrdup

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/xnetip"
	"github.com/yanet-platform/yanet2/controlplane/crud"
	"github.com/yanet-platform/yanet2/modules/unrdup/bindings/go/cunrdup"
	"github.com/yanet-platform/yanet2/modules/unrdup/controlplane/unrduppb/v1"
)

// ModuleConfig is the published payload: the source addresses the
// duplicated traffic is sent from and the services it is duplicated
// for.
type ModuleConfig struct {
	SourceV4 xnetip.Network
	SourceV6 xnetip.Network
	Services []cunrdup.Service
}

func (m *ModuleConfig) Sources() []xnetip.Network {
	sources := make([]xnetip.Network, 0, 2)
	if sourceIsSet(m.SourceV4) {
		sources = append(sources, m.SourceV4)
	}
	if sourceIsSet(m.SourceV6) {
		sources = append(sources, m.SourceV6)
	}

	return sources
}

// Backend abstracts shared memory operations.
type Backend interface {
	// Update builds a module config from the sources and services and
	// publishes it to the dataplane.
	Update(name string, payload *ModuleConfig) (crud.Handle, error)
	// Delete removes a module config.
	Delete(name string) error
}

type UnrdupService struct {
	unrduppb.UnimplementedUnrdupServiceServer

	core *crud.Core[*ModuleConfig]
}

func NewUnrdupService(backend Backend) *UnrdupService {
	return &UnrdupService{
		core: crud.NewCore(backend),
	}
}

func (m *UnrdupService) ListConfigs(
	ctx context.Context,
	request *unrduppb.ListConfigsRequest,
) (*unrduppb.ListConfigsResponse, error) {
	return &unrduppb.ListConfigsResponse{Configs: m.core.List()}, nil
}

func (m *UnrdupService) ShowConfig(
	ctx context.Context,
	request *unrduppb.ShowConfigRequest,
) (*unrduppb.ShowConfigResponse, error) {
	name := request.GetName()
	current, ok := m.core.Get(name)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	return &unrduppb.ShowConfigResponse{
		Name:   name,
		Config: current.ToProto(),
	}, nil
}

func (m *UnrdupService) UpdateConfig(
	ctx context.Context,
	request *unrduppb.UpdateConfigRequest,
) (*unrduppb.UpdateConfigResponse, error) {
	name := request.GetName()
	updated, err := configFromProto(request.GetConfig())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	if err := m.core.Replace(name, updated); err != nil {
		return nil, status.Errorf(
			codes.Internal, "failed to update config %q: %s", name, err,
		)
	}

	return &unrduppb.UpdateConfigResponse{}, nil
}

// DeleteConfig removes the named config if it is not referenced by any
// pipeline.
func (m *UnrdupService) DeleteConfig(
	ctx context.Context,
	request *unrduppb.DeleteConfigRequest,
) (*unrduppb.DeleteConfigResponse, error) {
	name := request.GetName()
	if err := m.core.Delete(name); err != nil {
		return nil, crud.DeleteError(name, err)
	}

	return &unrduppb.DeleteConfigResponse{}, nil
}
