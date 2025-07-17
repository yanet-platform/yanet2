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

	commonpb "github.com/yanet-platform/yanet2/common/proto"
	"github.com/yanet-platform/yanet2/coordinator/coordinatorpb"
	"github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb"
)

// ModuleService implements the Module gRPC service for the forward module.
type ModuleService struct {
	coordinatorpb.UnimplementedModuleServiceServer

	gatewayEndpoint string
	log             *zap.SugaredLogger
}

// NewModuleService creates a new ModuleService instance.
func NewModuleService(gatewayEndpoint string, log *zap.SugaredLogger) *ModuleService {
	return &ModuleService{
		gatewayEndpoint: gatewayEndpoint,
		log:             log,
	}
}

// SetupConfig applies a configuration to the module for a specific instance.
func (m *ModuleService) SetupConfig(
	ctx context.Context,
	req *coordinatorpb.SetupConfigRequest,
) (*coordinatorpb.SetupConfigResponse, error) {
	instance := req.GetInstance()
	configName := req.GetConfigName()

	config := &Config{}
	if err := yaml.Unmarshal(req.GetConfig(), config); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to unmarshal configuration: %v", err)
	}

	if err := m.setupConfig(ctx, instance, configName, config); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to setup configuration: %v", err)
	}

	return &coordinatorpb.SetupConfigResponse{}, nil
}

// setupConfig setups the provided configuration to the forward module for the
// specified instance.
func (m *ModuleService) setupConfig(
	ctx context.Context,
	instance uint32,
	configName string,
	config *Config,
) error {
	m.log.Infow("setting up forward configuration",
		zap.Uint32("instance", instance),
		zap.String("config_name", configName),
		zap.Any("config", config),
	)

	// Connect to the controlplane ForwardService
	conn, err := grpc.NewClient(
		m.gatewayEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("failed to connect to controlplane service: %w", err)
	}
	defer conn.Close()

	client := forwardpb.NewForwardServiceClient(conn)

	// Create a target configuration for the ForwardService
	target := &commonpb.TargetModule{
		ConfigName:        configName,
		DataplaneInstance: instance,
	}

	for _, forward := range config.L2Forwards {
		req := &forwardpb.L2ForwardEnableRequest{
			Target:   target,
			SrcDevId: uint32(forward.SourceDeviceID),
			DstDevId: uint32(forward.DestinationDeviceID),
		}

		if _, err = client.EnableL2Forward(ctx, req); err != nil {
			return fmt.Errorf(
				"failed to enable L2 forward from %d to %d: %w",
				forward.SourceDeviceID,
				forward.DestinationDeviceID,
				err,
			)
		}
	}

	for _, forward := range config.L3Forwards {
		sourceDeviceID := forward.SourceDeviceID

		for _, rule := range forward.Rules {
			req := &forwardpb.AddL3ForwardRequest{
				Target:   target,
				SrcDevId: uint32(sourceDeviceID),
				Forward: &forwardpb.L3ForwardEntry{
					Network:  rule.Network.String(),
					DstDevId: uint32(rule.DestinationDeviceID),
				},
			}

			if _, err = client.AddL3Forward(ctx, req); err != nil {
				return fmt.Errorf(
					"failed to add forward from %d to %d for network %s: %w",
					sourceDeviceID,
					rule.DestinationDeviceID,
					rule.Network.String(),
					err,
				)
			}
		}
	}

	m.log.Infow("finished setting up forward configuration",
		zap.Uint32("instance", instance),
		zap.String("config_name", configName),
	)

	return nil
}
