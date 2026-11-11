package operator

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/readiness"
	operatorpb "github.com/yanet-platform/yanet2/operators/pipeline/operatorpb/v1"
)

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

	service := NewService(WithServiceLog(log))

	tracker := readiness.NewTracker(readinessScopeSpecs(cfg),
		readiness.WithDrainLatch(),
		readiness.WithLog(log.With(zap.String("operator", "pipeline"))),
	)

	gatewayNames := make([]string, len(cfg.Gateways))
	for idx := range cfg.Gateways {
		gatewayNames[idx] = cfg.Gateways[idx].Name
	}
	source := NewStageQueueSource(
		gatewayNames,
		WithStageQueueMetrics(metrics),
		WithStageQueueLog(log),
	)

	gateways := make([]operator.Target[*StageConfig], 0, len(cfg.Gateways))
	for idx, gw := range cfg.Gateways {
		actuator, err := NewGatewayActuator(
			gw,
			WithGatewayActuatorMetrics(gatewayMetrics[idx]),
			WithGatewayActuatorStageObserver(source),
			WithGatewayActuatorLog(log),
		)
		if err != nil {
			for _, gateway := range gateways {
				_ = gateway.Actuator.Close()
			}
			return nil, fmt.Errorf("failed to construct gateway actuator %q: %w", gw.Name, err)
		}

		gateways = append(gateways, operator.Target[*StageConfig]{
			Name:     gw.Name,
			Actuator: actuator,
			Scope:    pipelineScope(gw.Name),
		})
	}

	stages := cfg.Stages
	services := []operator.ServiceRegistrar{
		func(s *grpc.Server) string {
			operatorpb.RegisterPipelineOperatorServiceServer(s, service)
			return operatorpb.PipelineOperatorService_ServiceDesc.ServiceName
		},
	}

	app := operator.NewOperator(
		gateways,
		source.Latest(),
		operator.WithGRPCServer(cfg.Server, services...),
		operator.WithReadiness("pipeline", tracker),
		operator.WithMetrics(operator.MetricsConfig{Name: "pipeline", Prefix: "pipeline_operator"}, metrics),
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

// pipelineScope names the readiness scope of the stages on a gateway.
func pipelineScope(gateway string) string {
	return "pipeline:" + gateway
}

// readinessScopeSpecs uses the reconcile cadence for gateways with stages.
// Gateways without stages have no freshness contract because they stay idle.
func readinessScopeSpecs(cfg *Config) []readiness.ScopeSpec {
	freshness := cfg.Reconcile.Interval.Unwrap()
	if len(cfg.Stages) == 0 {
		freshness = 0
	}
	specs := make([]readiness.ScopeSpec, len(cfg.Gateways))
	for idx, gw := range cfg.Gateways {
		specs[idx] = readiness.ScopeSpec{
			Name:                        pipelineScope(gw.Name),
			ExpectedObservationInterval: freshness,
		}
	}
	return specs
}
