package operator

import (
	"context"
	"fmt"
	"io"
	"net"

	"github.com/cenkalti/backoff/v5"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/yanet-platform/yanet2/agents/yanet-pipeline-operator/operatorpb"
	"github.com/yanet-platform/yanet2/controlplane/ynpb"
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

	actuator := NewFanOutActuator(
		actuators,
		WithFanOutActuatorLog(log),
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

	if err := m.registerInGateways(ctx, listener.Addr()); err != nil {
		return fmt.Errorf("failed to register operator in gateways: %w", err)
	}

	wg.Go(func() error {
		return m.server.Run(ctx, listener)
	})
	wg.Go(func() error {
		return m.reconciler.Run(ctx)
	})

	return wg.Wait()
}

func (m *Operator) registerInGateways(ctx context.Context, endpoint net.Addr) error {
	if len(m.cfg.Gateways) == 0 {
		m.log.Warn("no gateways configured for operator registration",
			zap.Strings("services", serviceNames),
		)
		return nil
	}

	wg, ctx := errgroup.WithContext(ctx)
	for _, cfg := range m.cfg.Gateways {
		wg.Go(func() error {
			return m.registerInGateway(ctx, cfg, endpoint)
		})
	}

	return wg.Wait()
}

func (m *Operator) registerInGateway(ctx context.Context, cfg GatewayConfig, endpoint net.Addr) error {
	log := m.log.With(
		zap.String("gateway", cfg.Name),
		zap.String("gateway_endpoint", cfg.Endpoint.Unwrap()),
	)
	log.Info("registering services in gateway", zap.Any("services", serviceNames))

	client, conn, err := newGatewayClient(cfg.Endpoint.Unwrap())
	if err != nil {
		return err
	}
	defer conn.Close()

	wg, ctx := errgroup.WithContext(ctx)
	for _, serviceName := range serviceNames {
		wg.Go(func() error {
			return registerServiceInGateway(ctx, client, cfg.Name, serviceName, endpoint, log)
		})
	}

	return wg.Wait()
}

func newGatewayClient(endpoint string) (ynpb.GatewayClient, io.Closer, error) {
	conn, err := grpc.NewClient(
		endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"failed to create gRPC client for gateway %q: %w",
			endpoint,
			err,
		)
	}

	return ynpb.NewGatewayClient(conn), conn, nil
}

func registerServiceInGateway(
	ctx context.Context,
	client ynpb.GatewayClient,
	gatewayName string,
	serviceName string,
	endpoint net.Addr,
	log *zap.Logger,
) error {
	request := &ynpb.RegisterRequest{
		Name:     serviceName,
		Endpoint: endpoint.String(),
	}

	log = log.With(zap.String("service", serviceName))
	_, err := backoff.Retry(ctx, func() (*ynpb.RegisterResponse, error) {
		resp, err := client.Register(ctx, request)
		if err != nil {
			log.Warn("failed to register operator service in gateway", zap.Error(err))
			return nil, err
		}

		log.Info("successfully registered operator service in gateway")
		return resp, nil
	}, backoff.WithBackOff(backoff.NewExponentialBackOff()))
	if err != nil {
		return fmt.Errorf("failed to register service %q in gateway %q: %w", serviceName, gatewayName, err)
	}

	return nil
}
