package gateway

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/siderolabs/grpc-proxy/proxy"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/go/grpcmetrics"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	"github.com/yanet-platform/yanet2/common/go/readiness"
	commonxgrpc "github.com/yanet-platform/yanet2/common/go/xgrpc"
	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
	"github.com/yanet-platform/yanet2/controlplane/httpproxy"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth"
	"github.com/yanet-platform/yanet2/controlplane/internal/xgrpc"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// Service is the interface that gateway services must implement.
//
// When Endpoint returns an empty string the service shares the gateway's own
// gRPC server. When Endpoint returns a non-empty host:port or unix path the
// service runs its own listener and registers itself with the gateway client.
type Service interface {
	Name() string
	// Endpoint returns "" when the service shares the gateway's own gRPC
	// server, or a host:port / unix path when the service runs its own
	// listener.
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

// serviceEntry pairs a service with its declared backend kind.
type serviceEntry struct {
	service Service
	kind    BackendKind
}

// defaultPerServiceMethodLimit caps the number of distinct grpc_method label
// values the gateway's gRPC server metrics track per grpc_service.
const defaultPerServiceMethodLimit = 64

// errorReasonMetadataKey is the trailer metadata key that classifies a
// NotFound status without relying on its message text.
//
// It is part of the wire contract with clients that classify on it, so
// renaming it breaks them.
const errorReasonMetadataKey = "x-yanet-error-reason"

// errorReasonServiceUnregistered marks a NotFound raised because the
// requested gRPC service has no backend registered.
//
// It is part of the wire contract with clients that classify on it, so
// renaming it breaks them.
const errorReasonServiceUnregistered = "service-unregistered"

type gatewayOptions struct {
	Services []serviceEntry
	Log      *zap.Logger
	LogLevel *zap.AtomicLevel
}

func newGatewayOptions() *gatewayOptions {
	return &gatewayOptions{Log: zap.NewNop()}
}

// GatewayOption is a function that configures the Gateway.
type GatewayOption func(*gatewayOptions)

// WithService adds an in-process module or device service to the Gateway.
func WithService(service Service) GatewayOption {
	return func(o *gatewayOptions) {
		o.Services = append(o.Services, serviceEntry{service: service, kind: BackendKindInProcess})
	}
}

// WithBuiltinService adds a framework service that shares the gateway's own gRPC server.
func WithBuiltinService(service Service) GatewayOption {
	return func(o *gatewayOptions) {
		o.Services = append(o.Services, serviceEntry{service: service, kind: BackendKindBuiltin})
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
	cfg              *Config
	server           *grpc.Server
	services         []Service
	serviceRunners   []*ServiceRunner
	registry         *BackendRegistry
	readinessTracker *readiness.Tracker
	log              *zap.Logger
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
			// The HTTP surface answers 503 for this same condition,
			// because it cannot tell an HTTP client "this service never
			// existed" from "its control plane is unreachable" and so
			// separates the two by status code, reserving 404 for a
			// NotFound a backend itself returns. gRPC keeps both as
			// NotFound and separates them by trailer instead: Unavailable
			// is not free to reuse, since clients already give it a
			// different exit code and a different remedy, retrying the
			// connection, than a registry miss, which needs the operator
			// started or registered first.
			//
			// The trailer is what lets a client classify this NotFound
			// structurally. The message text stays unchanged because
			// clients released before the trailer still match on it, and
			// stays that way until those are gone. SetTrailer only fails
			// without a live server stream, which the proxying handler
			// always supplies. Losing it would cost only the marker.
			_ = grpc.SetTrailer(ctx, metadata.Pairs(errorReasonMetadataKey, errorReasonServiceUnregistered))
			return proxy.One2One, nil, status.Errorf(codes.NotFound, "unknown service")
		}

		log.Debug("proxying request",
			zap.String("method", fullMethodName),
			zap.String("service", service),
		)

		return proxy.One2One, []proxy.Backend{backend}, nil
	}

	// server is assigned below, after the metrics retention predicate and
	// service filter are built. Neither reads server before Serve is called:
	// the retention closure only reads server.GetServiceInfo() at Collect
	// time, and serviceKnown defers its own server.GetServiceInfo() read to
	// its first call, which an RPC can only trigger after Serve — by then
	// registration is complete. The forward reference is safe.
	var server *grpc.Server

	// serviceKnown snapshots the gateway's own statically registered gRPC
	// services exactly once, on first use, rather than on every call.
	//
	// server.GetServiceInfo() allocates a fresh map on every call, and the
	// service filter runs on the interceptor hot path, so the snapshot is
	// taken lazily via sync.OnceValue instead of eagerly here or repeatedly
	// per call. The gateway's own services are static once NewGateway
	// returns, so a single snapshot never goes stale.
	serviceKnown := sync.OnceValue(func() map[string]struct{} {
		known := map[string]struct{}{}
		for name := range server.GetServiceInfo() {
			known[name] = struct{}{}
		}
		return known
	})

	// serviceFilter rejects any service the gateway does not know about,
	// stopping metric series and per-service method bookkeeping allocation
	// at the source for unauthenticated scans against random service names.
	// A registry backend lookup is mutex-guarded and cheap. The gateway's
	// own registered services are read from the cached serviceKnown snapshot.
	serviceFilter := func(service string) bool {
		if _, ok := registry.GetBackend(service); ok {
			return true
		}
		_, ok := serviceKnown()[service]
		return ok
	}

	// retention keeps a series iff its grpc_service is a registry backend
	// (built-in or proxied module) or is registered directly on the
	// gateway's own gRPC server.
	//
	// Three complementary bounds keep the metric label space finite: the
	// service filter stops unknown-service allocation at the source, the
	// per-service method limit bounds grpc_method cardinality for known
	// services, and this retention predicate prunes series for services
	// that were known but have since disappeared from the registry.
	retention := func() func(metrics.MetricID) bool {
		// Snapshot live service names outside the metric maps' lock, as
		// required by the Retention contract.
		live := map[string]struct{}{}
		for _, entry := range registry.ListBackends() {
			live[entry.Service()] = struct{}{}
		}
		for name := range server.GetServiceInfo() {
			live[name] = struct{}{}
		}

		return func(id metrics.MetricID) bool {
			_, ok := live[id.Labels[grpcmetrics.ServiceLabel]]
			return ok
		}
	}

	serverMetrics := grpcmetrics.New(
		grpcmetrics.WithPerServiceMethodLimit(defaultPerServiceMethodLimit),
		grpcmetrics.WithRetention(retention),
		grpcmetrics.WithServiceFilter(serviceFilter),
	)

	serverOpts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(
			serverMetrics.UnaryServerInterceptor(),
			auth.UnaryServerInterceptor(authManager, log),
			xgrpc.AccessLogInterceptor(log),
		),
		grpc.ChainStreamInterceptor(
			serverMetrics.StreamServerInterceptor(),
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
	server = grpc.NewServer(serverOpts...)

	gatewayService := NewGatewayService(registry, WithGatewayServiceLog(log))
	ynpb.RegisterGatewayServer(server, gatewayService)

	ynpb.RegisterAuthServiceServer(server, authService)

	rdTracker := readiness.NewTracker(
		[]string{gatewayReadinessScope},
		readiness.WithDrainLatch(),
		readiness.WithLog(log),
	)
	readinessSvc := NewReadinessService(rdTracker)
	ynpb.RegisterReadinessServiceServer(server, readinessSvc)

	metricsService := NewMetricsService(metricsCollectors(serverMetrics, opts.Services)...)
	ynpb.RegisterMetricsServiceServer(server, metricsService)

	// Dial a single loopback connection shared by services hosted on the
	// gateway's own gRPC server.
	//
	// Out-of-process module backends (from the Register RPC) each get their
	// own connection via RegisterBackend in the registration loop.
	creds, err := TransportCredentials(cfg.Server.TLS, cfg.Server.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to build loopback TLS credentials: %w", err)
	}

	loopback, err := dialBackend(cfg.Server.Endpoint, creds)
	if err != nil {
		return nil, fmt.Errorf("failed to create loopback backend for gateway-hosted services: %w", err)
	}

	for _, service := range []string{
		"controlplane.ynpb.v1.Gateway",
		"controlplane.ynpb.v1.Auth",
		ynpb.ReadinessService_ServiceDesc.ServiceName,
		ynpb.MetricsService_ServiceDesc.ServiceName,
	} {
		registry.RegisterBackend(service, loopback, BackendKindBuiltin)
		log.Info("registered service in registry",
			zap.String("service", service),
			zap.Stringer("kind", BackendKindBuiltin),
		)
	}

	var services []Service
	var serviceRunners []*ServiceRunner

	for _, entry := range opts.Services {
		if entry.service.Endpoint() == "" {
			// Shared-server service: register on the gateway's gRPC server and
			// point every service name at the shared loopback backend using the
			// declared kind.
			entry.service.RegisterService(server)

			for _, name := range entry.service.ServicesNames() {
				registry.RegisterBackend(name, loopback, entry.kind)
				log.Info("registered service in registry",
					zap.String("service", name),
					zap.Stringer("kind", entry.kind),
				)
			}
		} else {
			// Out-of-process: wrap in a ServiceRunner.
			runner := NewServiceRunner(entry.service, cfg.Server.Endpoint, cfg.Server.TLS, WithServiceRunnerLog(log))
			serviceRunners = append(serviceRunners, runner)
		}

		services = append(services, entry.service)
	}

	return &Gateway{
		cfg:              cfg,
		server:           server,
		services:         services,
		serviceRunners:   serviceRunners,
		registry:         registry,
		readinessTracker: rdTracker,
		log:              log,
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

	if err := m.registry.Close(); err != nil {
		m.log.Warn("failed to close backend registry", zap.Error(err))
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

	wg.Go(func() error {
		return m.runRegistrySweeper(ctx)
	})

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

	// The readiness marker is published once every out-of-process service
	// runner has finished its initial service registration, and
	// immediately when there are none. Both branches emit the identical
	// "all built-in modules ready" message, so a run publishes at most
	// one readiness marker: the waiting branch publishes none if the
	// context is canceled before every runner has registered.
	//
	// That message text is matched verbatim by an external observer to
	// decide the gateway is ready to accept module RPCs, so its wording is a
	// contract and must stay identical between the branches.
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
			m.readinessTracker.Set(gatewayReadinessScope, readinesspb.State_STATE_READY)
			return nil
		})
	} else {
		m.log.Info("all built-in modules ready", zap.Int("count", 0))
		m.readinessTracker.Set(gatewayReadinessScope, readinesspb.State_STATE_READY)
	}

	<-ctx.Done()

	m.readinessTracker.Drain()

	m.log.Info("stopping gRPC gateway", zap.Stringer("addr", listener.Addr()))
	defer m.log.Info("stopped gRPC gateway", zap.Stringer("addr", listener.Addr()))

	commonxgrpc.StopGracefully(m.server, commonxgrpc.GracefulStopTimeout, func() {
		m.log.Warn("graceful stop timed out, forcing shutdown",
			zap.Duration("grace_period", commonxgrpc.GracefulStopTimeout),
		)
	})

	return wg.Wait()
}

