package blackhole

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/configstore"
	blackholepb "github.com/yanet-platform/yanet2/modules/blackhole/controlplane/blackholepb/v1"
)

var errConfigNameRequired = status.Error(codes.InvalidArgument, "config name is required")

// ModuleHandle is a handle to a module configuration.
type ModuleHandle interface {
	Free() error
}

// Backend abstracts shared memory operations.
type Backend interface {
	// UpdateModule creates a module config and publishes it to the
	// dataplane.
	UpdateModule(name string) (ModuleHandle, error)
	// DeleteModule removes a module config.
	DeleteModule(name string) error
}

type config struct {
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

// BlackholeService implements the BlackholeService gRPC server.
type BlackholeService struct {
	blackholepb.UnimplementedBlackholeServiceServer

	backend Backend
	configs *configstore.Store[*config]
}

// NewBlackholeService constructs a BlackholeService backed by the given Backend.
func NewBlackholeService(backend Backend) *BlackholeService {
	return &BlackholeService{
		backend: backend,
		configs: configstore.NewStore[*config](),
	}
}

// ListConfigs returns all known config names across all dataplane instances.
func (m *BlackholeService) ListConfigs(
	ctx context.Context,
	req *blackholepb.ListConfigsRequest,
) (*blackholepb.ListConfigsResponse, error) {
	return &blackholepb.ListConfigsResponse{Configs: m.configs.Names()}, nil
}

// ShowConfig returns the named config when it exists.
func (m *BlackholeService) ShowConfig(
	ctx context.Context,
	req *blackholepb.ShowConfigRequest,
) (*blackholepb.ShowConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	if _, ok := m.configs.Get(name); !ok {
		return nil, status.Error(codes.NotFound, "no config found")
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
	if name == "" {
		return nil, errConfigNameRequired
	}

	err := m.configs.Update(name, func(*config, bool) (*config, error) {
		module, err := m.backend.UpdateModule(name)
		if err != nil {
			return nil, err
		}
		return &config{Module: module}, nil
	})
	if err != nil {
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

	return &blackholepb.DeleteConfigResponse{}, nil
}

// ReclaimDeferred retries every superseded config whose free was refused,
// releasing the ones whose generations have drained.
//
// The service runs it after each successful publish, and anything else
// may call it at any time.
func (m *BlackholeService) ReclaimDeferred() {
	m.configs.ReclaimDeferred()
}
