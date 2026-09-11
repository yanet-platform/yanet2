package unrdup

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/xnetip"
	"github.com/yanet-platform/yanet2/controlplane/configstore"
	"github.com/yanet-platform/yanet2/modules/unrdup/bindings/go/cunrdup"
	"github.com/yanet-platform/yanet2/modules/unrdup/controlplane/unrduppb/v1"
)

var errConfigNameRequired = status.Error(
	codes.InvalidArgument,
	"config name is required",
)

func validateConfigName(name string) error {
	if name == "" {
		return errConfigNameRequired
	}
	if strings.IndexByte(name, 0) >= 0 {
		return status.Error(
			codes.InvalidArgument, "config name must not contain a NUL byte",
		)
	}
	if len(name) > cunrdup.ModuleNameMaxLen {
		return status.Errorf(
			codes.InvalidArgument,
			"config name is %d bytes, the dataplane keeps at most %d",
			len(name),
			cunrdup.ModuleNameMaxLen,
		)
	}

	return nil
}

// ModuleHandle is a handle to a module configuration.
type ModuleHandle interface {
	Free() error
}

// Backend abstracts shared memory operations.
type Backend interface {
	// UpdateModule publishes a module config to the dataplane.
	UpdateModule(
		name string,
		sources []xnetip.Network,
		services []cunrdup.Service,
	) (ModuleHandle, error)
	// DeleteModule removes a module config.
	DeleteModule(name string) error
}

type UnrdupService struct {
	unrduppb.UnimplementedUnrdupServiceServer

	backend Backend
	configs *configstore.Store[*config]
}

// ReclaimDeferred retries every superseded config whose free was refused,
// releasing the ones whose generations have drained.
//
// The service runs it after each successful publish, and anything else
// may call it at any time.
func (m *UnrdupService) ReclaimDeferred() {
	m.configs.ReclaimDeferred()
}

type config struct {
	SourceV4 xnetip.Network
	SourceV6 xnetip.Network
	Services []cunrdup.Service
	Module   ModuleHandle
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

func (m *config) Sources() []xnetip.Network {
	sources := make([]xnetip.Network, 0, 2)
	if sourceIsSet(m.SourceV4) {
		sources = append(sources, m.SourceV4)
	}
	if sourceIsSet(m.SourceV6) {
		sources = append(sources, m.SourceV6)
	}

	return sources
}

func NewUnrdupService(backend Backend) *UnrdupService {
	return &UnrdupService{
		backend: backend,
		configs: configstore.NewStore[*config](),
	}
}

func (m *UnrdupService) ListConfigs(
	ctx context.Context,
	request *unrduppb.ListConfigsRequest,
) (*unrduppb.ListConfigsResponse, error) {
	return &unrduppb.ListConfigsResponse{Configs: m.configs.Names()}, nil
}

func (m *UnrdupService) ShowConfig(
	ctx context.Context,
	request *unrduppb.ShowConfigRequest,
) (*unrduppb.ShowConfigResponse, error) {
	name := request.GetName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	current, ok := m.configs.Get(name)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q is not found", name)
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
	if err := validateConfigName(name); err != nil {
		return nil, err
	}

	updated, err := configFromProto(request.GetConfig())
	if err != nil {
		return nil, err
	}

	err = m.configs.Update(name, func(*config, bool) (*config, error) {
		module, err := m.backend.UpdateModule(name, updated.Sources(), updated.Services)
		if err != nil {
			return nil, err
		}
		updated.Module = module
		return updated, nil
	})
	if err != nil {
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
	if name == "" {
		return nil, errConfigNameRequired
	}

	err := m.configs.Delete(name, func(*config) error {
		return m.backend.DeleteModule(name)
	})
	if errors.Is(err, configstore.ErrNotFound) {
		return nil, status.Errorf(codes.NotFound, "config %q is not found", name)
	}
	if err != nil {
		return nil, status.Errorf(
			codes.Internal, "failed to delete config %q: %s", name, err,
		)
	}

	return &unrduppb.DeleteConfigResponse{}, nil
}
