package gateway

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/siderolabs/grpc-proxy/proxy"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
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
	Services    []serviceEntry
	Log         *zap.Logger
	LogLevel    *zap.AtomicLevel
	Listener    net.Listener
	TLSListener net.Listener
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
// of binding the gRPC endpoint the config names.
//
// It stands in for server.grpc.endpoint and serves plaintext, or for the
// deprecated server.endpoint, which serves TLS while the deprecated
// server.tls is set. Run serves this listener directly and never calls
// net.Listen for it. The server closes it when it stops, and closing it
// again is a harmless net.ErrClosed — close it yourself only if Run never
// serves it.
func WithListener(listener net.Listener) GatewayOption {
	return func(o *gatewayOptions) {
		o.Listener = listener
	}
}

// WithTLSListener hands the Gateway an already-open listener to serve gRPC
// over TLS on instead of binding server.grpc.tls.endpoint.
//
// It requires the server.grpc.tls section, whose certificate it serves.
// Run serves this listener directly and never calls net.Listen for it. The
// server closes it when it stops, and closing it again is a harmless
// net.ErrClosed — close it yourself only if Run never serves it.
func WithTLSListener(listener net.Listener) GatewayOption {
	return func(o *gatewayOptions) {
		o.TLSListener = listener
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
	cfg Config
	// server is the plaintext gRPC server, which also serves the in-memory
	// loopback.
	server *grpc.Server
	// secureServer is the TLS gRPC server, nil without a gRPC TLS listener.
	secureServer  *grpc.Server
	grpcListeners []grpcListener
	http          ListenerConfig
	// httpsCertificates is the store the HTTPS listener serves from, nil
	// without one.
	httpsCertificates *commonxgrpc.CertificateStore
	memoryListener    *bufconn.Listener
	runners           []serviceRunner
	registry          *BackendRegistry
	readinessTracker  *readiness.Tracker
	log               *zap.Logger
}

// grpcListener is a network listener of one of the gateway's gRPC servers.
type grpcListener struct {
	// Scheme is how the listener is logged: grpcs for TLS, grpc otherwise.
	Scheme string
	// Server is the gRPC server the listener feeds.
	Server *grpc.Server
	// Listener is the injected listener, nil when Run binds the endpoint.
	Listener net.Listener
	// Endpoint is the configured address of the listener.
	Endpoint string
}

// NewGateway creates a new Gateway API.
func NewGateway(cfg Config, options ...GatewayOption) (*Gateway, error) {
	opts := newGatewayOptions()
	for _, o := range options {
		o(opts)
	}
	log := opts.Log

	plan := newListenerPlan(&cfg.Server)
	if len(plan.Mapped) > 0 || len(plan.Ignored) > 0 {
		log.Warn("deprecated gateway server keys, use server.grpc and server.http instead",
			zap.Strings("mapped", plan.Mapped),
			zap.Strings("ignored", plan.Ignored),
			zap.String("issue", deprecatedKeysIssue),
		)
	}

	// An injected TLS listener stands in for the endpoint of the gRPC TLS
	// section and serves that section's certificate.
	if opts.TLSListener != nil && (cfg.Server.GRPC == nil || cfg.Server.GRPC.TLS == nil) {
		return nil, errors.New("TLS listener requires server.grpc.tls")
	}

	// The deprecated gRPC endpoint serves TLS while the deprecated TLS
	// material is set, and so does a listener injected in its place.
	listener, tlsListener := opts.Listener, opts.TLSListener
	if cfg.Server.GRPC == nil && cfg.Server.TLS != nil {
		listener, tlsListener = nil, opts.Listener
	}

	plaintext := listener != nil || plan.GRPC.Endpoint != ""
	if !plaintext && plan.GRPC.TLS == nil {
		return nil, errors.New("server.grpc needs an endpoint or a tls listener")
	}

	// Only the gRPC TLS server bounds connection age, so an age given to
	// HTTPS would silently do nothing.
	if plan.HTTP.TLS != nil && plan.HTTP.TLS.MaxConnectionAge != 0 {
		return nil, errors.New("server.http.tls.max_connection_age is not supported, only server.grpc.tls bounds connection age")
	}
	if plan.GRPC.TLS != nil {
		switch age := plan.GRPC.TLS.MaxConnectionAge; {
		case age < 0:
			return nil, fmt.Errorf("server.grpc.tls.max_connection_age (%s) must not be negative", age)
		case age > 0 && age < minConnectionAge:
			return nil, fmt.Errorf("server.grpc.tls.max_connection_age (%s) must be at least %s", age, minConnectionAge)
		}
	}

	// Every TLS listener serves from a store that picks up renewed files
	// without a restart. Building a store loads its files, so an unreadable
	// or mismatched pair fails startup here, before any authenticator starts
	// its background refresh.
	var grpcsCertificates, httpsCertificates *commonxgrpc.CertificateStore
	var served servedCertificates
	if plan.GRPC.TLS != nil {
		store, err := newCertificateStore(
			&plan.GRPC.TLS.TLSConfig,
			commonxgrpc.WithCertificateStoreLog(log.With(zap.String("listener", "grpcs"))),
		)
		if err != nil {
			return nil, fmt.Errorf("load gateway TLS: %w", err)
		}
		grpcsCertificates = store
		served = append(served, listenerCertificates{Listener: "grpcs", Store: store})
	}
	if plan.HTTP.TLS != nil {
		store, err := newCertificateStore(
			&plan.HTTP.TLS.TLSConfig,
			commonxgrpc.WithCertificateStoreLog(log.With(zap.String("listener", "https"))),
		)
		if err != nil {
			return nil, fmt.Errorf("load gateway HTTPS: %w", err)
		}
		httpsCertificates = store
		served = append(served, listenerCertificates{Listener: "https", Store: store})
	}

	registry := NewBackendRegistry()

	authManager, err := auth.NewManager(&cfg.Auth, auth.WithLog(log))
	if err != nil {
		return nil, fmt.Errorf("failed to create auth manager: %w", err)
	}

	// A client certificate is only ever verified in a TLS handshake, so an
	// authenticator that reads one is dead without a gRPC TLS listener.
	if authManager.ClientCAs() != nil && plan.GRPC.TLS == nil {
		return nil, errors.New("x509 authenticator requires server.tls or server.grpc.tls")
	}

	// A connection keeps the client certificate of its handshake, so
	// without a bounded age a renewed one reaches the gateway only when its
	// client reconnects.
	if authManager.ClientCAs() != nil && plan.GRPC.TLS.MaxConnectionAge == 0 {
		log.Warn("gRPC TLS connection age is unbounded, clients keep their certificate until they reconnect",
			zap.String("key", "server.grpc.tls.max_connection_age"),
		)
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
			commonxgrpc.ResponseCompressionUnaryInterceptor(),
			serverMetrics.UnaryServerInterceptor(),
			auth.UnaryServerInterceptor(authManager, log),
			xgrpc.AccessLogInterceptor(log),
			commonxgrpc.ValidateUnaryInterceptor(),
		),
		grpc.ChainStreamInterceptor(
			commonxgrpc.ResponseCompressionStreamInterceptor(),
			serverMetrics.StreamServerInterceptor(),
			auth.StreamServerInterceptor(authManager, log),
			xgrpc.StreamAccessLogInterceptor(log),
			commonxgrpc.ValidateStreamInterceptor(),
		),
		grpc.MaxRecvMsgSize(1024 * 1024 * 256),
		grpc.MaxSendMsgSize(1024 * 1024 * 256),
		grpc.ForceServerCodecV2(proxy.Codec()),
		grpc.UnknownServiceHandler(
			proxy.TransparentHandler(director),
		),
	}

	gatewayService := NewGatewayService(registry, WithGatewayServiceLog(log))

	// The gateway scope is set once at startup and has no heartbeat source,
	// so it declares no expected freshness.
	rdTracker := readiness.NewTracker(
		[]readiness.ScopeSpec{{Name: gatewayReadinessScope}},
		readiness.WithDrainLatch(),
		readiness.WithLog(log),
	)
	readinessSvc := NewReadinessService(rdTracker)
	metricsService := NewMetricsService(append(metricsCollectors(serverMetrics, opts.Services), authManager, served)...)
	portMetricsService := NewPortMetricsService(portMetricsCollectors(opts.Services)...)

	var builtins []Service
	for _, entry := range opts.Services {
		if entry.Kind == BackendKindBuiltin {
			builtins = append(builtins, entry.Service)
		}
	}

	// Both gRPC servers share the options, the interceptor instances and
	// the services, and differ only in transport credentials.
	newServer := func(options ...grpc.ServerOption) *grpc.Server {
		server := grpc.NewServer(slices.Concat(serverOpts, options)...)
		ynpb.RegisterGatewayServer(server, gatewayService)
		ynpb.RegisterAuthServiceServer(server, authService)
		ynpb.RegisterReadinessServiceServer(server, readinessSvc)
		ynpb.RegisterMetricsServiceServer(server, metricsService)
		ynpb.RegisterPortMetricsServiceServer(server, portMetricsService)
		for _, service := range builtins {
			service.RegisterService(server)
		}

		return server
	}

	// The plaintext server runs even without a plaintext gRPC listener: it
	// serves the in-memory loopback.
	server = newServer()

	var grpcListeners []grpcListener
	if plaintext {
		grpcListeners = append(grpcListeners, grpcListener{
			Scheme:   "grpc",
			Server:   server,
			Listener: listener,
			Endpoint: plan.GRPC.Endpoint,
		})
	}

	// Client certificates are asked for on the TLS server alone.
	var secureServer *grpc.Server
	if plan.GRPC.TLS != nil {
		var clientCAs func() *x509.CertPool
		if authManager.ClientCAs() != nil {
			clientCAs = authManager.ClientCAs
		}

		secureOptions := []grpc.ServerOption{grpc.Creds(serverCredentials(grpcsCertificates, clientCAs))}
		// A bounded age makes every client reconnect and present its current
		// certificate. The grace stays unlimited, so calls in flight run to
		// completion.
		if age := plan.GRPC.TLS.MaxConnectionAge; age > 0 {
			secureOptions = append(secureOptions, grpc.KeepaliveParams(keepalive.ServerParameters{
				MaxConnectionAge: age,
			}))
		}
		secureServer = newServer(secureOptions...)
		grpcListeners = append(grpcListeners, grpcListener{
			Scheme:   "grpcs",
			Server:   secureServer,
			Listener: tlsListener,
			Endpoint: plan.GRPC.TLS.Endpoint.Unwrap(),
		})
	}

	// Every backend hosted inside this process is labeled with the address it
	// is reachable at from outside: the plaintext gRPC listener's, else the
	// TLS one's, injected or configured.
	endpoint := grpcListeners[0].Endpoint
	if grpcListeners[0].Listener != nil {
		endpoint = grpcListeners[0].Listener.Addr().String()
	}

	// Gateway-hosted services are reached through one in-memory connection
	// back into the plaintext server, a backend for the HTTP surface and the
	// registry.
	//
	// No network hop is involved, and the interceptor chain still runs on every
	// request that comes through it. External backends (from the Register RPC)
	// each get their own network connection via RegisterBackend in the
	// registration loop. A connection that never leaves the process runs no
	// TLS.
	memoryListener := newMemoryListener()
	loopback, err := dialMemoryBackend(endpoint, memoryListener, insecure.NewCredentials())
	if err != nil {
		return nil, fmt.Errorf("failed to create loopback backend for gateway-hosted services: %w", err)
	}

	for _, service := range []string{
		ynpb.Gateway_ServiceDesc.ServiceName,
		ynpb.AuthService_ServiceDesc.ServiceName,
		ynpb.ReadinessService_ServiceDesc.ServiceName,
		ynpb.MetricsService_ServiceDesc.ServiceName,
		ynpb.PortMetricsService_ServiceDesc.ServiceName,
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
			// Shared-server service: registered on the gateway's gRPC servers
			// above, every service name points at the shared loopback backend.
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
		cfg:               cfg,
		server:            server,
		secureServer:      secureServer,
		grpcListeners:     grpcListeners,
		http:              plan.HTTP,
		httpsCertificates: httpsCertificates,
		memoryListener:    memoryListener,
		runners:           runners,
		registry:          registry,
		readinessTracker:  rdTracker,
		log:               log,
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

	// Every gRPC listener is bound before any is served.
	listeners := slices.Clone(m.grpcListeners)
	for idx := range listeners {
		if listeners[idx].Listener != nil {
			continue
		}

		listener, err := net.Listen("tcp", listeners[idx].Endpoint)
		if err != nil {
			return fmt.Errorf("failed to initialize gRPC listener: %w", err)
		}
		listeners[idx].Listener = listener
	}

	wg, ctx := errgroup.WithContext(ctx)

	for _, entry := range listeners {
		m.log.Info("exposing gRPC gateway",
			zap.String("scheme", entry.Scheme),
			zap.Stringer("addr", entry.Listener.Addr()),
		)
		wg.Go(func() error {
			return serve(entry.Server, entry.Listener)
		})
	}
	wg.Go(func() error {
		return serve(m.server, m.memoryListener)
	})
	if m.http.Endpoint != "" || m.http.TLS != nil {
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

	for _, entry := range listeners {
		m.log.Info("stopping gRPC gateway",
			zap.String("scheme", entry.Scheme),
			zap.Stringer("addr", entry.Listener.Addr()),
		)
	}

	// The servers stop in parallel, so a shutdown waits out one grace period
	// rather than one per server.
	var stops errgroup.Group
	for _, server := range []*grpc.Server{m.server, m.secureServer} {
		if server == nil {
			continue
		}

		stops.Go(func() error {
			commonxgrpc.StopGracefully(server, commonxgrpc.GracefulStopTimeout, func() {
				m.log.Warn("graceful stop timed out, forcing shutdown",
					zap.Duration("grace_period", commonxgrpc.GracefulStopTimeout),
				)
			})
			return nil
		})
	}
	_ = stops.Wait()

	err := wg.Wait()
	for _, entry := range listeners {
		m.log.Info("stopped gRPC gateway",
			zap.String("scheme", entry.Scheme),
			zap.Stringer("addr", entry.Listener.Addr()),
		)
	}

	return err
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
			// Keep the monotonic reading for TTL comparisons.
			before := time.Now().Add(-ttl)
			for _, entry := range m.registry.EvictStale(before) {
				m.log.Info("evicted stale service from registry",
					zap.String("service", entry.Service()),
					zap.String("endpoint", entry.Endpoint()),
					zap.Time("last_seen_at", entry.LastSeenAt().UTC()),
					zap.Stringer("kind", entry.Kind()),
				)
			}
		}
	}
}

