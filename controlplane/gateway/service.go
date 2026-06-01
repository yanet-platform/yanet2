package gateway

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/siderolabs/grpc-proxy/proxy"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/yanet-platform/yanet2/controlplane/ynpb"
)

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

// backendConn adapts a backend's *grpc.ClientConn to the registry Conn
// interface, reporting the real backend endpoint rather than the dial target.
//
// Close is idempotent: a single connection may back several registry entries
// (e.g. the loopback connection shared across built-in service names), so the
// registry's shutdown sweep can call Close more than once.
type backendConn struct {
	endpoint  string
	conn      *grpc.ClientConn
	closeOnce sync.Once
	closeErr  error
}

func newBackendConn(endpoint string, conn *grpc.ClientConn) *backendConn {
	return &backendConn{endpoint: endpoint, conn: conn}
}

func (m *backendConn) Target() string { return m.endpoint }

func (m *backendConn) Close() error {
	m.closeOnce.Do(func() { m.closeErr = m.conn.Close() })
	return m.closeErr
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
	for _, backend := range backends {
		registeredBackend := &ynpb.RegisteredBackend{
			Backend: &ynpb.BackendDesc{
				Name:     backend.Service(),
				Endpoint: backend.Endpoint(),
			},
			LastSeenAt: timestamppb.New(backend.LastSeenAt()),
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
	request *ynpb.RegisterRequest,
) (*ynpb.RegisterResponse, error) {
	backendDesc := request.GetBackend()
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

	conn, err := grpc.NewClient(
		"passthrough:target",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			dialer := net.Dialer{}
			endpoint := backendDesc.GetEndpoint()
			if strings.HasPrefix(endpoint, "/") {
				return dialer.DialContext(ctx, "unix", endpoint)
			}

			return dialer.DialContext(ctx, "tcp", endpoint)
		}),
		grpc.WithDefaultCallOptions(
			grpc.ForceCodecV2(proxy.Codec()),
			grpc.UseCompressor(gzip.Name),
			grpc.MaxCallRecvMsgSize(1024*1024*256),
			grpc.MaxCallSendMsgSize(1024*1024*256),
		),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC client to backend: %w", err)
	}

	backend := &proxy.SingleBackend{
		GetConn: func(ctx context.Context) (context.Context, *grpc.ClientConn, error) {
			md, _ := metadata.FromIncomingContext(ctx)
			outCtx := metadata.NewOutgoingContext(ctx, md.Copy())

			return outCtx, conn, nil
		},
	}
	status := m.registry.RegisterBackend(
		backendDesc.GetName(),
		backend,
		newBackendConn(backendDesc.GetEndpoint(), conn),
	)

	switch status {
	case RegistrationRegistered:
		log.Info("registered backend")
	case RegistrationUpdated:
		log.Info("updated backend endpoint")
	default:
		log.Debug("renewed backend registration")
	}

	return &ynpb.RegisterResponse{Status: registrationStatusToProto(status)}, nil
}
