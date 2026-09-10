package gateway

import (
	"context"
	"crypto/x509"
	"errors"
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
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

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
// A framework service shares the gateway's own gRPC server. A module or
// device service gets a gRPC server of its own behind an in-memory listener,
// so inside the gateway process it never touches the network.
type Service interface {
	Name() string
	// Endpoint returns the address the service would bind when run in a
	// separate process. Inside the gateway process it is ignored.
	Endpoint() string
	ServicesNames() []string
	RegisterService(server *grpc.Server)
}

// BackgroundService is an optional interface for services that need to run
// background work while the gateway is active.
type BackgroundService interface {
	Run(ctx context.Context) error
}

// serviceEntry pairs a service with its declared backend kind.
type serviceEntry struct {
	Service Service
	Kind    BackendKind
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
	Listener net.Listener
}

func newGatewayOptions() *gatewayOptions {
	return &gatewayOptions{Log: zap.NewNop()}
}

// GatewayOption is a function that configures the Gateway.
type GatewayOption func(*gatewayOptions)

// WithService adds an in-process module or device service to the Gateway.
func WithService(service Service) GatewayOption {
	return func(o *gatewayOptions) {
		o.Services = append(o.Services, serviceEntry{Service: service, Kind: BackendKindInProcess})
	}
}

// WithBuiltinService adds a framework service that shares the gateway's own gRPC server.
func WithBuiltinService(service Service) GatewayOption {
	return func(o *gatewayOptions) {
		o.Services = append(o.Services, serviceEntry{Service: service, Kind: BackendKindBuiltin})
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

// WithListener hands the Gateway an already-open listener to serve instead
// of binding cfg.Server.Endpoint itself.
//
// Run serves this listener directly and never calls net.Listen. The server
// closes it when it stops, and closing it again is a harmless net.ErrClosed
// — close it yourself only if Run never runs.
func WithListener(listener net.Listener) GatewayOption {
	return func(o *gatewayOptions) {
		o.Listener = listener
	}
}

// serviceRunner runs one hosted Service for the gateway's lifetime.
//
// The gateway starts every runner, waits for every runner to become ready
// before it publishes its own readiness, and closes every runner on
// shutdown, without caring whether the service shares the gateway's gRPC
// server or has one of its own.
type serviceRunner interface {
	// Ready returns a channel closed once the runner's services are
	// reachable through the gateway.
	Ready() <-chan struct{}
	// ServiceType returns the concrete service type used in diagnostics.
	ServiceType() string
	// Run runs the runner's work and returns once it is done or the context
	// is canceled. A runner without background work returns at once.
	Run(ctx context.Context) error
	// Close releases the hosted service's resources.
	Close() error
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
	cfg              Config
	server           *grpc.Server
	listener         net.Listener
	memoryListener   *bufconn.Listener
	runners          []serviceRunner
	registry         *BackendRegistry
	readinessTracker *readiness.Tracker
	log              *zap.Logger
}

// NewGateway creates a new Gateway API.
func NewGateway(cfg Config, options ...GatewayOption) (*Gateway, error) {
	opts := newGatewayOptions()
	for _, o := range options {
		o(opts)
	}
	log := opts.Log
	registry := NewBackendRegistry()

	// Every backend hosted inside this process is labeled with the address it
	// is reachable at from outside: the injected listener's, else the configured.
	endpoint := cfg.Server.Endpoint
	if opts.Listener != nil {
		endpoint = opts.Listener.Addr().String()
	}

	authManager, err := auth.NewManager(&cfg.Auth, auth.WithLog(log))
	if err != nil {
		return nil, fmt.Errorf("failed to create auth manager: %w", err)
	}

	// A client certificate is only ever verified in a TLS handshake, so an
	// authenticator that reads one is dead without server TLS.
	if authManager.ClientCAs() != nil && cfg.Server.TLS == nil {
		return nil, errors.New("x509 authenticator requires server.tls")
	}

	authService := NewAuthService(authManager)

	director := func(ctx context.Context, fullMethodName string) (proxy.Mode, []proxy.Backend, error) {
		service, _, err := xgrpc.ParseFullMethod(fullMethodName)
		if err != nil {
			return proxy.One2One, nil, status.Errorf(codes.NotFound, "malformed gRPC method name: %v", err)
		}

		backend, release, ok := registry.GetBackend(service)
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

		// The proxy library hands backend to its own machinery for the
		// whole call rather than returning it to this closure, so there is
		// no return-site here to release the lease at. ctx is the RPC's
		// own server-side stream context, which the proxy library cancels
		// once this call finishes, so tying the release to it releases the
		// lease exactly when the call is done, however it ends.
		context.AfterFunc(ctx, release)

		log.Debug("proxying request",
			zap.String("method", fullMethodName),
			zap.String("service", service),
		)

		return proxy.One2One, []proxy.Backend{backend}, nil
	}

	// The server is assigned below, after the metrics retention predicate and
	// service filter are built. Neither reads server before Serve is called:
	// the retention closure only reads server.GetServiceInfo() at Collect
	// time, and serviceKnown defers its own server.GetServiceInfo() read to
	// its first call, which an RPC can only trigger after Serve — by then
	// registration is complete. The forward reference is safe.
	var server *grpc.Server

	// The serviceKnown helper snapshots the gateway's own statically registered
	// gRPC services exactly once, on first use, rather than on every call.
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

	// The serviceFilter rejects any service the gateway does not know about,
	// stopping metric series and per-service method bookkeeping allocation
	// at the source for unauthenticated scans against random service names.
	// A registry backend lookup is mutex-guarded and cheap. The gateway's
	// own registered services are read from the cached serviceKnown snapshot.
	serviceFilter := func(service string) bool {
		if registry.HasBackend(service) {
			return true
		}
		_, ok := serviceKnown()[service]
		return ok
	}

	// Retention keeps a series iff its grpc_service is a registry backend
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
		grpc.MaxRecvMsgSize(maxRequestMessageBytes),
		grpc.MaxSendMsgSize(maxResponseMessageBytes),
		grpc.ForceServerCodecV2(proxy.Codec()),
		grpc.UnknownServiceHandler(
			proxy.TransparentHandler(director),
		),
	}
	if cfg.Server.TLS != nil {
		var clientCAs func() *x509.CertPool
		if authManager.ClientCAs() != nil {
			clientCAs = authManager.ClientCAs
		}

		creds, err := cfg.Server.TLS.ServerCredentials(clientCAs)
		if err != nil {
			return nil, fmt.Errorf("load gateway TLS: %w", err)
		}
		serverOpts = append(serverOpts, grpc.Creds(creds))
	}
	server = grpc.NewServer(serverOpts...)

	gatewayService := NewGatewayService(registry, WithGatewayServiceLog(log))
	ynpb.RegisterGatewayServer(server, gatewayService)

	ynpb.RegisterAuthServiceServer(server, authService)

	// The gateway scope is set once at startup and has no heartbeat source,
	// so it declares no expected freshness.
	rdTracker := readiness.NewTracker(
		[]readiness.ScopeSpec{{Name: gatewayReadinessScope}},
		readiness.WithDrainLatch(),
		readiness.WithLog(log),
	)
	readinessSvc := NewReadinessService(rdTracker)
	ynpb.RegisterReadinessServiceServer(server, readinessSvc)

	metricsService := NewMetricsService(append(metricsCollectors(serverMetrics, opts.Services), authManager)...)
	ynpb.RegisterMetricsServiceServer(server, metricsService)

	// Gateway-hosted services are reached through one in-memory connection
	// back into this server, a backend for the HTTP surface and the registry.
	//
	// No network hop is involved, and the interceptor chain still runs on every
	// request that comes through it. External backends (from the Register RPC)
	// each get their own network connection via RegisterBackend in the
	// registration loop.
	// The loopback completes the handshake the server credentials impose on
	// every listener, so under TLS it pins the gateway's own certificate.
	loopbackCreds := credentials.TransportCredentials(insecure.NewCredentials())
	if cfg.Server.TLS != nil {
		loopbackCreds, err = cfg.Server.TLS.LoopbackCredentials()
		if err != nil {
			return nil, fmt.Errorf("failed to build loopback TLS credentials: %w", err)
		}
	}

	memoryListener := newMemoryListener()
	loopback, err := dialMemoryBackend(endpoint, memoryListener, loopbackCreds)
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

	var runners []serviceRunner

	for _, entry := range opts.Services {
		switch entry.Kind {
		case BackendKindBuiltin:
			// Shared-server service: register on the gateway's gRPC server and
			// point every service name at the shared loopback backend.
			entry.Service.RegisterService(server)

			for _, name := range entry.Service.ServicesNames() {
				registry.RegisterBackend(name, loopback, entry.Kind)
				log.Info("registered service in registry",
					zap.String("service", name),
					zap.Stringer("kind", entry.Kind),
				)
			}

			runners = append(runners, newBuiltinServiceRunner(entry.Service))
		case BackendKindInProcess:
			runners = append(runners, NewInProcessServiceRunner(entry.Service, registry, endpoint, WithInProcessServiceRunnerLog(log)))
		case BackendKindExternal:
			return nil, fmt.Errorf("service %q is external and cannot be hosted inside the gateway", entry.Service.Name())
		default:
			return nil, fmt.Errorf("service %q has unknown backend kind %s", entry.Service.Name(), entry.Kind)
		}
	}

	return &Gateway{
		cfg:              cfg,
		server:           server,
		listener:         opts.Listener,
		memoryListener:   memoryListener,
		runners:          runners,
		registry:         registry,
		readinessTracker: rdTracker,
		log:              log,
	}, nil
}

// Close closes the gateway API.
func (m *Gateway) Close() error {
	var errs []error

	for _, runner := range m.runners {
		if err := runner.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close service %s: %w", runner.ServiceType(), err))
		}
	}

	if err := m.registry.Close(); err != nil {
		errs = append(errs, fmt.Errorf("failed to close backend registry: %w", err))
	}

	return errors.Join(errs...)
}

// pendingRunners returns the runners not ready at the time of the call.
//
// Taken before any runner starts, the result is exactly the in-process
// runners, since a framework runner is ready from construction.
func (m *Gateway) pendingRunners() []serviceRunner {
	var pending []serviceRunner
	for _, runner := range m.runners {
		select {
		case <-runner.Ready():
		default:
			pending = append(pending, runner)
		}
	}

	return pending
}

// Run runs the gateway API until the specified context is canceled.
func (m *Gateway) Run(ctx context.Context) error {
	m.log.Info("starting gRPC gateway")

	listener := m.listener
	if listener == nil {
		var err error
		listener, err = net.Listen("tcp", m.cfg.Server.Endpoint)
		if err != nil {
			return fmt.Errorf("failed to initialize gRPC listener: %w", err)
		}
	}

	m.log.Info("exposing gRPC gateway", zap.Stringer("addr", listener.Addr()))

	wg, ctx := errgroup.WithContext(ctx)

	wg.Go(func() error {
		return serve(m.server, listener)
	})
	wg.Go(func() error {
		return serve(m.server, m.memoryListener)
	})
	if m.cfg.Server.HTTPEndpoint != "" {
		wg.Go(func() error {
			return m.runHTTPServer(ctx)
		})
	}

	// The runners still registering are snapshotted before any runner
	// starts, so a runner that registers instantly still leaves readiness to
	// the waiting goroutine below and the log it emits never blocks this one.
	pending := m.pendingRunners()

	for _, runner := range m.runners {
		wg.Go(func() error {
			return runner.Run(ctx)
		})
	}

	wg.Go(func() error {
		return m.runRegistrySweeper(ctx)
	})

	// The readiness marker is published once every runner still registering
	// has finished, and immediately when none is. Both branches emit the
	// identical "all built-in modules ready" message, so a run publishes at
	// most one readiness marker: the waiting branch publishes none if the
	// context is canceled before every runner has registered.
	//
	// That message text is matched verbatim by an external observer to
	// decide the gateway is ready to accept module RPCs, so its wording is a
	// contract and must stay identical between the branches.
	if len(pending) > 0 {
		wg.Go(func() error {
			for _, runner := range pending {
				select {
				case <-ctx.Done():
					return nil
				case <-runner.Ready():
				}
			}
			m.log.Info("all built-in modules ready",
				zap.Int("count", len(pending)),
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
