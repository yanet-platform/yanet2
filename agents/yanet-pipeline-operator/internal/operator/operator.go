package operator

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/agents/yanet-pipeline-operator/operatorpb"
	"github.com/yanet-platform/yanet2/common/go/operator"
)

// Actuator applies a desired stage configuration.
type Actuator = operator.Actuator[*StageConfig]

// Operator is the pipeline operator's thin wrapper around the generic
// operator framework.
type Operator struct {
	app *operator.Operator[*StageConfig]
}

// NewOperator constructs a pipeline operator from the supplied config.
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

	service := NewService(
		WithServiceMetrics(metrics),
		WithServiceLog(log),
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

	fanOut := operator.NewFanOutActuator(
		actuators,
		operator.WithFanOutLog(log),
	)

	source := NewStageQueueSource(
		WithStageQueueMetrics(metrics),
		WithStageQueueLog(log),
	)

	stages := cfg.Stages
	services := []operator.ServiceRegistrar{
		func(s *grpc.Server) string {
			operatorpb.RegisterPipelineOperatorServiceServer(s, service)
			return operatorpb.PipelineOperatorService_ServiceDesc.ServiceName
		},
		func(s *grpc.Server) string {
			operatorpb.RegisterMetricsServiceServer(s, service)
			return operatorpb.MetricsService_ServiceDesc.ServiceName
		},
	}

	app := operator.NewOperator(
		cfg.Server,
		fanOut,
		source,
		services,
		operator.WithReconcile(cfg.Reconcile),
		operator.WithGateways(cfg.Register, cfg.Gateways...),
		operator.WithPreRun(func(ctx context.Context) error {
			if len(stages) == 0 {
				return nil
			}

			queue := make([]*StageConfig, len(stages))
			for idx := range stages {
				queue[idx] = &stages[idx]
			}
			source.SetStages(queue)

			return nil
		}),
		operator.WithMetrics(metrics),
		operator.WithLog(log),
	)

	return &Operator{
		app: app,
	}, nil
}

// Close releases resources owned by the operator.
func (m *Operator) Close() error {
	return m.app.Close()
}

// Run drives the operator until the supplied context is cancelled.
func (m *Operator) Run(ctx context.Context) error {
	return m.app.Run(ctx)
}
