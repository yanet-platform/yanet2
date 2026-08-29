package operator

import (
	"context"
	"fmt"
	"net"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

// Runner is a long-running goroutine driven by the operator's errgroup.
//
// A non-nil return cancels the errgroup and tears the operator down.
type Runner func(ctx context.Context) error

// PreRun is an optional one-shot hook executed before the operator
// starts its goroutines.
//
// Operators use it to seed in-memory state from static configuration so
// the first reconcile pass observes a populated source.
type PreRun func(ctx context.Context) error

// ServiceRegistrar registers a gRPC service on server and returns its
// fully-qualified service name.
//
// The name is used for gateway registration heartbeats.
type ServiceRegistrar func(server *grpc.Server) string

type grpcServerOption struct {
	// Config controls where the embedded server listens and advertises itself.
	Config GRPCServerConfig
	// Services are registered with the embedded server at construction.
	Services []ServiceRegistrar
}

type options struct {
	// Reconcile supplies timing defaults consumed by the reconcile loop.
	Reconcile ReconcileConfig
	// Register supplies the heartbeat interval for gateway registration.
	Register RegisterConfig
	// Gateways receive registration heartbeats when the server is enabled.
	Gateways []GatewayConfig
	// Workers run alongside the reconciler in the shared error group.
	Workers []Runner
	// PreRun initializes state before concurrent work starts.
	PreRun PreRun
	// Metrics receives reconcile-loop lifecycle events.
	Metrics ReconcilerMetricsObserver
	// GRPCServer is nil when the embedded server is disabled.
	GRPCServer *grpcServerOption
	// Log is the fallback logger for operator components.
	Log *zap.Logger
}

func newOptions() *options {
	return &options{
		Reconcile: ReconcileConfig{
			Interval:       xcfg.MustNonZero(DefaultReconcileInterval),
			InitialBackoff: xcfg.MustNonZero(DefaultReconcileInitialBackoff),
			MaxBackoff:     xcfg.MustNonZero(DefaultReconcileMaxBackoff),
		},
		PreRun:  func(context.Context) error { return nil },
		Metrics: NoopReconcilerMetricsObserver{},
		Log:     zap.NewNop(),
	}
}

// Option configures the Operator.
type Option func(*options)

// WithLog sets the logger used by the Operator and its sub-components
// when no more specific logger is supplied.
func WithLog(log *zap.Logger) Option {
	return func(o *options) {
		o.Log = log
	}
}

// WithReconcile sets the reconcile-loop timing parameters.
//
// Zero-valued fields set the corresponding default value.
func WithReconcile(cfg ReconcileConfig) Option {
	return func(o *options) {
		if cfg.Interval.Unwrap() > 0 {
			o.Reconcile.Interval = cfg.Interval
		}
		if cfg.InitialBackoff.Unwrap() > 0 {
			o.Reconcile.InitialBackoff = cfg.InitialBackoff
		}
		if cfg.MaxBackoff.Unwrap() > 0 {
			o.Reconcile.MaxBackoff = cfg.MaxBackoff
		}
	}
}

// WithGateways enables gateway registration against the supplied gateways.
//
// An empty gateway list leaves registration disabled. Registration also
// requires an embedded gRPC server.
func WithGateways(register RegisterConfig, gateways ...GatewayConfig) Option {
	return func(o *options) {
		o.Register = register
		o.Gateways = gateways
	}
}

// WithGRPCServer enables an embedded gRPC server on the configured endpoint.
//
// The server exposes the supplied services independently of gateway
// registration, so operators may serve metrics or APIs without registering.
// When this option is not supplied, the operator does not bind a listener.
func WithGRPCServer(cfg GRPCServerConfig, services ...ServiceRegistrar) Option {
	return func(o *options) {
		o.GRPCServer = &grpcServerOption{
			Config:   cfg,
			Services: services,
		}
	}
}

// WithWorkers attaches additional long-running goroutines to the
// operator's errgroup.
//
// Returning a non-nil error from any worker tears down the operator.
func WithWorkers(workers ...Runner) Option {
	return func(o *options) {
		o.Workers = workers
	}
}

// WithPreRun registers a hook executed once before any goroutine starts.
//
// Useful for seeding the source from static configuration.
func WithPreRun(fn PreRun) Option {
	return func(o *options) {
		o.PreRun = fn
	}
}

// WithMetrics attaches the metrics observer for the reconcile loop.
func WithMetrics(observer ReconcilerMetricsObserver) Option {
	return func(o *options) {
		o.Metrics = observer
	}
}

// Operator is the generic operator skeleton.
//
// It always runs the optional PreRun hook, the reconciler, and any
// configured workers.
//
// The embedded gRPC server and gateway-registration loop are opt-in via
// WithGRPCServer and WithGateways respectively.
type Operator[T any] struct {
	server            *GRPCServer
	endpoint          string
	advertiseEndpoint string
	reconciler        *Reconciler[T]
	actuator          Actuator[T]
	preRun            PreRun
	workers           []Runner
	gateways          []GatewayConfig
	register          RegisterConfig
	serviceNames      []string

	log *zap.Logger
}

func NewOperator[T any](
	actuator Actuator[T],
	source StateSource[T],
	options ...Option,
) *Operator[T] {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log

	var (
		server            *GRPCServer
		endpoint          string
		advertiseEndpoint string
		serviceNames      []string
	)

	if opts.GRPCServer != nil {
		server, serviceNames = NewGRPCServer(
			opts.GRPCServer.Config,
			opts.GRPCServer.Services,
			WithGRPCLog(log),
		)
		endpoint = opts.GRPCServer.Config.Endpoint.Unwrap()
		advertiseEndpoint = opts.GRPCServer.Config.AdvertiseEndpoint
	}

	reconciler := NewReconciler(
		actuator,
		source,
		WithReconcileInterval(opts.Reconcile.Interval.Unwrap()),
		WithReconcileBackoff(
			opts.Reconcile.InitialBackoff.Unwrap(),
			opts.Reconcile.MaxBackoff.Unwrap(),
		),
		WithReconcilerMetrics(opts.Metrics),
		WithReconcilerLog(log),
	)

	return &Operator[T]{
		server:            server,
		endpoint:          endpoint,
		advertiseEndpoint: advertiseEndpoint,
		reconciler:        reconciler,
		actuator:          actuator,
		preRun:            opts.PreRun,
		workers:           opts.Workers,
		gateways:          opts.Gateways,
		register:          opts.Register,
		serviceNames:      serviceNames,
		log:               log,
	}
}

// Close releases resources owned by the Operator.
func (m *Operator[T]) Close() error {
	return m.actuator.Close()
}

// Run runs the optional PreRun hook, then runs the reconciler and any
// configured workers in an errgroup.
//
// When WithGRPCServer was supplied, it binds the listener and runs the gRPC
// server. The gateway-registration loop runs only when WithGateways supplied
// a non-empty gateway list.
//
// Run blocks until the supplied context is cancelled or any goroutine
// returns a non-nil error.
func (m *Operator[T]) Run(ctx context.Context) error {
	if len(m.gateways) > 0 && m.server == nil {
		m.log.Warn(
			"gateways configured but gRPC server is disabled; registration loop will not run",
		)
	}

	listener, err := m.makeListener()
	if err != nil {
		return fmt.Errorf("failed to listen gRPC operator endpoint %q: %w", m.endpoint, err)
	}

	if err := m.preRun(ctx); err != nil {
		_ = listener.Close()
		return fmt.Errorf("failed to run pre-run hook: %w", err)
	}

	wg, ctx := errgroup.WithContext(ctx)

	if m.server != nil {
		wg.Go(func() error {
			return m.server.Run(ctx, listener)
		})

		if len(m.gateways) > 0 {
			advertiseEndpoint := m.advertiseEndpoint
			if advertiseEndpoint == "" {
				advertiseEndpoint = listener.Addr().String()
			}

			wg.Go(func() error {
				runner := NewGatewayRegRunner(
					m.gateways,
					m.serviceNames,
					advertiseEndpoint,
					WithGatewayRegInterval(m.register.Interval.Unwrap()),
					WithGatewayRegLog(m.log),
				)
				return runner.Run(ctx)
			})
		}
	}

	wg.Go(func() error {
		return m.reconciler.Run(ctx)
	})
	for _, worker := range m.workers {
		wg.Go(func() error {
			return worker(ctx)
		})
	}

	return wg.Wait()
}

func (m *Operator[T]) makeListener() (net.Listener, error) {
	if m.server == nil {
		return &noopListener{}, nil
	}

	return net.Listen("tcp", m.endpoint)
}

type noopListener struct{}

func (m *noopListener) Accept() (net.Conn, error) {
	return nil, net.ErrClosed
}

func (m *noopListener) Close() error {
	return nil
}

func (m *noopListener) Addr() net.Addr {
	return nil
}
