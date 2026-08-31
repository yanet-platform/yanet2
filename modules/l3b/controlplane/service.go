package l3b

import (
	"context"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	l3bpb "github.com/yanet-platform/yanet2/modules/l3b/controlplane/l3bpb/v1"
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

// L3BService implements the L3BService gRPC server.
type L3BService struct {
	l3bpb.UnimplementedL3BServiceServer

	mu      sync.Mutex
	backend Backend
	configs map[string]*config
}

// NewL3BService constructs an L3BService backed by the given Backend.
func NewL3BService(backend Backend) *L3BService {
	return &L3BService{
		backend: backend,
		configs: map[string]*config{},
	}
}

// ListConfigs returns all known config names across all dataplane instances.
func (m *L3BService) ListConfigs(
	ctx context.Context,
	req *l3bpb.ListConfigsRequest,
) (*l3bpb.ListConfigsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	names := make([]string, 0, len(m.configs))
	for name := range m.configs {
		names = append(names, name)
	}

	return &l3bpb.ListConfigsResponse{Configs: names}, nil
}

// ShowConfig returns the named config when it exists.
func (m *L3BService) ShowConfig(
	ctx context.Context,
	req *l3bpb.ShowConfigRequest,
) (*l3bpb.ShowConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.configs[name]; !ok {
		return nil, status.Error(codes.NotFound, "no config found")
	}

	return &l3bpb.ShowConfigResponse{Name: name}, nil
}

// UpdateConfig creates or replaces the named config and publishes it to the
// dataplane.
func (m *L3BService) UpdateConfig(
	ctx context.Context,
	req *l3bpb.UpdateConfigRequest,
) (*l3bpb.UpdateConfigResponse, error) {
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

	return &l3bpb.UpdateConfigResponse{}, nil
}

// updateConfig publishes a fresh config and, on success, frees the old module
// handle and stores the new one.
//
// The caller must hold m.mu.
func (m *L3BService) updateConfig(name string) error {
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
func (m *L3BService) DeleteConfig(
	ctx context.Context,
	req *l3bpb.DeleteConfigRequest,
) (*l3bpb.DeleteConfigResponse, error) {
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

	return &l3bpb.DeleteConfigResponse{Deleted: true}, nil
}