// runRegistrySweeper periodically evicts stale external backends from the
// registry until ctx is canceled.
//
// Eviction is skipped entirely when PreserveStaleBackends is set.
func (m *Gateway) runRegistrySweeper(ctx context.Context) error {
	if m.cfg.Registry.PreserveStaleBackends {
		m.log.Info("registry eviction disabled, preserving stale backends")
		return nil
	}

	// The YAML path is covered by validation: xcfg.NonZero rejects a zero
	// TTL or sweep interval, and RegistryConfig.Validate rejects a negative
	// one. gateway.Config is exported, though, so the fallback below still
	// guards a programmatic literal that sets either field to zero or
	// below, which would otherwise panic this goroutine (and the director
	// with it) or evict every live external backend on the first sweep.
	ttl := m.cfg.Registry.TTL.Unwrap()
	if ttl <= 0 {
		m.log.Warn("non-positive registry ttl, falling back to default",
			zap.Duration("configured", ttl),
			zap.Duration("default", defaultRegistryTTL),
		)
		ttl = defaultRegistryTTL
	}

	interval := m.cfg.Registry.SweepInterval.Unwrap()
	if interval <= 0 {
		m.log.Warn("non-positive registry sweep interval, falling back to default",
			zap.Duration("configured", interval),
			zap.Duration("default", defaultRegistrySweepInterval),
		)
		interval = defaultRegistrySweepInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			before := time.Now().UTC().Add(-ttl)
			for _, entry := range m.registry.EvictStale(before) {
				m.log.Info("evicted stale service from registry",
					zap.String("service", entry.Service()),
					zap.String("endpoint", entry.Endpoint()),
					zap.Time("last_seen_at", entry.LastSeenAt()),
					zap.Stringer("kind", entry.Kind()),
				)
			}
		}
	}
}

// runHTTPServer runs the HTTP server that provides access to gRPC services
// via HTTP.
func (m *Gateway) runHTTPServer(ctx context.Context) error {
	server := &http.Server{
		Addr: m.cfg.Server.HTTPEndpoint,
		Handler: httpproxy.GzipMiddleware(
			httpproxy.NewHTTPHandler(
				m.registry,
				httpproxy.WithLog(m.log),
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
