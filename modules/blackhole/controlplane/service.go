package blackhole

import (
	"context"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	blackholepb "github.com/yanet-platform/yanet2/modules/blackhole/controlplane/blackholepb/v1"
)

var errConfigNameRequired = status.Error(codes.InvalidArgument, "config name is required")

// ModuleHandle is a handle to a module configuration.
type ModuleHandle interface {
	Free()
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
	module ModuleHandle
}

// BlackholeService implements the BlackholeService gRPC server.
type BlackholeService struct {
	blackholepb.UnimplementedBlackholeServiceServer

	mu      sync.Mutex
	backend Backend
	configs map[string]*config
}

// NewBlackholeService constructs a BlackholeService backed by the given Backend.
func NewBlackholeService(backend Backend) *BlackholeService {
	return &BlackholeService{
		backend: backend,
		configs: map[string]*config{},
	}
}

// ListConfigs returns all known config names across all dataplane instances.
func (m *BlackholeService) ListConfigs(
	ctx context.Context,
	req *blackholepb.ListConfigsRequest,
) (*blackholepb.ListConfigsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	names := make([]string, 0, len(m.configs))
	for name := range m.configs {
		names = append(names, name)
	}

	return &blackholepb.ListConfigsResponse{Configs: names}, nil
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

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.configs[name]; !ok {
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

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.updateConfig(name); err != nil {
		return nil, status.Errorf(
			codes.Internal,
			"failed to update module config %q: %v", name, err,
		)
	}

	return &blackholepb.UpdateConfigResponse{}, nil
}

// updateConfig publishes a fresh config and, on success, frees the old module
// handle and stores the new one.
//
// The caller must hold m.mu.
func (m *BlackholeService) updateConfig(name string) error {
	mod, err := m.backend.UpdateModule(name)
	if err != nil {
		return err
	}

	if old, ok := m.configs[name]; ok && old.module != nil {
		old.module.Free()
	}

	m.configs[name] = &config{module: mod}

	return nil
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

	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.configs[name]
	if !ok {
		return nil, status.Error(codes.NotFound, "no config found")
	}

	if err := m.backend.DeleteModule(name); err != nil {
		return nil, status.Errorf(
			codes.Internal,
			"failed to delete module config %q: %v", name, err,
		)
	}

	if entry.module != nil {
		entry.module.Free()
	}

	delete(m.configs, name)

	return &blackholepb.DeleteConfigResponse{Deleted: true}, nil
}
