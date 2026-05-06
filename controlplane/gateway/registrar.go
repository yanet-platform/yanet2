package gateway

import (
	"context"
	"fmt"
	"time"

	"github.com/cenkalti/backoff/v5"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding/gzip"

	"github.com/yanet-platform/yanet2/controlplane/ynpb"
)

type gatewayRegistrarOptions struct {
	backoff        func() backoff.BackOff
	maxElapsedTime time.Duration
	log            *zap.Logger
}

type GatewayRegistrarOption func(*gatewayRegistrarOptions)

func newGatewayRegistrarOptions() *gatewayRegistrarOptions {
	return &gatewayRegistrarOptions{
		backoff: func() backoff.BackOff { return backoff.NewExponentialBackOff() },
		log:     zap.NewNop(),
	}
}

// WithLog sets the logger for the GatewayRegistrar.
func WithLog(log *zap.Logger) GatewayRegistrarOption {
	return func(o *gatewayRegistrarOptions) {
		o.log = log
	}
}

// WithBackOff overrides the backoff strategy used when retrying per-service
// Register RPCs.
//
// The factory is invoked once per service registration attempt inside
// RegisterServices, because backoff.BackOff instances are stateful and must
// not be shared across concurrent retries.
func WithBackOff(factory func() backoff.BackOff) GatewayRegistrarOption {
	return func(o *gatewayRegistrarOptions) {
		o.backoff = factory
	}
}

// WithMaxElapsedTime caps the total time RegisterServices will spend retrying
// a single service.
//
// A zero value (the default) leaves the cap up to the underlying retry
// implementation.
func WithMaxElapsedTime(d time.Duration) GatewayRegistrarOption {
	return func(o *gatewayRegistrarOptions) {
		o.maxElapsedTime = d
	}
}

// GatewayRegistrar registers service backends in a single gateway endpoint.
//
// A single GatewayRegistrar instance is tied to exactly one endpoint.
type GatewayRegistrar struct {
	endpoint       string
	client         ynpb.GatewayClient
	conn           *grpc.ClientConn
	backoff        func() backoff.BackOff
	maxElapsedTime time.Duration
	log            *zap.Logger
}

// NewGatewayRegistrar creates a registrar for the given gateway endpoint.
func NewGatewayRegistrar(
	endpoint string,
	tlsConfig *TLSConfig,
	options ...GatewayRegistrarOption,
) (*GatewayRegistrar, error) {
	opts := newGatewayRegistrarOptions()
	for _, option := range options {
		option(opts)
	}

	creds, err := TransportCredentials(tlsConfig, endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize gateway transport credentials: %w", err)
	}

	conn, err := grpc.NewClient(
		endpoint,
		grpc.WithTransportCredentials(creds),
		grpc.WithDefaultCallOptions(grpc.UseCompressor(gzip.Name)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC client to gateway %q: %w", endpoint, err)
	}

	return &GatewayRegistrar{
		endpoint:       endpoint,
		client:         ynpb.NewGatewayClient(conn),
		conn:           conn,
		backoff:        opts.backoff,
		maxElapsedTime: opts.maxElapsedTime,
		log:            opts.log,
	}, nil
}

// Close closes the underlying gRPC client connection.
func (m *GatewayRegistrar) Close() error {
	return m.conn.Close()
}

// Endpoint returns the gateway endpoint this registrar is bound to.
func (m *GatewayRegistrar) Endpoint() string {
	return m.endpoint
}

// RegisterServices registers services with the same backend endpoint.
func (m *GatewayRegistrar) RegisterServices(
	ctx context.Context,
	services []string,
	backendEndpoint string,
) error {
	wg, ctx := errgroup.WithContext(ctx)

	for _, name := range services {
		request := &ynpb.RegisterRequest{
			Backend: &ynpb.BackendDesc{
				Name:     name,
				Endpoint: backendEndpoint,
			},
		}

		wg.Go(func() error {
			retryOpts := []backoff.RetryOption{
				backoff.WithBackOff(m.backoff()),
			}
			if m.maxElapsedTime > 0 {
				retryOpts = append(retryOpts, backoff.WithMaxElapsedTime(m.maxElapsedTime))
			}

			_, err := backoff.Retry(ctx, func() (*ynpb.RegisterResponse, error) {
				resp, err := m.client.Register(ctx, request)
				if err != nil {
					m.log.Warn("failed to register in gateway",
						zap.String("service", name),
						zap.String("gateway", m.endpoint),
						zap.Error(err),
					)
					return nil, err
				}

				m.log.Info("successfully registered in gateway",
					zap.String("service", name),
					zap.String("gateway", m.endpoint),
				)
				return resp, nil
			}, retryOpts...)

			return err
		})
	}

	return wg.Wait()
}
