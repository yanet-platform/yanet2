package l3b

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	l3bpb "github.com/yanet-platform/yanet2/modules/l3b/controlplane/l3bpb/v1"
)

var errServiceNameRequired = status.Error(codes.InvalidArgument, "service name is required")
var errModuleNameRequired = status.Error(codes.InvalidArgument, "module config name is required")

// maxNameLength matches the C registry's fixed name buffers; a longer name
// would be silently truncated into a different object's identity.
const maxNameLength = 79

// validateName rejects names that cannot round-trip through the C registry:
// longer than its fixed buffer, empty, or containing unprintable bytes.
func validateName(kind, name string) error {
	if name == "" {
		return status.Errorf(codes.InvalidArgument, "%s name is required", kind)
	}
	if len(name) > maxNameLength {
		return status.Errorf(
			codes.InvalidArgument,
			"%s name must be at most %d bytes", kind, maxNameLength,
		)
	}
	for idx := 0; idx < len(name); idx++ {
		if name[idx] < 0x20 || name[idx] == 0x7f {
			return status.Errorf(
				codes.InvalidArgument,
				"%s name must contain only printable bytes", kind,
			)
		}
	}
	return nil
}

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
	if service == nil {
		return nil, errServiceNameRequired
	}
	if err := validateName("service", service.GetName()); err != nil {
		return nil, err
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
	if service == nil {
		return nil, errServiceNameRequired
	}
	if err := validateName("service", service.GetName()); err != nil {
		return nil, err
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
	if err := validateName("service", name); err != nil {
		return nil, err
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
	if config == nil {
		return nil, errModuleNameRequired
	}
	if err := validateName("module config", config.GetName()); err != nil {
		return nil, err
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

// UpdateRealServerState enables or disables a real server within a named
// virtual service.
func (m *L3BService) UpdateRealServerState(
	ctx context.Context,
	req *l3bpb.UpdateRealServerStateRequest,
) (*l3bpb.UpdateRealServerStateResponse, error) {
	if req.GetService() == "" {
		return nil, errServiceNameRequired
	}

	if err := m.backend.UpdateRealServerState(req.GetService(), req.GetRealServerIndex(), req.GetEnabled()); err != nil {
		return nil, backendError(err)
	}

	return &l3bpb.UpdateRealServerStateResponse{}, nil
}

// UpdateRealServerWeight sets the weight of a real server within a named
// virtual service.
func (m *L3BService) UpdateRealServerWeight(
	ctx context.Context,
	req *l3bpb.UpdateRealServerWeightRequest,
) (*l3bpb.UpdateRealServerWeightResponse, error) {
	if req.GetService() == "" {
		return nil, errServiceNameRequired
	}

	if err := m.backend.UpdateRealServerWeight(req.GetService(), req.GetRealServerIndex(), req.GetWeight()); err != nil {
		return nil, backendError(err)
	}

	return &l3bpb.UpdateRealServerWeightResponse{}, nil
}

// ListSessions pages through the session records of a named virtual service.
func (m *L3BService) ListSessions(
	ctx context.Context,
	req *l3bpb.ListSessionsRequest,
) (*l3bpb.ListSessionsResponse, error) {
	service := req.GetService()
	if err := validateName("service", service); err != nil {
		return nil, err
	}

	sessions, nextCursor, nowNs, err := m.backend.ListSessions(
		service,
		req.GetCursor(),
		req.GetLimit(),
	)
	if err != nil {
		return nil, backendError(err)
	}

	records := make([]*l3bpb.SessionRecord, 0, len(sessions))
	for _, session := range sessions {
		var sourceBytes []byte
		var realBytes []byte
		if session.SourceAddress.Is4() {
			octets := session.SourceAddress.As4()
			sourceBytes = octets[:]
		} else {
			octets := session.SourceAddress.As16()
			sourceBytes = octets[:]
		}
		if session.RealAddress.Is4() {
			octets := session.RealAddress.As4()
			realBytes = octets[:]
		} else {
			octets := session.RealAddress.As16()
			realBytes = octets[:]
		}

		records = append(records, &l3bpb.SessionRecord{
			SourceAddress: sourceBytes,
			SourcePort:    uint32(session.SourcePort),
			RealAddress:   realBytes,
			ExpiresAt:     session.ExpiresAt,
		})
	}

	return &l3bpb.ListSessionsResponse{
		Sessions:   records,
		NextCursor: nextCursor,
		NowNs:      nowNs,
	}, nil
}
