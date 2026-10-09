package example

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/configstore"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	examplepb "github.com/yanet-platform/yanet2/sdk/example/controlplane/examplepb/v1"
)

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

// ExampleService implements the ExampleService gRPC server.
type ExampleService struct {
	examplepb.UnimplementedExampleServiceServer

	backend Backend
	configs *configstore.Store[*config]
}

// NewExampleService constructs an ExampleService backed by the given Backend.
func NewExampleService(backend Backend) *ExampleService {
	return &ExampleService{
		backend: backend,
		configs: configstore.NewStore[*config](),
	}
}

// ListConfigs returns all known config names across all dataplane instances.
func (m *ExampleService) ListConfigs(
	ctx context.Context,
	req *examplepb.ListConfigsRequest,
) (*examplepb.ListConfigsResponse, error) {
	return &examplepb.ListConfigsResponse{Configs: m.configs.Names()}, nil
}

// ShowConfig returns the named config when it exists.
func (m *ExampleService) ShowConfig(
	ctx context.Context,
	req *examplepb.ShowConfigRequest,
) (*examplepb.ShowConfigResponse, error) {
	name := req.GetName()
	if _, ok := m.configs.Get(name); !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	return &examplepb.ShowConfigResponse{Name: name}, nil
}

// UpdateConfig creates or replaces the named config and publishes it to the
// dataplane.
func (m *ExampleService) UpdateConfig(
	ctx context.Context,
	req *examplepb.UpdateConfigRequest,
) (*examplepb.UpdateConfigResponse, error) {
	name := req.GetName()
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

	return &examplepb.UpdateConfigResponse{}, nil
}

// DeleteConfig removes the named config if it is not referenced by any
// pipeline.
func (m *ExampleService) DeleteConfig(
	ctx context.Context,
	req *examplepb.DeleteConfigRequest,
) (*examplepb.DeleteConfigResponse, error) {
	name := req.GetName()
	err := m.configs.Delete(name, func(*config) error {
		return m.backend.DeleteModule(name)
	})
	if errors.Is(err, configstore.ErrNotFound) {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}
	if err != nil {
		code := codes.Internal
		if errors.Is(err, ffi.ErrFailedPrecondition) {
			// A chain still references the config.
			code = codes.FailedPrecondition
		}
		return nil, status.Errorf(code, "failed to delete module config %q: %v", name, err)
	}

	return &examplepb.DeleteConfigResponse{}, nil
}
