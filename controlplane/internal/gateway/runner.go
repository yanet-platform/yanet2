package gateway

import (
	"context"
	"fmt"
	"net"
	"os"
	"path"
	"strings"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/yanet-platform/yanet2/controlplane/ynpb"
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

	if err = m.register(ctx, listener.Addr()); err != nil {
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

func (m *BuiltInModuleRunner) register(ctx context.Context, addr net.Addr) error {
	gatewayConn, err := grpc.NewClient(
		m.gatewayEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("failed to initialize gateway gRPC client: %w", err)
	}

	client := ynpb.NewGatewayClient(gatewayConn)

	wg, ctx := errgroup.WithContext(ctx)
	for _, serviceName := range m.module.ServicesNames() {
		serviceName := serviceName
		req := &ynpb.RegisterRequest{
			Name:     serviceName,
			Endpoint: addr.String(),
		}

		wg.Go(func() error {
			for {
				if _, err := client.Register(ctx, req); err == nil {
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
