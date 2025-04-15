package nat64

import (
	"context"
	"fmt"
	"net"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/yncp/gateway"
	"github.com/yanet-platform/yanet2/modules/nat64/controlplane/nat64pb"
)

// NAT64Module is a control-plane component responsible for NAT64 translation
type NAT64Module struct {
	cfg          *Config
	server       *grpc.Server
	shm          *ffi.SharedMemory
	agents       []*ffi.Agent
	nat64Service *NAT64Service
	log          *zap.SugaredLogger
}

// NewNAT64Module creates a new NAT64 module instance
func NewNAT64Module(cfg *Config, log *zap.SugaredLogger) (*NAT64Module, error) {
	log = log.With(zap.String("module", "nat64pb.NAT64Service"))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath)
	if err != nil {
		return nil, err
	}

	numaIndices := shm.NumaIndices()
	log.Debugw("mapping shared memory",
		zap.Uint32s("numa", numaIndices),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agents, err := shm.AgentsAttach("nat64", numaIndices, uint(cfg.MemoryRequirements))
	if err != nil {
		return nil, err
	}

	server := grpc.NewServer()

	nat64Service := NewNAT64Service(agents, log)
	nat64pb.RegisterNAT64ServiceServer(server, nat64Service)

	return &NAT64Module{
		cfg:          cfg,
		server:       server,
		shm:          shm,
		agents:       agents,
		nat64Service: nat64Service,
		log:          log,
	}, nil
}

// Close closes the module and releases all resources
func (m *NAT64Module) Close() error {
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

// Run runs the module until the specified context is canceled
func (m *NAT64Module) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", m.cfg.Endpoint)
	if err != nil {
		return fmt.Errorf("failed to initialize gRPC listener: %w", err)
	}

	wg, ctx := errgroup.WithContext(ctx)
	wg.Go(func() error {
		m.log.Infow("exposing gRPC API", zap.Stringer("addr", listener.Addr()))
		return m.server.Serve(listener)
	})

	serviceNames := []string{"nat64pb.NAT64Service"}

	if err = gateway.RegisterModule(
		ctx,
		m.cfg.GatewayEndpoint,
		listener,
		serviceNames,
		m.log,
	); err != nil {
		return fmt.Errorf("failed to register services: %w", err)
	}

	<-ctx.Done()

	m.log.Infow("stopping gRPC API", zap.Stringer("addr", listener.Addr()))
	defer m.log.Infow("stopped gRPC API", zap.Stringer("addr", listener.Addr()))

	m.server.GracefulStop()

	return wg.Wait()
}
