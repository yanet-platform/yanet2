package operator

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/cenkalti/backoff/v5"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/controlplane/gateway"
)

type gatewayRegRunnerOptions struct {
	Interval time.Duration
	Log      *zap.Logger
}

func newGatewayRegRunnerOptions() *gatewayRegRunnerOptions {
	return &gatewayRegRunnerOptions{
		Interval: DefaultRegisterInterval,
		Log:      zap.NewNop(),
	}
}

// GatewayRegRunner periodically re-registers operator services in all
// configured gateways.
type GatewayRegRunner struct {
	gateways []GatewayConfig
	services []string
	interval time.Duration
	endpoint net.Addr
	log      *zap.Logger
}

// GatewayRegRunnerOption configures NewGatewayRegistrationRunner.
type GatewayRegRunnerOption func(*gatewayRegRunnerOptions)

// WithGatewayRegInterval sets the period between registration attempts.
func WithGatewayRegInterval(d time.Duration) GatewayRegRunnerOption {
	return func(o *gatewayRegRunnerOptions) {
		o.Interval = d
	}
}

// WithGatewayRegLog sets the logger for the registration runner.
func WithGatewayRegLog(log *zap.Logger) GatewayRegRunnerOption {
	return func(o *gatewayRegRunnerOptions) {
		o.Log = log
	}
}

// NewGatewayRegRunner creates a registration runner for all configured
// gateways.
func NewGatewayRegRunner(
	gateways []GatewayConfig,
	services []string,
	endpoint net.Addr,
	options ...GatewayRegRunnerOption,
) *GatewayRegRunner {
	opts := newGatewayRegRunnerOptions()
	for _, o := range options {
		o(opts)
	}

	return &GatewayRegRunner{
		gateways: gateways,
		services: services,
		interval: opts.Interval,
		endpoint: endpoint,
		log:      opts.Log,
	}
}

// Run heartbeats the given service set to every configured gateway.
func (m *GatewayRegRunner) Run(ctx context.Context) error {
	if len(m.gateways) == 0 {
		m.log.Warn("no gateways configured for operator registration",
			zap.Strings("services", m.services),
		)
		return nil
	}

	shortBackOff := func() backoff.BackOff {
		return backoff.NewExponentialBackOff()
	}

	wg, ctx := errgroup.WithContext(ctx)
	for _, cfg := range m.gateways {
		log := m.log.With(
			zap.String("gateway", cfg.Name),
			zap.String("gateway_endpoint", cfg.Endpoint.Unwrap()),
		)

		registrar, err := gateway.NewGatewayRegistrar(
			cfg.Endpoint.Unwrap(),
			nil,
			gateway.WithBackOff(shortBackOff),
			gateway.WithMaxElapsedTime(m.interval/2),
			gateway.WithLog(log),
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
				m.services,
				m.endpoint.String(),
				gateway.WithLoopInterval(m.interval),
				gateway.WithLoopLog(log),
			)
			return loop.Run(ctx)
		})
	}

	return wg.Wait()
}