// runHTTPServer runs the HTTP server that provides access to gRPC services
// via HTTP, one server on its plaintext and TLS listeners alike.
//
// A listener that fails to bind or to serve ends the run with its error.
func (m *Gateway) runHTTPServer(ctx context.Context) error {
	server := &http.Server{
		Handler: httpproxy.GzipMiddleware(
			httpproxy.NewHTTPHandler(
				m.registry,
				httpproxy.WithLog(m.log),
			),
		),
	}

	type httpListener struct {
		Scheme   string
		Listener net.Listener
		Serve    func(listener net.Listener) error
	}

	var listeners []httpListener
	if endpoint := m.http.Endpoint; endpoint != "" {
		listener, err := net.Listen("tcp", endpoint)
		if err != nil {
			return fmt.Errorf("failed to serve: %w", err)
		}
		listeners = append(listeners, httpListener{Scheme: "http", Listener: listener, Serve: server.Serve})
	}
	if config := m.http.TLS; config != nil {
		listener, err := net.Listen("tcp", config.Endpoint.Unwrap())
		if err != nil {
			return fmt.Errorf("failed to serve: %w", err)
		}
		// The TLS config leaves client authentication at its default of
		// none, so HTTPS never asks for a client certificate. It names h2, so
		// HTTP/2 is set up whether this listener or the plaintext one starts
		// serving first.
		server.TLSConfig = &tls.Config{
			GetCertificate: m.httpsCertificates.GetCertificate,
			MinVersion:     tls.VersionTLS12,
			NextProtos:     []string{"h2", "http/1.1"},
		}
		serveTLS := func(listener net.Listener) error {
			return server.ServeTLS(listener, "", "")
		}
		listeners = append(listeners, httpListener{Scheme: "https", Listener: listener, Serve: serveTLS})
	}

	group, ctx := errgroup.WithContext(ctx)

	// Set up graceful shutdown.
	group.Go(func() error {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		for _, entry := range listeners {
			m.log.Info("shutting down HTTP server",
				zap.String("scheme", entry.Scheme),
				zap.Stringer("addr", entry.Listener.Addr()),
			)
		}
		if err := server.Shutdown(shutdownCtx); err != nil {
			m.log.Warn("failed to shut down HTTP server", zap.Error(err))
		}
		return nil
	})

	for _, entry := range listeners {
		m.log.Info("exposing HTTP <-> gRPC gateway",
			zap.String("scheme", entry.Scheme),
			zap.Stringer("addr", entry.Listener.Addr()),
		)
		group.Go(func() error {
			if err := entry.Serve(entry.Listener); err != http.ErrServerClosed {
				return fmt.Errorf("failed to serve: %w", err)
			}
			return nil
		})
	}

	return group.Wait()
}
