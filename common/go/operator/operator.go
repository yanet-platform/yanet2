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
// It always runs the optional PreRun hook, the reconciler, and any
// configured workers.
//
// The embedded gRPC server and gateway-registration loop are opt-in via
// WithGRPCServer and WithGateways respectively.
type Operator[T any] struct {
	server       *GRPCServer
	endpoint     string
	reconciler   *Reconciler[T]
	actuator     Actuator[T]
	preRun       PreRun
	workers      []Runner
	gateways     []GatewayConfig
	register     RegisterConfig
	serviceNames []string

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
		server       *GRPCServer
		endpoint     string
		serviceNames []string
	)

	if opts.GRPCServer != nil {
		server, serviceNames = NewGRPCServer(
			opts.GRPCServer.cfg,
			opts.GRPCServer.services,
			WithGRPCLog(log),
		)
		endpoint = opts.GRPCServer.cfg.Endpoint.Unwrap()
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
		server:       server,
		endpoint:     endpoint,
		reconciler:   reconciler,
		actuator:     actuator,
		preRun:       opts.PreRun,
		workers:      opts.Workers,
		gateways:     opts.Gateways,
		register:     opts.Register,
		serviceNames: serviceNames,
		log:          log,
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
