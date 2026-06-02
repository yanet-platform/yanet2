package operator

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/common/go/operator"
	operatorpb "github.com/yanet-platform/yanet2/operators/forward/operatorpb/v1"
)

// Operator is the forward operator's thin wrapper around the generic
// operator framework.
type Operator struct {
	cfg *Config
	app *operator.Operator[State]
	log *zap.Logger
}

// NewOperator constructs an Operator from the supplied configuration.
func NewOperator(cfg *Config, options ...Option) (*Operator, error) {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log

	modules := make([]ModuleConfig, 0, len(cfg.Functions))
	for _, fn := range cfg.Functions {
		rules, err := LoadForwardRules(fn.RulesFile.Unwrap())
		if err != nil {
			return nil, fmt.Errorf("failed to load rules file %q: %w", fn.RulesFile.Unwrap(), err)
		}
		modules = append(modules, ModuleConfig{
			Name:  fn.Module.Unwrap(),
			Rules: rules,
		})
	}

	// One config:<gateway> scope per gateway, covering all module configs pushed there.
	scopeNames := make([]string, len(cfg.Gateways))
	for idx, gw := range cfg.Gateways {
		scopeNames[idx] = fmt.Sprintf("config:%s", gw.Name)
	}
	tracker := operator.NewReadiness(scopeNames,
		operator.WithReadinessLog(log.With(zap.String("operator", "forward"))),
	)

	actuators := make([]operator.Actuator[State], 0, len(cfg.Gateways))
	for _, gw := range cfg.Gateways {
		actuator, err := NewGatewayActuator(gw, cfg.Functions, WithGatewayActuatorLog(log))
		if err != nil {
			for _, a := range actuators {
				_ = a.Close()
			}
			return nil, fmt.Errorf("failed to construct gateway actuator %q: %w", gw.Name, err)
		}
		observed := operator.NewObservedActuator(actuator, fmt.Sprintf("config:%s", gw.Name), tracker.Observe)
		actuators = append(actuators, observed)
	}

	fanOut := operator.NewFanOutActuator(actuators, operator.WithFanOutLog(log))
	source := NewStaticSource(modules, WithSourceLog(log))

	svc := NewReadinessService(tracker)
	registrar := func(s *grpc.Server) string {
		operatorpb.RegisterReadinessServiceServer(s, svc)
		return operatorpb.ReadinessService_ServiceDesc.ServiceName
	}

	app := operator.NewOperator(
		fanOut,
		source,
		operator.WithGRPCServer(cfg.Server, registrar),
		operator.WithGateways(cfg.Register, cfg.Gateways...),
		operator.WithWorkers(func(ctx context.Context) error {
			<-ctx.Done()
			tracker.Drain()
			return nil
		}),
		operator.WithLog(log),
		operator.WithReconcile(cfg.Reconcile),
	)

	return &Operator{
		cfg: cfg,
		app: app,
		log: log,
	}, nil
}

// Run drives the operator until the supplied context is cancelled.
func (m *Operator) Run(ctx context.Context) error {
	return m.app.Run(ctx)
}

// Close releases resources owned by the operator.
func (m *Operator) Close() error {
	return m.app.Close()
}
