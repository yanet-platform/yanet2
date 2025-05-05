package coordinator

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/coordinator/coordinatorpb"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb"
)

// ModuleService implements the Module gRPC service for the route module.
type ModuleService struct {
	coordinatorpb.UnimplementedModuleServiceServer

	gatewayEndpoint string
	log             *zap.SugaredLogger
}

func NewModuleService(
	gatewayEndpoint string,
	log *zap.SugaredLogger,
) *ModuleService {
	return &ModuleService{
		gatewayEndpoint: gatewayEndpoint,
		log:             log,
	}
}

func (m *ModuleService) SetupConfig(
	ctx context.Context,
	req *coordinatorpb.SetupConfigRequest,
) (*coordinatorpb.SetupConfigResponse, error) {
	numaNode := req.GetNumaNode()
	configName := req.GetConfigName()

	m.log.Infow("setting up configuration",
		zap.Uint32("numa", numaNode),
	)

	cfg := &Config{}
	if err := yaml.Unmarshal(req.GetConfig(), cfg); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to unmarshal configuration: %v", err)
	}
	if err := m.setupConfig(ctx, numaNode, configName, cfg); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to setup configuration: %v", err)
	}

	return &coordinatorpb.SetupConfigResponse{
		Success: true,
	}, nil
}

func (m *ModuleService) setupConfig(
	ctx context.Context,
	numaNode uint32,
	configName string,
	config *Config,
) error {
	conn, err := grpc.NewClient(
		m.gatewayEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("failed to connect to the gateway: %w", err)
	}
	defer conn.Close()

	client := routepb.NewRouteServiceClient(conn)

	for _, route := range config.Routes {
		request := &routepb.InsertRouteRequest{
			Numa:        []uint32{numaNode},
			ModuleName:  configName,
			Prefix:      route.Prefix.String(),
			NexthopAddr: route.Nexthop.String(),
		}

		if _, err := client.InsertRoute(ctx, request); err != nil {
			return fmt.Errorf("failed to insert static route: %w", err)
		}
	}

	return nil
}
