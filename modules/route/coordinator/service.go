package coordinator

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/proto"
	"github.com/yanet-platform/yanet2/coordinator/coordinatorpb"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb"
	"github.com/yanet-platform/yanet2/modules/route/internal/discovery/bird"
	"github.com/yanet-platform/yanet2/modules/route/internal/rib"
)

type instanceKey struct {
	name string
	numa uint32
}

type importHodler struct {
	export *bird.Export
	cancel context.CancelFunc
	conn   *grpc.ClientConn
}

// ModuleService implements the Module gRPC service for the route module.
type ModuleService struct {
	coordinatorpb.UnimplementedModuleServiceServer

	imports         map[instanceKey]*importHodler
	gatewayEndpoint string
	log             *zap.SugaredLogger
}

func NewModuleService(
	gatewayEndpoint string,
	log *zap.SugaredLogger,
) *ModuleService {
	return &ModuleService{
		imports:         map[instanceKey]*importHodler{},
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
		zap.String("name", configName),
		zap.Uint32("numa", numaNode),
	)

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(req.GetConfig(), cfg); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to unmarshal configuration: %v", err)
	}

	if err := m.setupConfig(ctx, numaNode, configName, cfg); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to setup configuration: %v", err)
	}

	return &coordinatorpb.SetupConfigResponse{}, nil
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
	client := routepb.NewRouteServiceClient(conn)
	target := &commonpb.TargetModule{
		ConfigName: configName,
		Numa:       numaNode,
	}
	flushRequest := &routepb.FlushRoutesRequest{Target: target}

	// Insert and flush static routes first.
	for _, route := range config.Routes {
		request := &routepb.InsertRouteRequest{
			Target:      target,
			Prefix:      route.Prefix.String(),
			NexthopAddr: route.Nexthop.String(),
		}

		if _, err := client.InsertRoute(ctx, request); err != nil {
			return fmt.Errorf("failed to insert static route: %w", err)
		}
	}

	if _, err := client.FlushRoutes(ctx, flushRequest); err != nil {
		return fmt.Errorf("failed to flush static routes for %s: %w", configName, err)
	}

	// And then add dynamic routes, if any.
	if len(config.BirdImport.Sockets) > 0 {
		streamCtx, cancel := context.WithCancel(context.Background())

		stream, err := client.FeedRIB(streamCtx)
		if err != nil {
			cancel()
			return fmt.Errorf("failed to setup route update push stream: %w", err)
		}

		log := m.log.With("config", configName, "numa", numaNode)
		onUpdate := func(routes []rib.Route) error {
			log.Debugf("received update batch with %d routes", len(routes))
			for idx := range routes {
				select {
				case <-streamCtx.Done():
					log.Warnf("terminate update stream due to: %w", streamCtx.Err())
					_, err = stream.CloseAndRecv()
					return errors.Join(streamCtx.Err(), err)
				default:
				}

				err := stream.Send(&routepb.Update{
					Target:   target,
					IsDelete: routes[idx].ToRemove,
					Route:    routepb.FromRIBRoute(&routes[idx], false /* we don't know */),
				})
				if err != nil {
					return fmt.Errorf("failed to send update: %w", err)
				}
			}
			return nil
		}
		onFlush := func() error {
			_, err := client.FlushRoutes(streamCtx, flushRequest)
			return err
		}

		export := bird.NewExportReader(config.BirdImport, onUpdate, onFlush, m.log)
		holder, ok := m.imports[instanceKey{name: configName, numa: numaNode}]
		if ok {
			holder.cancel()
			holder.conn.Close()
		}

		m.imports[instanceKey{name: configName, numa: numaNode}] = &importHodler{
			export: export,
			cancel: cancel,
			conn:   conn,
		}

		go func() {
			if err := export.Run(streamCtx); err != nil {
				log.Errorf("failed to run bird export reader: %v", err)
				cancel()
				conn.Close()
				// FIXME: Determine the appropriate action for this scenario.
				panic(err)
			}
		}()

	} else {
		// We do not need this connection if there is no background stream for import
		conn.Close()
	}

	return nil
}
