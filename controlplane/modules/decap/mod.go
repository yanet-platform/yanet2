package decap

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/internal/ffi"
	"github.com/yanet-platform/yanet2/controlplane/internal/gateway"
	"github.com/yanet-platform/yanet2/controlplane/modules/decap/decappb"
)

// DecapModule is a control-plane component of a module that is responsible for
// decapsulating various kinds of tunnels.
type DecapModule struct {
	cfg          *Config
	server       *grpc.Server
	shm          *ffi.SharedMemory
	agents       []*ffi.Agent
	decapService *DecapService
	log          *zap.SugaredLogger
}

func NewDecapModule(cfg *Config, log *zap.SugaredLogger) (*DecapModule, error) {
	log = log.With(zap.String("module", "decappb.DecapService"))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath)
	if err != nil {
		return nil, err
	}

	numaIndices := shm.NumaIndices()
	log.Debugw("mapping shared memory",
		zap.Uint32s("numa", numaIndices),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agents, err := shm.AgentsAttach("decap", numaIndices, uint(cfg.MemoryRequirements))
	if err != nil {
		return nil, err
	}

	server := grpc.NewServer()

	decapService := NewDecapService(agents, log)
	decappb.RegisterDecapServiceServer(server, decapService)

	return &DecapModule{
		cfg:          cfg,
		server:       server,
		shm:          shm,
		agents:       agents,
		decapService: decapService,
		log:          log,
	}, nil
}

// Close closes the module.
func (m *DecapModule) Close() error {
	for numaIdx, agent := range m.agents {
		if err := agent.Close(); err != nil {
			m.log.Warnw("failed to close shared memory agent", zap.Int("numa", numaIdx), zap.Error(err))
		}
	}

	if err := m.shm.Detach(); err != nil {
		m.log.Warnw("failed to detach from shared memory mapping", zap.Error(err))
	}

	return nil
}

// Run runs the module until the specified context is canceled.
func (m *DecapModule) Run(ctx context.Context) error {

	serviceNames := []string{"decappb.DecapService"}

	listener, err := gateway.RegisterModule(
		ctx,
		m.cfg.GatewayEndpoint,
		m.cfg.Endpoint,
		serviceNames,
		m.log.With("name", "registry"),
	)
	if err != nil {
		return fmt.Errorf("failed to register services: %w", err)
	}

	m.log.Infow("exposing gRPC API", zap.Stringer("addr", listener.Addr()))

	wg, ctx := errgroup.WithContext(ctx)
	wg.Go(func() error {
		return m.server.Serve(listener)
	})

	<-ctx.Done()

	m.log.Infow("stopping gRPC API", zap.Stringer("addr", listener.Addr()))
	defer m.log.Infow("stopped gRPC API", zap.Stringer("addr", listener.Addr()))

	m.server.GracefulStop()

	return wg.Wait()
}
