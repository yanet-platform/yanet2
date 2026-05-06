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

	"github.com/yanet-platform/yanet2/controlplane/gateway"
	"github.com/yanet-platform/yanet2/controlplane/internal/xgrpc"
)

type BuiltInModule interface {
	Name() string
	Endpoint() string
	ServicesNames() []string
	RegisterService(server *grpc.Server)
	Close() error
}

type BackgroundBuiltInModule interface {
	Run(ctx context.Context) error
}

type BuiltInModuleRunner struct {
	module          BuiltInModule
	gatewayEndpoint string
	gatewayTLS      *gateway.TLSConfig
	server          *grpc.Server
	ready           chan struct{}
	log             *zap.Logger
}

func NewBuiltInModuleRunner(
	module BuiltInModule,
	gatewayEndpoint string,
	gatewayTLS *gateway.TLSConfig,
	log *zap.Logger,
) *BuiltInModuleRunner {
	log = log.Named(module.Name()).With(zap.String("module", module.Name()))

	return &BuiltInModuleRunner{
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
func (m *BuiltInModuleRunner) Ready() <-chan struct{} {
	return m.ready
}

func (m *BuiltInModuleRunner) Close() error {
	return m.module.Close()
}

func (m *BuiltInModuleRunner) Run(ctx context.Context) error {
	listener, err := m.listen()
	if err != nil {
		return fmt.Errorf("failed to initialize gRPC listener: %w", err)
	}

	m.module.RegisterService(m.server)

	wg, ctx := errgroup.WithContext(ctx)
	if mod, ok := m.module.(BackgroundBuiltInModule); ok {
		m.log.Info("running background jobs")

		wg.Go(func() error {
			return mod.Run(ctx)
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

func (m *BuiltInModuleRunner) listen() (net.Listener, error) {
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

func (m *BuiltInModuleRunner) register(ctx context.Context, addr net.Addr) error {
	registrar, err := gateway.NewGatewayRegistrar(
		m.gatewayEndpoint,
		m.gatewayTLS,
		gateway.WithLog(m.log),
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
