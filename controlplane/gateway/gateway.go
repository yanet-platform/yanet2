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
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/httpproxy"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth"
	"github.com/yanet-platform/yanet2/controlplane/internal/xgrpc"
	"github.com/yanet-platform/yanet2/controlplane/ynpb"
)

// Service is the interface that gateway services must implement.
//
// When Endpoint returns an empty string the service is in-process:
// it shares the gateway's gRPC server. When Endpoint returns a
// non-empty host:port or unix path the service runs its own listener
// and registers itself with the gateway client.
type Service interface {
	Name() string
	// Endpoint returns "" for in-process services, or a host:port / unix
	// path for out-of-process services that run their own listener.
	Endpoint() string
	ServicesNames() []string
	RegisterService(server *grpc.Server)
}

// BackgroundService is an optional interface for services that need to run
// background work while the gateway is active.
type BackgroundService interface {
	Run(ctx context.Context) error
}

// ClosableService is an optional interface for services that hold resources
// that must be released on shutdown.
type ClosableService interface {
	Close() error
}

type gatewayOptions struct {
	Services []Service
	Log      *zap.Logger
	LogLevel *zap.AtomicLevel
}

func newGatewayOptions() *gatewayOptions {
	return &gatewayOptions{
		Log: zap.NewNop(),
	}
}

// GatewayOption is a function that configures the Gateway.
type GatewayOption func(*gatewayOptions)

// WithService adds a service to the Gateway.
func WithService(service Service) GatewayOption {
	return func(o *gatewayOptions) {
		o.Services = append(o.Services, service)
	}
}

// WithLog sets the logger for the Gateway.
func WithLog(log *zap.Logger) GatewayOption {
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
	services       []Service
	serviceRunners []*ServiceRunner
	registry       *BackendRegistry
	log            *zap.Logger
}

// NewGateway creates a new Gateway API.
func NewGateway(cfg *Config, options ...GatewayOption) (*Gateway, error) {
	opts := newGatewayOptions()
	for _, o := range options {
		o(opts)
	}
	log := opts.Log
	registry := NewBackendRegistry()

	authManager, err := auth.NewManager(&cfg.Auth, auth.WithLog(log))
	if err != nil {
		return nil, fmt.Errorf("failed to create auth manager: %w", err)
	}

	authService := NewAuthService(authManager)

	director := func(ctx context.Context, fullMethodName string) (proxy.Mode, []proxy.Backend, error) {
		service, _, err := xgrpc.ParseFullMethod(fullMethodName)
		if err != nil {
			return proxy.One2One, nil, status.Errorf(codes.NotFound, "malformed gRPC method name: %v", err)
		}

		backend, ok := registry.GetBackend(service)
		if !ok {
			return proxy.One2One, nil, status.Errorf(codes.NotFound, "unknown service")
		}

		log.Debug("proxying request",
			zap.String("method", fullMethodName),
			zap.String("service", service),
		)

		return proxy.One2One, []proxy.Backend{backend}, nil
	}

	serverOpts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(
			auth.UnaryServerInterceptor(authManager, log),
			xgrpc.AccessLogInterceptor(log),
		),
		grpc.ChainStreamInterceptor(
			auth.StreamServerInterceptor(authManager, log),
		),
		grpc.MaxRecvMsgSize(1024 * 1024 * 256),
		grpc.MaxSendMsgSize(1024 * 1024 * 256),
		grpc.ForceServerCodecV2(proxy.Codec()),
		grpc.UnknownServiceHandler(
			proxy.TransparentHandler(director),
		),
	}
	if cfg.Server.TLS != nil {
		creds, err := cfg.Server.TLS.ServerCredentials()
		if err != nil {
			return nil, fmt.Errorf("load gateway TLS: %w", err)
		}
		serverOpts = append(serverOpts, grpc.Creds(creds))
	}
	server := grpc.NewServer(serverOpts...)

	gatewayService := NewGatewayService(registry, log)
	ynpb.RegisterGatewayServer(server, gatewayService)
	log.Info("registered service", zap.String("service", fmt.Sprintf("%T", gatewayService)))

	ynpb.RegisterAuthServiceServer(server, authService)
	log.Info("registered service", zap.String("service", fmt.Sprintf("%T", authService)))

	// Register Gateway and Auth as loopback backends for HTTP proxy access.
	loopback, err := newLoopbackBackend(cfg.Server.Endpoint, cfg.Server.TLS)
	if err != nil {
		return nil, fmt.Errorf("failed to create loopback backend for built-in services: %w", err)
	}
	registry.RegisterBackend("ynpb.Gateway", loopback, cfg.Server.Endpoint)
	log.Debug("registered built-in service in registry", zap.String("service", "ynpb.Gateway"))
	registry.RegisterBackend("ynpb.Auth", loopback, cfg.Server.Endpoint)
	log.Debug("registered built-in service in registry", zap.String("service", "ynpb.Auth"))

	var allServices []Service
	var serviceRunners []*ServiceRunner

	for _, service := range opts.Services {
		if service.Endpoint() == "" {
			// In-process: register on the shared server.
			service.RegisterService(server)
			log.Info("registered in-process service", zap.String("service", service.Name()))

			// Share one loopback backend across all service names for this service.
			inprocBackend, err := newLoopbackBackend(cfg.Server.Endpoint, cfg.Server.TLS)
			if err != nil {
				return nil, fmt.Errorf("failed to create loopback backend for service %q: %w", service.Name(), err)
			}
			for _, name := range service.ServicesNames() {
				registry.RegisterBackend(name, inprocBackend, cfg.Server.Endpoint)
				log.Debug("registered in-process service in registry", zap.String("service", name))
			}
		} else {
			// Out-of-process: wrap in a ServiceRunner.
			runner := NewServiceRunner(service, cfg.Server.Endpoint, cfg.Server.TLS, log)
			serviceRunners = append(serviceRunners, runner)
		}
		allServices = append(allServices, service)
	}

	return &Gateway{
		cfg:            cfg,
		server:         server,
		services:       allServices,
		serviceRunners: serviceRunners,
		registry:       registry,
		log:            log,
	}, nil
}

