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

	"github.com/yanet-platform/yanet2/controlplane/yncp/gateway"
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
	server          *grpc.Server
	log             *zap.SugaredLogger
}

func NewBuiltInModuleRunner(
	module BuiltInModule,
	gatewayEndpoint string,
	log *zap.SugaredLogger,
) *BuiltInModuleRunner {
	return &BuiltInModuleRunner{
		module:          module,
		gatewayEndpoint: gatewayEndpoint,
		server:          grpc.NewServer(),
		log:             log.Named(module.Name()).With(zap.String("module", module.Name())),
	}
}

func (m *BuiltInModuleRunner) Close() error {
	endpoint := m.module.Endpoint()
	if strings.HasPrefix(endpoint, "/") {
		os.Remove(endpoint)
	}

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
		m.log.Infow("running background jobs")

		wg.Go(func() error {
			return mod.Run(ctx)
		})
	}
	wg.Go(func() error {
		m.log.Infow("exposing gRPC API", zap.Stringer("addr", listener.Addr()))
		return m.server.Serve(listener)
	})

	if err = gateway.RegisterModule(
		ctx,
		m.gatewayEndpoint,
		listener,
		m.module.ServicesNames(),
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

func (m *BuiltInModuleRunner) listen() (net.Listener, error) {
	endpoint := m.module.Endpoint()

	if strings.HasPrefix(endpoint, "/") {
		dir := path.Dir(endpoint)
		if err := os.MkdirAll(dir, 0644); err != nil {
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
