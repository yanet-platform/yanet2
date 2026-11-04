package operator

import (
	"context"
	"fmt"
	"time"

	"github.com/cenkalti/backoff/v5"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/go/xgrpc"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
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
	endpoint string
	log      *zap.Logger
}

// GatewayRegRunnerOption configures NewGatewayRegRunner.
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
// gateways, advertising the given endpoint verbatim to each of them.
func NewGatewayRegRunner(
	gateways []GatewayConfig,
	services []string,
	endpoint string,
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

	wg, ctx := errgroup.WithContext(ctx)
	for _, cfg := range m.gateways {
		log := m.log.With(
			zap.String("gateway", cfg.Name),
			zap.String("gateway_endpoint", cfg.Endpoint.Unwrap()),
		)

		conn, err := DialGateway(cfg, xgrpc.WithCertificateStoreLog(log))
		if err != nil {
			return err
		}

		heartbeat := &gatewayHeartbeat{
			client:          ynpb.NewGatewayClient(conn),
			gatewayEndpoint: cfg.Endpoint.Unwrap(),
			services:        m.services,
			backendEndpoint: m.endpoint,
			interval:        m.interval,
			log:             log,
		}

		wg.Go(func() error {
			defer func() {
				if err := conn.Close(); err != nil {
					log.Warn("failed to close gateway connection", zap.Error(err))
				}
			}()

			return heartbeat.Run(ctx)
		})
	}

	return wg.Wait()
}

// gatewayRegisterer is the part of the gateway API the heartbeat calls.
type gatewayRegisterer interface {
	Register(
		ctx context.Context,
		request *ynpb.RegisterRequest,
		options ...grpc.CallOption,
	) (*ynpb.RegisterResponse, error)
}

// gatewayHeartbeat keeps a service set registered in one gateway.
type gatewayHeartbeat struct {
	client          gatewayRegisterer
	gatewayEndpoint string
	services        []string
	backendEndpoint string
	interval        time.Duration
	log             *zap.Logger
}

// Run registers the services, waits interval and repeats until ctx is
// cancelled.
func (m *gatewayHeartbeat) Run(ctx context.Context) error {
	log := m.log.With(zap.Strings("services", m.services))
	defer log.Info("stopped gateway registration heartbeat")

	for {
		if err := m.registerServices(ctx); err != nil {
			log.Warn("failed to register in gateway", zap.Error(err))
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

// registerServices registers every service in the gateway concurrently,
// each at the advertised endpoint.
//
// A transient failure is retried with exponential backoff until the next
// retry would start past half the interval. A permanent rejection abandons
// the attempt at once.
func (m *gatewayHeartbeat) registerServices(ctx context.Context) error {
	// The backoff itself is left to each retry run, which starts its own:
	// backoff state must not be shared between the registrations below.
	var retryOptions []backoff.RetryOption
	if maxElapsedTime := m.interval / 2; maxElapsedTime > 0 {
		retryOptions = append(retryOptions, backoff.WithMaxElapsedTime(maxElapsedTime))
	}

	wg, ctx := errgroup.WithContext(ctx)
	for _, name := range m.services {
		request := &ynpb.RegisterRequest{
			Backend: &ynpb.BackendDesc{
				Name:     name,
				Endpoint: m.backendEndpoint,
			},
		}

		wg.Go(func() error {
			log := m.log.With(zap.String("service", name))

			_, err := backoff.Retry(ctx, func() (*ynpb.RegisterResponse, error) {
				response, err := m.client.Register(ctx, request)
				if err != nil {
					if isPermanentRegisterError(err) {
						log.Error("gateway rejected registration permanently, abandoning this attempt", zap.Error(err))
						return nil, backoff.Permanent(fmt.Errorf(
							"gateway %q permanently rejected registration of service %q: %w",
							m.gatewayEndpoint, name, err,
						))
					}
					log.Warn("failed to register in gateway", zap.Error(err))
					return nil, err
				}

				switch response.GetStatus() {
				case ynpb.RegistrationStatus_REGISTRATION_STATUS_REGISTERED:
					log.Info("registered in gateway")
				case ynpb.RegistrationStatus_REGISTRATION_STATUS_UPDATED:
					log.Info("updated registration in gateway")
				case ynpb.RegistrationStatus_REGISTRATION_STATUS_RENEWED:
					log.Debug("registration renewed in gateway")
				default:
					log.Info("registered in gateway")
				}
				return response, nil
			}, retryOptions...)

			return err
		})
	}

	return wg.Wait()
}

// isPermanentRegisterError reports whether err is a rejection that no retry
// within the same attempt can turn into success.
//
// A retry resends a byte-identical request under the same caller identity,
// so a rejection of the request's content or of that identity repeats on
// every retry, and only the next heartbeat cycle tries again. Everything
// else, including framework statuses such as Unimplemented and ambiguous
// ones such as Unknown and Internal, fails open toward retrying.
func isPermanentRegisterError(err error) bool {
	switch status.Code(err) {
	case codes.InvalidArgument,
		codes.Unauthenticated,
		codes.PermissionDenied:
		return true
	default:
		return false
	}
}
