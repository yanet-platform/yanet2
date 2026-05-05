package operator

import (
	"context"
	"fmt"
	"net"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/agents/yanet-pipeline-operator/operatorpb"
	"github.com/yanet-platform/yanet2/common/go/operator"
)

var (
	serviceNames = []string{
		operatorpb.PipelineOperatorService_ServiceDesc.ServiceName,
		operatorpb.MetricsService_ServiceDesc.ServiceName,
	}
)

type Operator struct {
	cfg        *Config
	server     *operator.GRPCServer
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

	service := NewService(WithServiceLog(log), WithServiceMetrics(metrics))
	server := operator.NewGRPCServer(
		cfg.Server,
		[]func(*grpc.Server){
			func(s *grpc.Server) { operatorpb.RegisterPipelineOperatorServiceServer(s, service) },
			func(s *grpc.Server) { operatorpb.RegisterMetricsServiceServer(s, service) },
		},
		operator.WithGRPCLog(log),
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
		runner := operator.NewGatewayRegRunner(
			m.cfg.Gateways,
			serviceNames,
			listener.Addr(),
			operator.WithGatewayRegInterval(m.cfg.Register.Interval.Unwrap()),
			operator.WithGatewayRegLog(m.log),
		)
		return runner.Run(ctx)
	})
	wg.Go(func() error {
		return m.reconciler.Run(ctx)
	})

	return wg.Wait()
}
