package operator

import (
	"context"
	"fmt"
	"net"

	"github.com/cenkalti/backoff/v5"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/agents/yanet-pipeline-operator/operatorpb"
	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/controlplane/gateway"
)

var (
	serviceNames = []string{
		operatorpb.PipelineOperatorService_ServiceDesc.ServiceName,
		operatorpb.MetricsService_ServiceDesc.ServiceName,
	}
)

type Operator struct {
	cfg        *Config
	server     *GRPCServer
	reconciler *Reconciler
	actuator   Actuator
	log        *zap.Logger
}

func NewOperator(cfg *Config, options ...Option) (*Operator, error) {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log

	gatewayMetrics := make([]*GatewayMetrics, len(cfg.Gateways))
	for idx := range cfg.Gateways {
		gatewayMetrics[idx] = NewGatewayMetrics(cfg.Gateways[idx].Name)
	}
	metrics := NewMetrics(gatewayMetrics)

	server := NewGRPCServer(
		cfg.Server,
		NewService(WithServiceLog(log), WithServiceMetrics(metrics)),
		WithGRPCLog(log),
	)

	actuators := make([]Actuator, 0, len(cfg.Gateways))
	for idx, gw := range cfg.Gateways {
		actuator, err := NewGatewayActuator(
			gw,
			WithGatewayActuatorMetrics(gatewayMetrics[idx]),
			WithGatewayActuatorLog(log),
		)
		if err != nil {
			for _, a := range actuators {
				_ = a.Close()
			}
			return nil, fmt.Errorf("failed to construct gateway actuator %q: %w", gw.Name, err)
		}

		actuators = append(actuators, actuator)
	}

	actuator := operator.NewFanOutActuator(
		actuators,
		operator.WithFanOutLog(log),
	)

	reconciler := NewReconciler(
		actuator,
		WithReconcileInterval(
			cfg.Reconcile.Interval.Unwrap(),
		),
		WithReconcileBackoff(
			cfg.Reconcile.InitialBackoff.Unwrap(),
			cfg.Reconcile.MaxBackoff.Unwrap(),
		),
		WithReconcilerMetrics(metrics),
		WithReconcilerLog(log),
	)

	m := &Operator{
		cfg:        cfg,
		server:     server,
		reconciler: reconciler,
		actuator:   actuator,
		log:        log,
	}

	return m, nil
}

func (m *Operator) Close() error {
	return m.actuator.Close()
}

func (m *Operator) Run(ctx context.Context) error {
	if len(m.cfg.Stages) > 0 {
		queue := make([]*StageConfig, len(m.cfg.Stages))
		for idx := range m.cfg.Stages {
			queue[idx] = &m.cfg.Stages[idx]
		}
		m.reconciler.SetStages(queue)
	}

	wg, ctx := errgroup.WithContext(ctx)
	listener, err := net.Listen("tcp", m.cfg.Server.Endpoint.Unwrap())
	if err != nil {
		return fmt.Errorf("failed to listen gRPC operator endpoint %q: %w", m.cfg.Server.Endpoint.Unwrap(), err)
	}

	wg.Go(func() error {
		return m.server.Run(ctx, listener)
	})
	wg.Go(func() error {
		return m.runGatewayRegistration(ctx, listener.Addr())
	})
	wg.Go(func() error {
		return m.reconciler.Run(ctx)
	})

	return wg.Wait()
}

func (m *Operator) runGatewayRegistration(
	ctx context.Context,
	endpoint net.Addr,
) error {
	if len(m.cfg.Gateways) == 0 {
		m.log.Warn("no gateways configured for operator registration",
			zap.Strings("services", serviceNames),
		)
		return nil
	}

	interval := m.cfg.Register.Interval.Unwrap()
	shortBackOff := func() backoff.BackOff {
		return backoff.NewExponentialBackOff()
	}

	wg, ctx := errgroup.WithContext(ctx)
	for _, cfg := range m.cfg.Gateways {
		log := m.log.With(
			zap.String("gateway", cfg.Name),
			zap.String("gateway_endpoint", cfg.Endpoint.Unwrap()),
		)
		registrar, err := gateway.NewGatewayRegistrar(
			cfg.Endpoint.Unwrap(),
			nil,
			gateway.WithLog(log),
			gateway.WithBackOff(shortBackOff),
			gateway.WithMaxElapsedTime(interval/2),
		)
		if err != nil {
			return fmt.Errorf("failed to create gateway registrar for %q: %w", cfg.Name, err)
		}

		wg.Go(func() error {
			defer func() {
				if err := registrar.Close(); err != nil {
					log.Warn("failed to close gateway registrar", zap.Error(err))
				}
			}()
			loop := gateway.NewRegistrationLoop(
				registrar,
				serviceNames,
				endpoint.String(),
				gateway.WithLoopInterval(interval),
				gateway.WithLoopLog(log),
			)
			return loop.Run(ctx)
		})
	}

	return wg.Wait()
}
