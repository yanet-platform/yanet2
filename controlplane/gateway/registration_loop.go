package gateway

import (
	"context"
	"time"

	"go.uber.org/zap"
)

const (
	defaultGatewayRegistrationInterval = 30 * time.Second
)

type loopOptions struct {
	Interval time.Duration
	Log      *zap.Logger
}

type LoopOption func(*loopOptions)

func newLoopOptions() *loopOptions {
	return &loopOptions{
		Interval: defaultGatewayRegistrationInterval,
		Log:      zap.NewNop(),
	}
}

// WithLoopLog sets the logger used by the registration loop.
func WithLoopLog(log *zap.Logger) LoopOption {
	return func(o *loopOptions) {
		o.Log = log
	}
}

// WithLoopInterval sets the period between successive registration attempts.
func WithLoopInterval(d time.Duration) LoopOption {
	return func(o *loopOptions) {
		o.Interval = d
	}
}

// RegistrationLoop refreshes gateway registration on a fixed interval until
// ctx is cancelled.
//
// Lifetime of the underlying GatewayRegistrar is owned by the caller.
type RegistrationLoop struct {
	registrar       *GatewayRegistrar
	services        []string
	backendEndpoint string
	interval        time.Duration
	log             *zap.Logger
}

// NewRegistrationLoop wraps an existing GatewayRegistrar with a fixed-interval
// re-registration loop.
func NewRegistrationLoop(
	registrar *GatewayRegistrar,
	services []string,
	backendEndpoint string,
	options ...LoopOption,
) *RegistrationLoop {
	opts := newLoopOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(
		zap.String("gateway", registrar.Endpoint()),
		zap.Strings("services", services),
	)

	return &RegistrationLoop{
		registrar:       registrar,
		services:        services,
		backendEndpoint: backendEndpoint,
		interval:        opts.Interval,
		log:             log,
	}
}

// Run repeatedly registers services with the gateway, waiting interval between
// attempts, until ctx is cancelled.
func (m *RegistrationLoop) Run(ctx context.Context) error {
	defer m.log.Info("stopped gateway registration heartbeat")

	for {
		if err := m.registrar.RegisterServices(ctx, m.services, m.backendEndpoint); err != nil {
			m.log.Warn("failed to register in gateway", zap.Error(err))
		}

		timer := time.NewTimer(m.interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
