package decap

import (
	"context"
	"fmt"
	"net"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/yanet-platform/yanet2/controlplane/internal/bitset"
	"github.com/yanet-platform/yanet2/controlplane/internal/ffi"
	"github.com/yanet-platform/yanet2/controlplane/modules/decap/decappb"
	"github.com/yanet-platform/yanet2/controlplane/ynpb"
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
		return nil, fmt.Errorf("failed to attach to shared memory %q: %w", cfg.MemoryPath, err)
	}

	numaIndices := make([]uint32, 0)
	bitset.NewBitsTraverser(uint64(shm.NumaMap())).Traverse(func(numaIdx int) {
		numaIndices = append(numaIndices, uint32(numaIdx))
	})

	agents := make([]*ffi.Agent, 0)
	for _, numaIdx := range numaIndices {
		log.Debugw("mapping shared memory",
			zap.Uint32("numa", numaIdx),
			zap.Stringer("size", cfg.MemoryRequirements),
		)

		agent, err := shm.AgentAttach("decap", numaIdx, uint(cfg.MemoryRequirements))
		if err != nil {
			return nil, fmt.Errorf("failed to connect to shared memory on NUMA %d: %w", numaIdx, err)
		}

		agents = append(agents, agent)
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
	listener, err := net.Listen("tcp", m.cfg.Endpoint)
	if err != nil {
		return fmt.Errorf("failed to initialize gRPC listener: %w", err)
	}

	listenerAddr := listener.Addr()
	m.log.Infow("exposing gRPC API", zap.Stringer("addr", listenerAddr))

	wg, ctx := errgroup.WithContext(ctx)
	wg.Go(func() error {
		return m.server.Serve(listener)
	})

	if err := m.registerServices(ctx, listenerAddr); err != nil {
		return fmt.Errorf("failed to register services: %w", err)
	}

	<-ctx.Done()

	m.log.Infow("stopping gRPC API", zap.Stringer("addr", listenerAddr))
	defer m.log.Infow("stopped gRPC API", zap.Stringer("addr", listenerAddr))

	m.server.GracefulStop()

	return wg.Wait()
}

func (m *DecapModule) registerServices(ctx context.Context, listenerAddr net.Addr) error {
	gatewayConn, err := grpc.NewClient(
		m.cfg.GatewayEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("failed to initialize gateway gRPC client: %w", err)
	}

	gateway := ynpb.NewGatewayClient(gatewayConn)

	servicesNames := []string{
		"decappb.DecapService",
	}

	wg, ctx := errgroup.WithContext(ctx)
	for _, serviceName := range servicesNames {
		serviceName := serviceName
		req := &ynpb.RegisterRequest{
			Name:     serviceName,
			Endpoint: listenerAddr.String(),
		}

		wg.Go(func() error {
			for {
				if _, err = gateway.Register(ctx, req); err == nil {
					m.log.Infof("successfully registered %q in the Gateway API", serviceName)
					return nil
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}

				m.log.Warnf("failed to register %q in the Gateway API: %v", serviceName, err)
				// TODO: exponential backoff should fit better here.
				time.Sleep(1 * time.Second)
			}
		})
	}

	return wg.Wait()
}
