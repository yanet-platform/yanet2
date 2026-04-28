package operator

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
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

	server := NewGRPCServer(
		cfg.Server,
		NewService(WithServiceLog(log)),
		WithGRPCLog(log),
	)

	actuators := make([]Actuator, 0, len(cfg.Gateways))
	for _, gw := range cfg.Gateways {
		actuator, err := NewGatewayActuator(
			gw,
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

	actuator := NewFanOutActuator(
		actuators,
		WithFanOutActuatorLog(log),
	)

	reconciler := NewReconciler(
		actuator,
		WithReconcilerLog(log),
		WithReconcileInterval(
			cfg.Reconcile.Interval.Unwrap(),
		),
		WithReconcileBackoff(
			cfg.Reconcile.InitialBackoff.Unwrap(),
			cfg.Reconcile.MaxBackoff.Unwrap(),
		),
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
	wg.Go(func() error {
		return m.server.Run(ctx)
	})
	wg.Go(func() error {
		return m.reconciler.Run(ctx)
	})

	return wg.Wait()
}
