package gateway

import (
	"context"
	"fmt"
	"net"

	"github.com/siderolabs/grpc-proxy/proxy"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/internal/pkg/xgrpc"
	"github.com/yanet-platform/yanet2/controlplane/ynpb"
)

type Module interface {
	Run(ctx context.Context) error
}

type gatewayOptions struct {
	BuiltInModules []Module
	Log            *zap.SugaredLogger
}

func newGatewayOptions() *gatewayOptions {
	return &gatewayOptions{
		Log: zap.NewNop().Sugar(),
	}
}

type GatewayOption func(*gatewayOptions)

func WithBuiltInModule(module Module) GatewayOption {
	return func(o *gatewayOptions) {
		o.BuiltInModules = append(o.BuiltInModules, module)
	}
}

func WithLog(log *zap.SugaredLogger) GatewayOption {
	return func(o *gatewayOptions) {
		o.Log = log
	}
}

type Gateway struct {
	cfg            *Config
	server         *grpc.Server
	builtInModules []Module
	registry       *BackendRegistry
	log            *zap.SugaredLogger
}

func NewGateway(cfg *Config, options ...GatewayOption) *Gateway {
	opts := newGatewayOptions()
	for _, o := range options {
		o(opts)
	}
	log := opts.Log
	registry := NewBackendRegistry()

	director := func(ctx context.Context, fullMethodName string) (proxy.Mode, []proxy.Backend, error) {
		service, _, err := xgrpc.ParseFullMethod(fullMethodName)
		if err != nil {
			return proxy.One2One, nil, status.Errorf(codes.NotFound, "malformed gRPC method name: %v", err)
		}

		backend, ok := registry.GetBackend(service)
		if !ok {
			return proxy.One2One, nil, status.Errorf(codes.NotFound, "unknown service")
		}

		log.Debugf("proxying request %q", fullMethodName)

		return proxy.One2One, []proxy.Backend{backend}, nil
	}

	server := grpc.NewServer(
		grpc.ForceServerCodecV2(proxy.Codec()),
		grpc.UnknownServiceHandler(
			proxy.TransparentHandler(director),
		),
	)

	service := NewGatewayService(registry, opts.Log)

	ynpb.RegisterGatewayServer(server, service)
	log.Infof("registered gateway service")

	return &Gateway{
		cfg:            cfg,
		server:         server,
		builtInModules: opts.BuiltInModules,
		registry:       registry,
		log:            log,
	}
}

func (m *Gateway) Run(ctx context.Context) error {
	m.log.Infof("starting gRPC gateway")

	listener, err := net.Listen("tcp", m.cfg.Server.Endpoint)
	if err != nil {
		return fmt.Errorf("failed to initialize gRPC listener: %w", err)
	}

	m.log.Infow("exposing gRPC gateway", zap.Stringer("addr", listener.Addr()))

	wg, ctx := errgroup.WithContext(ctx)
	wg.Go(func() error {
		return m.server.Serve(listener)
	})
	for _, builtInModule := range m.builtInModules {
		wg.Go(func() error {
			m.log.Infow("starting built-in module", zap.String("module", fmt.Sprintf("%T", builtInModule)))
			return builtInModule.Run(ctx)
		})
	}

	<-ctx.Done()

	m.log.Infow("stopping gRPC gateway", zap.Stringer("addr", listener.Addr()))
	defer m.log.Infow("stopped gRPC gateway", zap.Stringer("addr", listener.Addr()))

	m.server.GracefulStop()

	return wg.Wait()
}
