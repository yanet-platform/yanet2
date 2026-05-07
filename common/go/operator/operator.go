package operator

import (
	"context"
	"fmt"
	"net"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
)

// Runner is a long-running goroutine driven by the operator's errgroup.
//
// A non-nil return cancels the errgroup and tears the operator down.
type Runner func(ctx context.Context) error

// PreRun is an optional one-shot hook executed before the operator
// starts its goroutines.
//
// It runs after the gRPC listener is bound but before the gRPC server,
// gateway-registration loop, reconciler and workers start.
//
// Operators use it to seed in-memory state from static configuration so
// the first reconcile pass observes a populated source.
type PreRun func(ctx context.Context) error

// ServiceRegistrar registers a gRPC service on server and returns its
// fully-qualified service name.
//
// The name is used for gateway registration heartbeats.
type ServiceRegistrar func(server *grpc.Server) string

// Operator is the generic operator skeleton.
//
// It owns the gRPC server, the gateway-registration loop and the
// reconciler.
// Per-operator code retains ownership of the actuator and any worker-internal
// state.
type Operator[T any] struct {
	server       *GRPCServer
	listener     net.Listener
	reconciler   *Reconciler[T]
	actuator     Actuator[T]
	preRun       PreRun
	workers      []Runner
	gateways     []GatewayConfig
	register     RegisterConfig
	serviceNames []string
	endpoint     string

	log *zap.Logger
}

func NewOperator[T any](
	serverConfig *GRPCServerConfig,
	actuator Actuator[T],
	source StateSource[T],
	services []ServiceRegistrar,
	options ...Option,
) *Operator[T] {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log

	server, serviceNames := NewGRPCServer(
		serverConfig,
		services,
		WithGRPCLog(log),
	)

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
		server:       server,
		reconciler:   reconciler,
		actuator:     actuator,
		preRun:       opts.PreRun,
		workers:      opts.Workers,
		gateways:     opts.Gateways,
		register:     opts.Register,
		serviceNames: serviceNames,
		endpoint:     serverConfig.Endpoint.Unwrap(),
		log:          log,
	}
}

// Close releases resources owned by the Operator.
func (m *Operator[T]) Close() error {
	return m.actuator.Close()
}

// Run binds the gRPC listener, runs the optional PreRun hook, then
// runs the gRPC server, gateway-registration loop, reconciler and
// every Worker in an errgroup.
//
// Run blocks until the supplied context is cancelled or any goroutine
// returns a non-nil error.
func (m *Operator[T]) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", m.endpoint)
	if err != nil {
		return fmt.Errorf("failed to listen gRPC operator endpoint %q: %w", m.endpoint, err)
	}

	if err := m.preRun(ctx); err != nil {
		_ = listener.Close()
		return fmt.Errorf("failed to run pre-run hook: %w", err)
	}

	wg, ctx := errgroup.WithContext(ctx)
	wg.Go(func() error {
		return m.server.Run(ctx, listener)
	})
	wg.Go(func() error {
		runner := NewGatewayRegRunner(
			m.gateways,
			m.serviceNames,
			listener.Addr(),
			WithGatewayRegInterval(m.register.Interval.Unwrap()),
			WithGatewayRegLog(m.log),
		)
		return runner.Run(ctx)
	})
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
