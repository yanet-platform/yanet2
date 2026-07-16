package l3b

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	l3bpb "github.com/yanet-platform/yanet2/modules/l3b/controlplane/l3bpb/v1"
)

var errServiceNameRequired = status.Error(codes.InvalidArgument, "service name is required")
var errModuleNameRequired = status.Error(codes.InvalidArgument, "module config name is required")

// backendError preserves a gRPC status returned by the backend and wraps any
// other error as Internal.
func backendError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	return status.Error(codes.Internal, err.Error())
}

// L3BService implements the L3BService gRPC server.
type L3BService struct {
	l3bpb.UnimplementedL3BServiceServer

	backend Backend
}

// NewL3BService constructs an L3BService backed by the given Backend.
func NewL3BService(backend Backend) *L3BService {
	return &L3BService{
		backend: backend,
	}
}

// CreateService creates a named virtual service.
func (m *L3BService) CreateService(
	ctx context.Context,
	req *l3bpb.CreateServiceRequest,
) (*l3bpb.CreateServiceResponse, error) {
	service := req.GetService()
	if service == nil || service.GetName() == "" {
		return nil, errServiceNameRequired
	}

	if err := m.backend.CreateService(service); err != nil {
		return nil, backendError(err)
	}

	return &l3bpb.CreateServiceResponse{}, nil
}

// UpdateService replaces an existing named virtual service.
func (m *L3BService) UpdateService(
	ctx context.Context,
	req *l3bpb.UpdateServiceRequest,
) (*l3bpb.UpdateServiceResponse, error) {
	service := req.GetService()
	if service == nil || service.GetName() == "" {
		return nil, errServiceNameRequired
	}

	if err := m.backend.UpdateService(service); err != nil {
		return nil, backendError(err)
	}

	return &l3bpb.UpdateServiceResponse{}, nil
}

// DeleteService removes a named virtual service.
func (m *L3BService) DeleteService(
	ctx context.Context,
	req *l3bpb.DeleteServiceRequest,
) (*l3bpb.DeleteServiceResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, errServiceNameRequired
	}

	if err := m.backend.DeleteService(name); err != nil {
		return nil, backendError(err)
	}

	return &l3bpb.DeleteServiceResponse{Deleted: true}, nil
}

// ListServices returns the names of all virtual services.
func (m *L3BService) ListServices(
	ctx context.Context,
	req *l3bpb.ListServicesRequest,
) (*l3bpb.ListServicesResponse, error) {
	return &l3bpb.ListServicesResponse{Services: m.backend.ListServices()}, nil
}

// UpdateModuleConfig installs destination filters and services into a named
// module configuration.
func (m *L3BService) UpdateModuleConfig(
	ctx context.Context,
	req *l3bpb.UpdateModuleConfigRequest,
) (*l3bpb.UpdateModuleConfigResponse, error) {
	config := req.GetConfig()
	if config == nil || config.GetName() == "" {
		return nil, errModuleNameRequired
	}

	if err := m.backend.UpdateModuleConfig(config); err != nil {
		return nil, backendError(err)
	}

	return &l3bpb.UpdateModuleConfigResponse{}, nil
}

// ListModuleConfigs returns the names of all module configurations.
func (m *L3BService) ListModuleConfigs(
	ctx context.Context,
	req *l3bpb.ListModuleConfigsRequest,
) (*l3bpb.ListModuleConfigsResponse, error) {
	return &l3bpb.ListModuleConfigsResponse{Configs: m.backend.ListModuleConfigs()}, nil
}
