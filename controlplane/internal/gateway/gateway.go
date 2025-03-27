package gateway

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/siderolabs/grpc-proxy/proxy"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/internal/ffi"
	"github.com/yanet-platform/yanet2/controlplane/internal/xgrpc"
	"github.com/yanet-platform/yanet2/controlplane/ynpb"
)

type Module interface {
	Close() error
	Run(ctx context.Context) error
}

type gatewayOptions struct {
	BuiltInModules []Module
	Log            *zap.SugaredLogger
	LogLevel       *zap.AtomicLevel
}

func newGatewayOptions() *gatewayOptions {
	return &gatewayOptions{
		Log: zap.NewNop().Sugar(),
	}
}

// GatewayOption is a function that configures the Gateway.
type GatewayOption func(*gatewayOptions)

// WithBuiltInModule adds a built-in module to the Gateway.
func WithBuiltInModule(module Module) GatewayOption {
	return func(o *gatewayOptions) {
		o.BuiltInModules = append(o.BuiltInModules, module)
	}
}

// WithLog sets the logger for the Gateway.
func WithLog(log *zap.SugaredLogger) GatewayOption {
	return func(o *gatewayOptions) {
		o.Log = log
	}
}

// WithAtomicLogLevel sets the atomic logger level for the Gateway.
//
// This level can be changed at runtime.
func WithAtomicLogLevel(level *zap.AtomicLevel) GatewayOption {
	return func(o *gatewayOptions) {
		o.LogLevel = level
	}
}

// Gateway is the Gateway API to YANET modules.
//
// It is a gRPC server that acts as a proxy for each YANET module's
// configuration and monitoring.
//
// Such abstraction is required for the following reasons:
// - Unify distinct modules under a single entry point.
// - Serialize requests, because of possible conflicting configurations.
// - Implement unified access control.
//
// Think of it as gRPC middleware if it were a single process.
type Gateway struct {
	cfg            *Config
	server         *grpc.Server
	builtInModules []Module
	registry       *BackendRegistry
	log            *zap.SugaredLogger
}

// NewGateway creates a new Gateway API.
func NewGateway(cfg *Config, shm *ffi.SharedMemory, options ...GatewayOption) *Gateway {
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

		log.Debugf("proxying request %q to %q", fullMethodName, service)

		return proxy.One2One, []proxy.Backend{backend}, nil
	}

	server := grpc.NewServer(
		grpc.ForceServerCodecV2(proxy.Codec()),
		grpc.UnknownServiceHandler(
			proxy.TransparentHandler(director),
		),
	)

	gatewayService := NewGatewayService(registry, opts.Log)
	loggingService := NewLoggingService(opts.LogLevel, opts.Log)
	inspectService := NewInspectService(shm)

	ynpb.RegisterGatewayServer(server, gatewayService)
	log.Infow("registered service", zap.String("service", fmt.Sprintf("%T", gatewayService)))

	ynpb.RegisterLoggingServer(server, loggingService)
	log.Infow("registered service", zap.String("service", fmt.Sprintf("%T", loggingService)))

	ynpb.RegisterInspectServiceServer(server, inspectService)
	log.Infow("registered service", zap.String("service", fmt.Sprintf("%T", inspectService)))

	return &Gateway{
		cfg:            cfg,
		server:         server,
		builtInModules: opts.BuiltInModules,
		registry:       registry,
		log:            log,
	}
}

// Close closes the gateway API.
func (m *Gateway) Close() error {
	for _, builtInModule := range m.builtInModules {
		if err := builtInModule.Close(); err != nil {
			m.log.Warnw("failed to close built-in module",
				zap.String("module", fmt.Sprintf("%T", builtInModule)),
				zap.Error(err),
			)
		}
	}

	return nil
}

// Run runs the gateway API until the specified context is canceled.
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
	if m.cfg.Server.HTTPEndpoint != "" {
		wg.Go(func() error {
			return m.runHTTPServer(ctx)
		})
	}

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

// runHTTPServer runs the HTTP server that provides access to gRPC services
// via HTTP.
func (m *Gateway) runHTTPServer(ctx context.Context) error {
	server := &http.Server{
		Addr:    m.cfg.Server.HTTPEndpoint,
		Handler: NewHTTPHandler(m.registry, m.log),
	}

	// Set up graceful shutdown.
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		m.log.Infow("shutting down HTTP server", zap.String("addr", m.cfg.Server.HTTPEndpoint))
		if err := server.Shutdown(shutdownCtx); err != nil {
			m.log.Warnw("failed to shut down HTTP server", zap.Error(err))
		}
	}()

	m.log.Infow("exposing HTTP <-> gRPC gateway", zap.String("addr", m.cfg.Server.HTTPEndpoint))
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		return fmt.Errorf("failed to serve: %w", err)
	}

	return nil
}
