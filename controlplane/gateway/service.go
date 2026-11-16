package gateway

import (
	"context"
	"errors"
	"strings"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
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

// registerUnsafeEndpointPermission gates registration endpoint forms beyond
// a plain TCP host:port, such as a unix socket path.
//
// Dialing happens with the gateway's own privileges, so an endpoint outside
// the plain form is granted only to callers explicitly trusted with it.
const registerUnsafeEndpointPermission = "/controlplane.ynpb.v1.Gateway/Register/UnsafeEndpoint"

// registerAuthorizer decides whether the caller of a registration may use a
// restricted endpoint form.
type registerAuthorizer interface {
	Authorize(ctx context.Context, principal *core.Principal, fullMethod string) error
}

// GatewayService is the gRPC service for the Gateway API.
type GatewayService struct {
	ynpb.UnimplementedGatewayServer
	registry   *BackendRegistry
	authorizer registerAuthorizer
	log        *zap.Logger
}

// GatewayServiceOption configures the GatewayService constructor.
type GatewayServiceOption func(*gatewayServiceOptions)

type gatewayServiceOptions struct {
	Authorizer registerAuthorizer
	Log        *zap.Logger
}

func newGatewayServiceOptions() *gatewayServiceOptions {
	return &gatewayServiceOptions{
		Log: zap.NewNop(),
	}
}

// WithGatewayServiceLog sets the logger for the gateway service.
func WithGatewayServiceLog(log *zap.Logger) GatewayServiceOption {
	return func(o *gatewayServiceOptions) {
		o.Log = log
	}
}

// WithGatewayServiceAuthorizer sets the authorizer gating restricted
// registration endpoint forms.
//
// A service without one keeps every endpoint form open to every caller.
func WithGatewayServiceAuthorizer(authorizer registerAuthorizer) GatewayServiceOption {
	return func(o *gatewayServiceOptions) {
		o.Authorizer = authorizer
	}
}

// NewGatewayService creates a new GatewayService.
func NewGatewayService(registry *BackendRegistry, options ...GatewayServiceOption) *GatewayService {
	opts := newGatewayServiceOptions()
	for _, o := range options {
		o(opts)
	}

	return &GatewayService{
		registry:   registry,
		authorizer: opts.Authorizer,
		log:        opts.Log,
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

	// Every registrant that reaches this RPC runs outside the gateway
	// process: in-process services register directly and never call it.
	kind := BackendKindExternal

	log := m.log.With(
		zap.String("service", backendDesc.GetName()),
		zap.String("endpoint", backendDesc.GetEndpoint()),
		zap.Stringer("kind", kind),
	)
	log.Debug("registering service in registry")

	// A unix endpoint is dialed with the gateway's own privileges, so it
	// needs an explicit grant. A gateway without an authorizer runs open.
	endpoint := backendDesc.GetEndpoint()
	if strings.HasPrefix(endpoint, "/") && m.authorizer != nil {
		if authErr := m.authorizer.Authorize(ctx, core.FromContext(ctx), registerUnsafeEndpointPermission); authErr != nil {
			log.Warn("denied unsafe registration endpoint", zap.Error(authErr))
			return nil, status.Errorf(
				codes.PermissionDenied,
				"endpoint %q requires the %s permission",
				endpoint,
				registerUnsafeEndpointPermission,
			)
		}
	}

	// Fast path: skip dialing when the endpoint is unchanged.
	//
	// A registration that races in between this check and RegisterBackend
	// below is resolved there authoritatively (the redundant connection is
	// closed), so this check need not be the linearization point.
	if m.registry.Renew(backendDesc.GetName(), endpoint) {
		log.Debug("renewed service registration in registry")
		return &ynpb.RegisterResponse{Status: registrationStatusToProto(RegistrationRenewed)}, nil
	}

	b, err := dialBackend(endpoint, insecure.NewCredentials())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	regStatus, err := m.registry.RegisterBackend(backendDesc.GetName(), b, kind)
	if err != nil {
		// The registry never took ownership of the backend.
		_ = b.Close()
		if errors.Is(err, ErrBackendKindMismatch) {
			log.Warn("rejected service registration over an in-process registration")
			return nil, status.Errorf(
				codes.PermissionDenied,
				"service name %q is not available for external registration",
				backendDesc.GetName(),
			)
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	switch regStatus {
	case RegistrationRegistered:
		log.Info("registered service in registry")
	case RegistrationUpdated:
		log.Info("updated service endpoint in registry")
	default:
		log.Debug("renewed service registration in registry")
	}

	return &ynpb.RegisterResponse{Status: registrationStatusToProto(regStatus)}, nil
}
