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

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/gateway"
	"github.com/yanet-platform/yanet2/controlplane/httpproxy"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth"
	"github.com/yanet-platform/yanet2/controlplane/internal/xgrpc"
	"github.com/yanet-platform/yanet2/controlplane/ynpb"
)

type gatewayOptions struct {
	BuiltInModules []BuiltInModule
	Log            *zap.Logger
	LogLevel       *zap.AtomicLevel
}

func newGatewayOptions() *gatewayOptions {
	return &gatewayOptions{
		Log: zap.NewNop(),
	}
}

// GatewayOption is a function that configures the Gateway.
type GatewayOption func(*gatewayOptions)

// WithBuiltInModule adds a built-in module to the Gateway.
func WithBuiltInModule(module BuiltInModule) GatewayOption {
	return func(o *gatewayOptions) {
		o.BuiltInModules = append(o.BuiltInModules, module)
	}
}

// WithBuiltInModule adds a built-in device to the Gateway.
func WithBuiltInDevice(device BuiltInModule) GatewayOption {
	return func(o *gatewayOptions) {
		o.BuiltInModules = append(o.BuiltInModules, device)
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
	builtInModules []*BuiltInModuleRunner
	registry       *BackendRegistry
	log            *zap.Logger
}

// NewGateway creates a new Gateway API.
func NewGateway(cfg *Config, shm *ffi.SharedMemory, options ...GatewayOption) (*Gateway, error) {
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

	// Create AuthService for authentication introspection.
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

	gatewayService := NewGatewayService(registry, opts.Log)
	loggingService := NewLoggingService(opts.LogLevel, opts.Log)
	inspectService := NewInspectService(cfg.InstanceID, shm)
	pipelineService := NewPipelineService(cfg.InstanceID, shm, opts.Log)
	functionService := NewFunctionService(cfg.InstanceID, shm, opts.Log)
	countersService := NewCountersService(cfg.InstanceID, shm)

	ynpb.RegisterGatewayServer(server, gatewayService)
	log.Info("registered service", zap.String("service", fmt.Sprintf("%T", gatewayService)))

	ynpb.RegisterLoggingServer(server, loggingService)
	log.Info("registered service", zap.String("service", fmt.Sprintf("%T", loggingService)))

	ynpb.RegisterInspectServiceServer(server, inspectService)
	log.Info("registered service", zap.String("service", fmt.Sprintf("%T", inspectService)))

	ynpb.RegisterPipelineServiceServer(server, pipelineService)
	log.Info("registered service", zap.String("service", fmt.Sprintf("%T", pipelineService)))

	ynpb.RegisterFunctionServiceServer(server, functionService)
	log.Info("registered service", zap.String("service", fmt.Sprintf("%T", functionService)))

	ynpb.RegisterCountersServiceServer(server, countersService)
	log.Info("registered service", zap.String("service", fmt.Sprintf("%T", countersService)))

	ynpb.RegisterAuthServiceServer(server, authService)
	log.Info("registered service", zap.String("service", fmt.Sprintf("%T", authService)))

	// Register built-in services in the registry for HTTP gateway access.
	if err := registerBuiltInServices(registry, cfg.Server.Endpoint, cfg.Server.TLS, log); err != nil {
		return nil, fmt.Errorf("failed to register built-in services: %w", err)
	}

	builtInModules := make([]*BuiltInModuleRunner, 0)
	for _, mod := range opts.BuiltInModules {
		builtInModules = append(builtInModules, NewBuiltInModuleRunner(
			mod,
			cfg.Server.Endpoint,
			cfg.Server.TLS,
			log,
		))
	}

	return &Gateway{
		cfg:            cfg,
		server:         server,
		builtInModules: builtInModules,
		registry:       registry,
		log:            log,
	}, nil
}

// Close closes the gateway API.
func (m *Gateway) Close() error {
	for _, builtInModule := range m.builtInModules {
		if err := builtInModule.Close(); err != nil {
			m.log.Warn("failed to close built-in module",
				zap.String("module", fmt.Sprintf("%T", builtInModule)),
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

	for _, builtInModule := range m.builtInModules {
		wg.Go(func() error {
			m.log.Info("starting built-in module", zap.String("module", fmt.Sprintf("%T", builtInModule.module)))
			return builtInModule.Run(ctx)
		})
	}

	// Emit a single deterministic readiness marker once every built-in
	// module has finished its initial service registration. Functional
	// tests grep for this exact line to know the gateway is ready to
	// accept module RPCs.
	if len(m.builtInModules) > 0 {
		wg.Go(func() error {
			for _, builtInModule := range m.builtInModules {
				select {
				case <-ctx.Done():
					return nil
				case <-builtInModule.Ready():
				}
			}
			m.log.Info("all built-in modules ready",
				zap.Int("count", len(m.builtInModules)),
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

// registerBuiltInServices registers built-in services in the registry for HTTP
// gateway access.
func registerBuiltInServices(registry *BackendRegistry, endpoint string, tlsCfg *gateway.TLSConfig, log *zap.Logger) error {
	creds, err := gateway.TransportCredentials(tlsCfg, endpoint)
	if err != nil {
		return fmt.Errorf("failed to build loopback TLS credentials: %w", err)
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
		return fmt.Errorf("failed to create gRPC client for built-in services: %w", err)
	}

	backend := &proxy.SingleBackend{
		GetConn: func(ctx context.Context) (context.Context, *grpc.ClientConn, error) {
			md, _ := metadata.FromIncomingContext(ctx)
			outCtx := metadata.NewOutgoingContext(ctx, md.Copy())
			return outCtx, conn, nil
		},
	}

	builtInServices := []string{
		"ynpb.Gateway",
		"ynpb.Logging",
		"ynpb.InspectService",
		"ynpb.PipelineService",
		"ynpb.FunctionService",
		"ynpb.CountersService",
		"ynpb.Auth",
	}

	for _, serviceName := range builtInServices {
		registry.RegisterBackend(serviceName, backend, endpoint)
		log.Debug("registered built-in service in registry", zap.String("service", serviceName))
	}

	return nil
}
