package gateway

import (
	"context"
	"fmt"
	"net"
	"os"
	"path"
	"strings"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/internal/xgrpc"
)

// ServiceRunner runs an out-of-process Service on its own listener and
// registers it with the gateway.
type ServiceRunner struct {
	module          Service
	gatewayEndpoint string
	gatewayTLS      *TLSConfig
	server          *grpc.Server
	ready           chan struct{}
	log             *zap.Logger
}

// NewServiceRunner creates a new ServiceRunner for the given service.
func NewServiceRunner(
	module Service,
	gatewayEndpoint string,
	gatewayTLS *TLSConfig,
	log *zap.Logger,
) *ServiceRunner {
	log = log.Named(module.Name()).With(zap.String("module", module.Name()))

	return &ServiceRunner{
		module:          module,
		gatewayEndpoint: gatewayEndpoint,
		gatewayTLS:      gatewayTLS,
		server: grpc.NewServer(
			grpc.ChainUnaryInterceptor(xgrpc.AccessLogInterceptor(log)),
			grpc.MaxRecvMsgSize(1024*1024*256), grpc.MaxSendMsgSize(1024*1024*256),
		),
		ready: make(chan struct{}),
		log:   log,
	}
}

// Ready returns a channel that is closed when the runner has finished
// the initial service registration phase against the gateway. The
// channel is closed exactly once; consumers can use it to detect that
// the module is reachable through the gateway.
func (m *ServiceRunner) Ready() <-chan struct{} {
	return m.ready
}

// Close closes the underlying service if it implements ClosableService.
func (m *ServiceRunner) Close() error {
	if c, ok := m.module.(ClosableService); ok {
		return c.Close()
	}
	return nil
}

// Run runs the service until the context is canceled.
func (m *ServiceRunner) Run(ctx context.Context) error {
	listener, err := m.listen()
	if err != nil {
		return fmt.Errorf("failed to initialize gRPC listener: %w", err)
	}

	m.module.RegisterService(m.server)

	wg, ctx := errgroup.WithContext(ctx)
	if bg, ok := m.module.(BackgroundService); ok {
		m.log.Info("running background jobs")

		wg.Go(func() error {
			return bg.Run(ctx)
		})
	}
	wg.Go(func() error {
		m.log.Info("exposing gRPC API", zap.Stringer("addr", listener.Addr()))
		return m.server.Serve(listener)
	})

	if err = m.register(ctx, listener.Addr()); err != nil {
		return fmt.Errorf("failed to register services: %w", err)
	}
	close(m.ready)

	<-ctx.Done()

	m.log.Info("stopping gRPC API", zap.Stringer("addr", listener.Addr()))
	defer m.log.Info("stopped gRPC API", zap.Stringer("addr", listener.Addr()))

	m.server.GracefulStop()

	return wg.Wait()
}

func (m *ServiceRunner) listen() (net.Listener, error) {
	endpoint := m.module.Endpoint()

	if strings.HasPrefix(endpoint, "/") {
		dir := path.Dir(endpoint)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
		if err := os.Remove(endpoint); err != nil {
			if !os.IsNotExist(err) {
				return nil, err
			}
		}

		return net.Listen("unix", endpoint)
	}

	return net.Listen("tcp", endpoint)
}

func (m *ServiceRunner) register(ctx context.Context, addr net.Addr) error {
	registrar, err := NewGatewayRegistrar(
		m.gatewayEndpoint,
		m.gatewayTLS,
		WithRegistrarLog(m.log),
	)
	if err != nil {
		return fmt.Errorf("failed to initialize gateway registrar: %w", err)
	}
	defer registrar.Close()

	if err = registrar.RegisterServices(ctx, m.module.ServicesNames(), addr.String()); err != nil {
		return fmt.Errorf("failed to register services: %w", err)
	}
	return nil
}
