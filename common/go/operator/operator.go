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

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
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

type metricsOption struct {
	// Config names the served families and their service.
	Config MetricsConfig
	// Collectors add the operator's own families.
	Collectors []MetricsCollector
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
	// Metrics is nil when the operator serves no metrics.
	Metrics *metricsOption
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
		PreRun: func(context.Context) error { return nil },
		Log:    zap.NewNop(),
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

// WithMetrics serves the operator's metrics under the configured name: the
// reconcile families, the apply families of every target, labelled with its
// name as the gateway, then the families of every collector.
//
// The embedded server, when enabled, serves them in place of a metrics
// service registered by hand: a second registration under that name stops
// the process at startup.
func WithMetrics(cfg MetricsConfig, collectors ...MetricsCollector) Option {
	return func(o *options) {
		o.Metrics = &metricsOption{
			Config:     cfg,
			Collectors: collectors,
		}
	}
}

// Operator is the generic operator skeleton.
//
// It always runs the optional PreRun hook, the reconciler, and any
// configured workers.
//
// The embedded gRPC server, gateway-registration loop, readiness and
// metrics are opt-in via WithGRPCServer, WithGateways, WithReadiness and
// WithMetrics respectively.
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
// to every target.
func NewOperator[T any](
	targets []Target[T],
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

	recorded := newOperatorMetrics(opts.Metrics, targets)
	appliers := make([]*targetApplier[T], len(targets))
	for idx, target := range targets {
		appliers[idx] = &targetApplier[T]{
			target:  target,
			metrics: recorded.Targets[idx],
			tracker: tracker,
		}
	}
	actuator := &fanOut[T]{appliers: appliers}

	if opts.GRPCServer != nil {
		// Clipped, so the appends never write into the caller's slice.
		services := slices.Clip(opts.GRPCServer.Services)
		if opts.Readiness != nil {
			services = append(services, NewReadinessServiceRegistrar(opts.Readiness.Name, tracker))
		}
		if opts.Metrics != nil {
			services = append(services, NewMetricsServiceRegistrar(opts.Metrics.Config.Name, recorded.Collectors...))
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
		WithReconcilerMetrics(recorded.Reconciler),
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

// operatorMetrics are the families an operator records about its own work.
type operatorMetrics struct {
	// Reconciler observes the reconcile loop.
	Reconciler ReconcilerMetricsObserver
	// Targets record the apply outcomes of every target, in target order.
	Targets []*applyMetrics
	// Collectors serve all of the above, then the operator's own families.
	Collectors []MetricsCollector
}

// newOperatorMetrics creates the families the option names, or unnamed
// ones that nothing serves when there is no option.
func newOperatorMetrics[T any](option *metricsOption, targets []Target[T]) operatorMetrics {
	recorded := operatorMetrics{
		Reconciler: NoopReconcilerMetricsObserver{},
		Targets:    make([]*applyMetrics, len(targets)),
	}

	config := MetricsConfig{}
	if option != nil {
		config = option.Config
		reconciler := NewReconcilerMetrics(config.Prefix, config.Labels...)
		recorded.Reconciler = reconciler
		recorded.Collectors = append(recorded.Collectors, reconciler)
	}
	for idx, target := range targets {
		labels := append(slices.Clip(config.Labels), commonpb.NewLabel("gateway", target.Name))
		recorded.Targets[idx] = newApplyMetrics(makeMetricName(config.Prefix, "gateway"), labels...)
		recorded.Collectors = append(recorded.Collectors, recorded.Targets[idx])
	}
	if option != nil {
		recorded.Collectors = append(recorded.Collectors, option.Collectors...)
	}

	return recorded
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
