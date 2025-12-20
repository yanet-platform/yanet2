package gateway

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/siderolabs/grpc-proxy/proxy"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/metadata"

	"github.com/yanet-platform/yanet2/controlplane/ynpb"
)

// GatewayService is the gRPC service for the Gateway API.
type GatewayService struct {
	ynpb.UnimplementedGatewayServer
	registry *BackendRegistry
	log      *zap.SugaredLogger
}

// NewGatewayService creates a new GatewayService.
func NewGatewayService(registry *BackendRegistry, log *zap.SugaredLogger) *GatewayService {
	return &GatewayService{
		registry: registry,
		log:      log,
	}
}

// Register registers a new module in the Gateway API.
func (m *GatewayService) Register(
	ctx context.Context,
	request *ynpb.RegisterRequest,
) (*ynpb.RegisterResponse, error) {
	m.log.Infof("registering backend %q on %q", request.GetName(), request.GetEndpoint())

	conn, err := grpc.NewClient(
		"passthrough:target",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			dialer := net.Dialer{}
			endpoint := request.GetEndpoint()
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
	m.registry.RegisterBackend(request.GetName(), backend)

	return &ynpb.RegisterResponse{}, nil
}