// newLoopbackBackend creates a gRPC proxy backend that dials back to the
// gateway's own listener.
func newLoopbackBackend(endpoint string, tlsCfg *TLSConfig) (proxy.Backend, error) {
	creds, err := TransportCredentials(tlsCfg, endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to build loopback TLS credentials: %w", err)
	}

	conn, err := grpc.NewClient(
		"passthrough:target",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			dialer := net.Dialer{}
			return dialer.DialContext(ctx, "tcp", endpoint)
		}),
		grpc.WithDefaultCallOptions(
			grpc.ForceCodecV2(proxy.Codec()),
			grpc.UseCompressor(gzip.Name),
		),
		grpc.WithTransportCredentials(creds),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create loopback gRPC client: %w", err)
	}

	return &proxy.SingleBackend{
		GetConn: func(ctx context.Context) (context.Context, *grpc.ClientConn, error) {
			md, _ := metadata.FromIncomingContext(ctx)
			outCtx := metadata.NewOutgoingContext(ctx, md.Copy())
			return outCtx, conn, nil
		},
	}, nil
}

// Close closes the gateway API.
func (m *Gateway) Close() error {
	for _, service := range m.services {
		closer, ok := service.(ClosableService)
		if !ok {
			continue
		}
		if err := closer.Close(); err != nil {
			m.log.Warn("failed to close service",
				zap.String("service", fmt.Sprintf("%T", service)),
				zap.Error(err),
			)
		}
	}

	return nil
}

// Run runs the gateway API until the specified context is canceled.
func (m *Gateway) Run(ctx context.Context) error {
	m.log.Info("starting gRPC gateway")

	listener, err := net.Listen("tcp", m.cfg.Server.Endpoint)
	if err != nil {
		return fmt.Errorf("failed to initialize gRPC listener: %w", err)
	}

	m.log.Info("exposing gRPC gateway", zap.Stringer("addr", listener.Addr()))

	wg, ctx := errgroup.WithContext(ctx)

	wg.Go(func() error {
		return m.server.Serve(listener)
	})
	if m.cfg.Server.HTTPEndpoint != "" {
		wg.Go(func() error {
			return m.runHTTPServer(ctx)
		})
	}

	for _, runner := range m.serviceRunners {
		wg.Go(func() error {
			m.log.Info("starting out-of-process service", zap.String("service", fmt.Sprintf("%T", runner.module)))
			return runner.Run(ctx)
		})
	}

	// Schedule Run for any in-process BackgroundService.
	for _, service := range m.services {
		if service.Endpoint() == "" {
			if background, ok := service.(BackgroundService); ok {
				wg.Go(func() error {
					return background.Run(ctx)
				})
			}
		}
	}

	// Emit a single deterministic readiness marker once every out-of-process
	// service runner has finished its initial service registration. Functional
	// tests grep for this exact line to know the gateway is ready to accept
	// module RPCs.
	if len(m.serviceRunners) > 0 {
		wg.Go(func() error {
			for _, runner := range m.serviceRunners {
				select {
				case <-ctx.Done():
					return nil
				case <-runner.Ready():
				}
			}
			m.log.Info("all built-in modules ready",
				zap.Int("count", len(m.serviceRunners)),
			)
			return nil
		})
	} else {
		m.log.Info("all built-in modules ready", zap.Int("count", 0))
	}

	<-ctx.Done()

	m.log.Info("stopping gRPC gateway", zap.Stringer("addr", listener.Addr()))
	defer m.log.Info("stopped gRPC gateway", zap.Stringer("addr", listener.Addr()))

	m.server.GracefulStop()

	return wg.Wait()
}

// runHTTPServer runs the HTTP server that provides access to gRPC services
// via HTTP.
func (m *Gateway) runHTTPServer(ctx context.Context) error {
	server := &http.Server{
		Addr: m.cfg.Server.HTTPEndpoint,
		Handler: httpproxy.GzipMiddleware(
			httpproxy.NewHTTPHandler(
				m.registry,
				m.log,
			),
		),
	}

	// Set up graceful shutdown.
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		m.log.Info("shutting down HTTP server", zap.String("addr", m.cfg.Server.HTTPEndpoint))
		if err := server.Shutdown(shutdownCtx); err != nil {
			m.log.Warn("failed to shut down HTTP server", zap.Error(err))
		}
	}()

	scheme := "http"
	listen := server.ListenAndServe
	if tlsCfg := m.cfg.Server.TLS; tlsCfg != nil {
		scheme = "https"
		cert, key := tlsCfg.CertFile.Unwrap(), tlsCfg.KeyFile.Unwrap()

		listen = func() error {
			return server.ListenAndServeTLS(cert, key)
		}
	}

	m.log.Info("exposing HTTP <-> gRPC gateway",
		zap.String("scheme", scheme),
		zap.String("addr", m.cfg.Server.HTTPEndpoint),
	)
	if err := listen(); err != http.ErrServerClosed {
		return fmt.Errorf("failed to serve: %w", err)
	}

	return nil
}
