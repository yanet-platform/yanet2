package coordinator

import (
	"context"
	"fmt"
	"net"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/yanet-platform/yanet2/coordinator/coordinatorpb"
)

// Module represents a route module in the coordinator.
type Module struct {
	configs             map[uint32]*Config
	endpoint            string
	coordinatorEndpoint string
	log                 *zap.SugaredLogger
	moduleService       *ModuleService
}

// NewModule creates a new instance of the route module.
func NewModule(
	endpoint string,
	gatewayEndpoint string,
	coordinatorEndpoint string,
	log *zap.SugaredLogger,
) *Module {
	log = log.Named("route").With(zap.String("module", "route"))

	moduleService := NewModuleService(gatewayEndpoint, log)

	return &Module{
		configs:             map[uint32]*Config{},
		endpoint:            endpoint,
		coordinatorEndpoint: coordinatorEndpoint,
		log:                 log,
		moduleService:       moduleService,
	}
}

// Run starts the gRPC server for the module and registers it with the
// coordinator.
func (m *Module) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", m.endpoint)
	if err != nil {
		return fmt.Errorf("failed to create listener for route module: %w", err)
	}

	endpoint := listener.Addr().String()
	m.log.Infow("starting route module server", zap.String("endpoint", endpoint))

	// Create a gRPC server for the module.
	server := grpc.NewServer()

	// Register the Module service implementation.
	coordinatorpb.RegisterModuleServiceServer(server, m.moduleService)

	// Start the gRPC server in a goroutine.
	wg, ctx := errgroup.WithContext(ctx)
	wg.Go(func() error {
		// Ensure the server stops gracefully when the context is done.
		go func() {
			<-ctx.Done()
			server.GracefulStop()
		}()
		return server.Serve(listener)
	})

	// Register the module with the coordinator.
	m.log.Infow("registering route module with coordinator",
		zap.String("module_endpoint", endpoint),
		zap.String("coordinator_endpoint", m.coordinatorEndpoint))

	// Connect to the coordinator.
	conn, err := grpc.NewClient(
		m.coordinatorEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		// Stop the server if connection to coordinator fails.
		server.Stop()
		return fmt.Errorf("failed to connect to coordinator: %w", err)
	}
	defer conn.Close()

	// Create a client for the coordinator.
	client := coordinatorpb.NewRegistryServiceClient(conn)

	// Prepare the registration request.
	req := &coordinatorpb.RegisterModuleRequest{
		Name:     "route",
		Endpoint: endpoint,
	}

	resp, err := client.RegisterModule(ctx, req)
	if err != nil {
		// Stop the server if registration failed.
		server.Stop()
		return fmt.Errorf("failed to register with coordinator: %w", err)
	}

	if !resp.Success {
		// Stop the server if registration was unsuccessful.
		server.Stop()
		return fmt.Errorf("registration failed: %s", resp.Message)
	}

	return wg.Wait()
}
