package operator

import (
	"context"
	"fmt"
	"net"
	"slices"
	"sync"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/common/go/readiness"
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
// the first reconcile pass observes it.
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

type readinessOption struct {
	// Name scopes the readiness service the embedded server registers.
	Name string
	// Tracker is served by that service and drained on shutdown.
	Tracker *readiness.Tracker
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
	// Readiness is nil when the operator reports no readiness.
	Readiness *readinessOption
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

// WithReadiness reports the tracker as the operator's readiness.
//
// The embedded server, when enabled, serves the tracker under the given
// name, see ReadinessServiceName, in place of a readiness service registered
// by hand: a second registration under that name stops the process at
// startup. Every shutdown drains the tracker before the work stops, so one
// built with the drain latch keeps an apply cut off by the shutdown from
// overwriting the drain.
func WithReadiness(name string, tracker *readiness.Tracker) Option {
	return func(o *options) {
		o.Readiness = &readinessOption{
			Name:    name,
			Tracker: tracker,
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
// Useful for seeding the desired state from static configuration.
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
// The embedded gRPC server, gateway-registration loop and readiness are
// opt-in via WithGRPCServer, WithGateways and WithReadiness respectively.
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
	readiness         *readiness.Tracker

	log *zap.Logger
}

// NewOperator returns an operator that applies the published desired state
// through the actuator.
func NewOperator[T any](
	actuator Actuator[T],
	latest *Latest[T],
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
		tracker           *readiness.Tracker
	)

	if opts.Readiness != nil {
		tracker = opts.Readiness.Tracker
	}

	if opts.GRPCServer != nil {
		services := opts.GRPCServer.Services
		if opts.Readiness != nil {
			// Clipped, so the append never writes into the caller's slice.
			services = append(
				slices.Clip(services),
				NewReadinessServiceRegistrar(opts.Readiness.Name, tracker),
			)
		}

		server, serviceNames = NewGRPCServer(
			opts.GRPCServer.Config,
			services,
			WithGRPCLog(log),
		)
		endpoint = opts.GRPCServer.Config.Endpoint.Unwrap()
		advertiseEndpoint = opts.GRPCServer.Config.AdvertiseEndpoint
	}

	reconciler := NewReconciler(
		actuator,
		latest,
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
		readiness:         tracker,
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
// Run blocks until the supplied context is cancelled or any part fails.
// Either way the shutdown drains the readiness first, stops the reconciler,
// the workers and the registration next, and stops the server last, so its
// clients see the drain while it still answers.
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
	// The work and the server outlive the shared context, so that the
	// shutdown can stop them one after the other.
	workCtx, stopWork := context.WithCancel(context.WithoutCancel(ctx))
	defer stopWork()
	serveCtx, stopServing := context.WithCancel(context.WithoutCancel(ctx))
	defer stopServing()

	// Every runner shares the group, so the first failure starts the
	// shutdown at once and is the error returned. The counter only tells
	// the shutdown when the work has stopped.
	var work sync.WaitGroup
	for _, runner := range m.runners(listener) {
		work.Add(1)
		wg.Go(func() error {
			return runCounted(workCtx, runner, &work)
		})
	}
	if m.server != nil {
		wg.Go(func() error {
			return m.server.Run(serveCtx, listener)
		})
	}
	wg.Go(func() error {
		return m.stopInOrder(ctx, stopWork, &work, stopServing)
	})

	return wg.Wait()
}

// runners lists the reconciler, the workers and, when the server registers
// with gateways, the registration loop.
func (m *Operator[T]) runners(listener net.Listener) []Runner {
	runners := append([]Runner{m.reconciler.Run}, m.workers...)
	if m.server == nil || len(m.gateways) == 0 {
		return runners
	}

	advertiseEndpoint := m.advertiseEndpoint
	if advertiseEndpoint == "" {
		advertiseEndpoint = listener.Addr().String()
	}
	registration := NewGatewayRegRunner(
		m.gateways,
		m.serviceNames,
		advertiseEndpoint,
		WithGatewayRegInterval(m.register.Interval.Unwrap()),
		WithGatewayRegLog(m.log),
	)

	return append(runners, registration.Run)
}

// runCounted runs the runner and counts it out of the work once it returns.
func runCounted(ctx context.Context, runner Runner, work *sync.WaitGroup) error {
	defer work.Done()

	return runner(ctx)
}

// stopInOrder waits for the shutdown, drains the readiness, stops the work
// and waits for it, and only then stops the server.
func (m *Operator[T]) stopInOrder(
	ctx context.Context,
	stopWork context.CancelFunc,
	work *sync.WaitGroup,
	stopServing context.CancelFunc,
) error {
	<-ctx.Done()
	if m.readiness != nil {
		m.readiness.Drain()
	}
	stopWork()
	work.Wait()
	stopServing()

	return nil
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
