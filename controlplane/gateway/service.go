package gateway

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

func backendKindToProto(k BackendKind) ynpb.BackendKind {
	switch k {
	case BackendKindBuiltin:
		return ynpb.BackendKind_BACKEND_KIND_BUILTIN
	case BackendKindInProcess:
		return ynpb.BackendKind_BACKEND_KIND_IN_PROCESS
	case BackendKindExternal:
		return ynpb.BackendKind_BACKEND_KIND_EXTERNAL
	default:
		return ynpb.BackendKind_BACKEND_KIND_UNSPECIFIED
	}
}

func registrationStatusToProto(s RegistrationStatus) ynpb.RegistrationStatus {
	switch s {
	case RegistrationRegistered:
		return ynpb.RegistrationStatus_REGISTRATION_STATUS_REGISTERED
	case RegistrationRenewed:
		return ynpb.RegistrationStatus_REGISTRATION_STATUS_RENEWED
	case RegistrationUpdated:
		return ynpb.RegistrationStatus_REGISTRATION_STATUS_UPDATED
	default:
		return ynpb.RegistrationStatus_REGISTRATION_STATUS_UNSPECIFIED
	}
}

// GatewayService is the gRPC service for the Gateway API.
type GatewayService struct {
	ynpb.UnimplementedGatewayServer
	registry *BackendRegistry
	log      *zap.Logger
}

// NewGatewayService creates a new GatewayService.
func NewGatewayService(registry *BackendRegistry, log *zap.Logger) *GatewayService {
	return &GatewayService{
		registry: registry,
		log:      log,
	}
}

// ListServices returns all currently registered backends.
func (m *GatewayService) ListServices(
	ctx context.Context,
	req *ynpb.ListServicesRequest,
) (*ynpb.ListServicesResponse, error) {
	backends := m.registry.ListBackends()

	services := make([]*ynpb.RegisteredBackend, 0, len(backends))
	for _, b := range backends {
		registeredBackend := &ynpb.RegisteredBackend{
			Backend: &ynpb.BackendDesc{
				Name:     b.Service(),
				Endpoint: b.Endpoint(),
			},
			LastSeenAt: timestamppb.New(b.LastSeenAt()),
			Kind:       backendKindToProto(b.Kind()),
		}

		services = append(services, registeredBackend)
	}

	return &ynpb.ListServicesResponse{
		Services: services,
	}, nil
}

// Register registers a new module in the Gateway API.
func (m *GatewayService) Register(
	ctx context.Context,
	req *ynpb.RegisterRequest,
) (*ynpb.RegisterResponse, error) {
	backendDesc := req.GetBackend()
	if backendDesc == nil {
		return nil, status.Error(
			codes.InvalidArgument,
			"missing backend in register request",
		)
	}
	if backendDesc.GetName() == "" || backendDesc.GetEndpoint() == "" {
		return nil, status.Error(
			codes.InvalidArgument,
			"name and endpoint are required in register request backend",
		)
	}

	log := m.log.With(
		zap.String("name", backendDesc.GetName()),
		zap.String("endpoint", backendDesc.GetEndpoint()),
	)
	log.Debug("registering backend")

	kind := BackendKindExternal
	if req.GetInProcess() {
		kind = BackendKindInProcess
	}

	// Fast path: skip dialing when the endpoint is unchanged.
	//
	// A registration that races in between this check and RegisterBackend
	// below is resolved there authoritatively (the redundant connection is
	// closed), so this check need not be the linearization point.
	if m.registry.Renew(backendDesc.GetName(), backendDesc.GetEndpoint(), kind) {
		log.Debug("renewed backend registration")
		return &ynpb.RegisterResponse{Status: registrationStatusToProto(RegistrationRenewed)}, nil
	}

	b, err := dialBackend(backendDesc.GetEndpoint(), insecure.NewCredentials())
	if err != nil {
		return nil, err
	}

	regStatus := m.registry.RegisterBackend(backendDesc.GetName(), b, kind)
	switch regStatus {
	case RegistrationRegistered:
		log.Info("registered backend")
	case RegistrationUpdated:
		log.Info("updated backend endpoint")
	default:
		log.Debug("renewed backend registration")
	}

	return &ynpb.RegisterResponse{Status: registrationStatusToProto(regStatus)}, nil
}
