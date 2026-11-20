package main

import (
	"context"
	"fmt"
	"net"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/common/go/operator"
	example "github.com/yanet-platform/yanet2/sdk/example/controlplane"
	examplepb "github.com/yanet-platform/yanet2/sdk/example/controlplane/examplepb/v1"
)

// daemon serves the module's gRPC service and heartbeats it into every
// configured gateway, mirroring what operator.NewOperator does for
// reconcile-loop operators minus the reconcile loop.
type daemon struct {
	server *operator.GRPCServer
	module *example.ExampleModule
	cfg    *Config
	log    *zap.Logger
}

// Option configures the daemon constructor.
type Option func(*daemonOptions)

type daemonOptions struct {
	Log *zap.Logger
}

func newDaemonOptions() *daemonOptions {
	return &daemonOptions{
		Log: zap.NewNop(),
	}
}

// WithLog sets the logger for the daemon.
func WithLog(log *zap.Logger) Option {
	return func(o *daemonOptions) {
		o.Log = log
	}
}

func newDaemon(
	cfg *Config,
	module *example.ExampleModule,
	options ...Option,
) (*daemon, error) {
	opts := newDaemonOptions()
	for _, o := range options {
		o(opts)
	}

	registrar := func(server *grpc.Server) string {
		module.RegisterService(server)
		return examplepb.ExampleService_ServiceDesc.ServiceName
	}
	server, _ := operator.NewGRPCServer(
		cfg.Server,
		[]operator.ServiceRegistrar{registrar},
		operator.WithGRPCLog(opts.Log),
	)

	return &daemon{
		server: server,
		module: module,
		cfg:    cfg,
		log:    opts.Log,
	}, nil
}

// Run serves the module endpoint and registers it with the gateways until
// the context is cancelled or any component fails.
func (m *daemon) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", m.cfg.Server.Endpoint.Unwrap())
	if err != nil {
		return fmt.Errorf(
			"failed to listen on module endpoint %q: %w",
			m.cfg.Server.Endpoint.Unwrap(), err,
		)
	}

	advertise := m.cfg.Server.AdvertiseEndpoint
	if advertise == "" {
		advertise = listener.Addr().String()
	}

	wg, ctx := errgroup.WithContext(ctx)

	wg.Go(func() error {
		return m.server.Run(ctx, listener)
	})

	if len(m.cfg.Gateways) > 0 {
		runner := operator.NewGatewayRegRunner(
			m.cfg.Gateways,
			m.module.ServicesNames(),
			advertise,
			operator.WithGatewayRegInterval(m.cfg.Register.Interval.Unwrap()),
			operator.WithGatewayRegLog(m.log),
		)
		wg.Go(func() error {
			return runner.Run(ctx)
		})
	}

	return wg.Wait()
}

// Close releases the module's shared-memory attachment.
func (m *daemon) Close() error {
	return m.module.Close()
}
