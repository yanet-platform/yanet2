package builtin

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

type BuiltInModule interface {
	Name() string
	RegisterService(server *grpc.Server)
	StopService()
}

type BuiltInModuleRunner struct {
	module              BuiltInModule
	endpoint            string
	coordinatorEndpoint string
	log                 *zap.SugaredLogger
}

func NewBuiltInModuleRunner(
	module BuiltInModule,
	endpoint string,
	coordinatorEndpoint string,
	log *zap.SugaredLogger,
) *BuiltInModuleRunner {
	log = log.Named(module.Name()).With(zap.String("module", module.Name()))

	return &BuiltInModuleRunner{
		module:              module,
		endpoint:            endpoint,
		coordinatorEndpoint: coordinatorEndpoint,
		log:                 log,
	}
}

// Run starts the gRPC server for the module and registers it with the
// coordinator.
func (m *BuiltInModuleRunner) Run(ctx context.Context) error {
	defer m.log.Infow("stopped module server")

	listener, err := net.Listen("tcp", m.endpoint)
	if err != nil {
		return fmt.Errorf("failed to create listener for module: %w", err)
	}

	endpoint := listener.Addr().String()
	m.log.Infow("starting module server", zap.String("endpoint", endpoint))

	// Create a gRPC server for the module.
	server := grpc.NewServer()

	// Register the Module service implementation.
	m.module.RegisterService(server)

	// Start the gRPC server in a goroutine.
	wg, ctx := errgroup.WithContext(ctx)
	wg.Go(func() error {
		// Ensure the server stops gracefully when the context is done.
		go func() {
			<-ctx.Done()
			server.GracefulStop()
			m.module.StopService()
		}()
		return server.Serve(listener)
	})

	// Register the module with the coordinator.
	m.log.Infow("registering module with coordinator",
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
		Name:     m.module.Name(),
		Endpoint: endpoint,
	}

	if _, err := client.RegisterModule(ctx, req); err != nil {
		// Stop the server if registration failed.
		server.Stop()
		return fmt.Errorf("failed to register with coordinator: %w", err)
	}

	m.log.Infow("module registered with coordinator", zap.String("module_endpoint", endpoint))

	return wg.Wait()
}
