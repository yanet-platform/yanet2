package coordinator

import (
	"context"
	"fmt"
	"maps"
	"net"
	"sync"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/yanet-platform/yanet2/controlplane/ynpb"
	"github.com/yanet-platform/yanet2/coordinator/coordinatorpb"
	"github.com/yanet-platform/yanet2/coordinator/internal/registry"
	"github.com/yanet-platform/yanet2/coordinator/internal/stage"
	forwardcoord "github.com/yanet-platform/yanet2/modules/forward/coordinator"
)

type options struct {
	Log *zap.SugaredLogger
}

func newOptions() *options {
	return &options{
		Log: zap.NewNop().Sugar(),
	}
}

// CoordinatorOption is a function that configures the YANET coordinator.
type CoordinatorOption func(*options)

// WithLog sets the logger for the YANET coordinator.
func WithLog(log *zap.SugaredLogger) CoordinatorOption {
	return func(o *options) {
		o.Log = log
	}
}

// Coordinator is the main orchestration component.
type Coordinator struct {
	cfg            *Config
	mu             sync.Mutex
	registry       *registry.Registry
	registryRx     <-chan registry.RegisterEvent
	server         *grpc.Server
	builtInModules []builtInModule
	log            *zap.SugaredLogger
}

// NewCoordinator creates a new coordinator using the provided configuration.
func NewCoordinator(cfg *Config, options ...CoordinatorOption) (*Coordinator, error) {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log
	log.Infow("initializing YANET coordinator", zap.Any("config", cfg))

	txrx := make(chan registry.RegisterEvent)
	r := registry.New(txrx)
	server := grpc.NewServer()

	coordinatorpb.RegisterRegistryServiceServer(server, registry.NewRegistryService(r, log))

	const builtInModuleEndpoint = "[::1]:0"

	builtInModules := []builtInModule{
		forwardcoord.NewForwardModule(
			builtInModuleEndpoint,
			cfg.Gateway.Endpoint,
			cfg.Coordinator.Endpoint,
			log,
		),
	}

	return &Coordinator{
		cfg:            cfg,
		registry:       r,
		registryRx:     txrx,
		server:         server,
		builtInModules: builtInModules,
		log:            log,
	}, nil
}

func (m *Coordinator) Run(ctx context.Context) error {
	m.log.Info("running coordinator")
	defer m.log.Info("stopped coordinator")

	// Start the gRPC server to expose the registry.
	listener, err := net.Listen("tcp", m.cfg.Coordinator.Endpoint)
	if err != nil {
		return fmt.Errorf("failed to initialize gRPC listener: %w", err)
	}

	m.log.Infow("exposing registry API", zap.Stringer("addr", listener.Addr()))

	wg, ctx := errgroup.WithContext(ctx)

	// Serve the gRPC API.
	wg.Go(func() error {
		return m.server.Serve(listener)
	})
	wg.Go(func() error {
		return m.runBuiltInModules(ctx)
	})

	// Wait for ALL required modules to be registered.
	if err := m.waitRegistrationComplete(ctx); err != nil {
		return fmt.Errorf("failed to wait for all modules: %w", err)
	}

	if err := m.setupStages(ctx); err != nil {
		return fmt.Errorf("failed to setup stages: %w", err)
	}

	// TODO: expose stage API.

	<-ctx.Done()

	m.log.Infow("stopping registry API", zap.Stringer("addr", listener.Addr()))
	defer m.log.Infow("stopped registry API", zap.Stringer("addr", listener.Addr()))

	m.server.GracefulStop()

	return wg.Wait()
}

// runBuiltInModules runs built-in modules.
func (m *Coordinator) runBuiltInModules(ctx context.Context) error {
	m.log.Info("running built-in modules")
	defer m.log.Info("stopped built-in modules")

	wg, ctx := errgroup.WithContext(ctx)
	for _, module := range m.builtInModules {
		wg.Go(func() error {
			return module.Run(ctx)
		})
	}

	return wg.Wait()
}

// waitRegistrationComplete waits until all required modules are registered.
func (m *Coordinator) waitRegistrationComplete(ctx context.Context) error {
	// Collect all required module names from the configuration.
	requiredModules := m.cfg.RequiredModules()
	requiredModulesLeft := maps.Clone(requiredModules)

	m.log.Infow("waiting for modules to be registered",
		zap.Any("modules", requiredModules),
	)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-m.registryRx:
			m.log.Infow("received registry event", zap.String("module", ev.Name))

			delete(requiredModulesLeft, ev.Name)
			if len(requiredModulesLeft) == 0 {
				m.log.Infow("successfully registered required modules",
					zap.Any("modules", requiredModules),
				)
				return nil
			}
		}
	}
}

// setupStages applies the stages described in the config.
func (m *Coordinator) setupStages(ctx context.Context) error {
	m.log.Info("setting up stages")
	defer m.log.Info("finished setting up stages")

	conn, err := grpc.NewClient(
		m.cfg.Gateway.Endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("failed to create gRPC client: %w", err)
	}
	defer conn.Close()

	pipeline := ynpb.NewPipelineServiceClient(conn)

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, cfg := range m.cfg.Stages {
		m.log.Infow("setting up stage", zap.String("stage", cfg.Name))

		s := stage.NewStage(cfg, m.registry, pipeline, stage.WithLog(m.log))

		if err := s.Setup(ctx); err != nil {
			return fmt.Errorf("failed to set up stage %s: %w", cfg.Name, err)
		}
	}

	return nil
}

// Close stops the coordinator.
func (m *Coordinator) Close() error {
	if m.server != nil {
		m.server.Stop()
	}

	return nil
}

type builtInModule interface {
	Run(ctx context.Context) error
}
