package gateway

import (
	"context"
	"fmt"
	"io"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	commonxgrpc "github.com/yanet-platform/yanet2/common/go/xgrpc"
	"github.com/yanet-platform/yanet2/controlplane/internal/xgrpc"
)

// UnaryInterceptedService is implemented by services that contribute their own
// unary interceptors to the gRPC server the runner gives them.
//
// InProcessServiceRunner appends these after the framework access-log interceptor.
type UnaryInterceptedService interface {
	UnaryServerInterceptors() []grpc.UnaryServerInterceptor
}

// InProcessServiceRunner serves an in-process Service on its own gRPC server behind an
// in-memory listener and registers it with the gateway's registry directly.
//
// The service never touches the network: its server is reachable only
// through the connection the runner hands to the registry, so no transport
// security and no authentication apply on that hop. The gateway's own
// listeners stay the only entry points from outside.
type InProcessServiceRunner struct {
	module   Service
	registry *BackendRegistry
	endpoint string
	server   *grpc.Server
	ready    chan struct{}
	log      *zap.Logger
}

// InProcessServiceRunnerOption configures the InProcessServiceRunner constructor.
type InProcessServiceRunnerOption func(*inProcessServiceRunnerOptions)

type inProcessServiceRunnerOptions struct {
	Log *zap.Logger
}

func newInProcessServiceRunnerOptions() *inProcessServiceRunnerOptions {
	return &inProcessServiceRunnerOptions{
		Log: zap.NewNop(),
	}
}

// WithInProcessServiceRunnerLog sets the logger for the service runner.
func WithInProcessServiceRunnerLog(log *zap.Logger) InProcessServiceRunnerOption {
	return func(o *inProcessServiceRunnerOptions) {
		o.Log = log
	}
}

// NewInProcessServiceRunner creates a runner that registers module's services in
// registry under endpoint, the address the gateway itself serves.
//
// That address is where the services are reachable from outside. An endpoint
// the module carries for itself only applies when it runs in a separate
// process, so it is logged and ignored here.
func NewInProcessServiceRunner(
	module Service,
	registry *BackendRegistry,
	endpoint string,
	options ...InProcessServiceRunnerOption,
) *InProcessServiceRunner {
	opts := newInProcessServiceRunnerOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.Named(module.Name()).With(zap.String("module", module.Name()))

	if module.Endpoint() != "" {
		log.Info("service endpoint ignored, the service is served inside the gateway process",
			zap.String("endpoint", module.Endpoint()),
		)
	}

	interceptors := []grpc.UnaryServerInterceptor{xgrpc.AccessLogInterceptor(log)}
	if provider, ok := module.(UnaryInterceptedService); ok {
		interceptors = append(interceptors, provider.UnaryServerInterceptors()...)
	}

	return &InProcessServiceRunner{
		module:   module,
		registry: registry,
		endpoint: endpoint,
		server: grpc.NewServer(
			grpc.ChainUnaryInterceptor(interceptors...),
			grpc.MaxRecvMsgSize(1024*1024*256), grpc.MaxSendMsgSize(1024*1024*256),
		),
		ready: make(chan struct{}),
		log:   log,
	}
}

// Ready returns a channel closed exactly once, when the runner has registered
// its services and the module is reachable through the gateway.
func (m *InProcessServiceRunner) Ready() <-chan struct{} {
	return m.ready
}

// ServiceType returns the concrete service type used in runner diagnostics.
func (m *InProcessServiceRunner) ServiceType() string {
	return fmt.Sprintf("%T", m.module)
}

// Close closes the underlying service if it implements io.Closer.
func (m *InProcessServiceRunner) Close() error {
	if c, ok := m.module.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// Run runs the service until the context is canceled.
func (m *InProcessServiceRunner) Run(ctx context.Context) error {
	m.log.Info("starting in-process service", zap.String("service", m.ServiceType()))

	listener := newMemoryListener()

	m.module.RegisterService(m.server)

	wg, ctx := errgroup.WithContext(ctx)
	if bg, ok := m.module.(BackgroundService); ok {
		m.log.Info("running background jobs")

		wg.Go(func() error {
			return bg.Run(ctx)
		})
	}
	wg.Go(func() error {
		m.log.Info("exposing gRPC API in memory")
		return serve(m.server, listener)
	})

	if err := m.register(listener); err != nil {
		m.server.Stop()
		_ = wg.Wait()
		return fmt.Errorf("failed to register services: %w", err)
	}
	close(m.ready)

	<-ctx.Done()

	m.log.Info("stopping gRPC API")
	defer m.log.Info("stopped gRPC API")

	commonxgrpc.StopGracefully(m.server, commonxgrpc.GracefulStopTimeout, func() {
		m.log.Warn("graceful stop timed out, forcing shutdown",
			zap.Duration("grace_period", commonxgrpc.GracefulStopTimeout),
		)
	})

	return wg.Wait()
}

// register points every service name at one in-memory connection into the
// runner's server. The registry owns the connection from then on.
//
// A service exposing no gRPC services has nothing to register, so no
// connection is opened for it.
func (m *InProcessServiceRunner) register(listener *bufconn.Listener) error {
	names := m.module.ServicesNames()
	if len(names) == 0 {
		return nil
	}

	b, err := dialMemoryBackend(m.endpoint, listener, insecure.NewCredentials())
	if err != nil {
		return err
	}

	for _, name := range names {
		m.registry.RegisterBackend(name, b, BackendKindInProcess)
		m.log.Info("registered service in registry",
			zap.String("service", name),
			zap.Stringer("kind", BackendKindInProcess),
		)
	}

	return nil
}

// builtinServiceRunner runs a framework service registered on the gateway's
// own gRPC server.
//
// Such a service is reachable as soon as the gateway serves, so the runner is
// ready from construction and only runs the service's background job, if it
// has one.
type builtinServiceRunner struct {
	service Service
	ready   chan struct{}
}

func newBuiltinServiceRunner(service Service) *builtinServiceRunner {
	ready := make(chan struct{})
	close(ready)

	return &builtinServiceRunner{
		service: service,
		ready:   ready,
	}
}

// Ready returns a channel that is already closed.
func (m *builtinServiceRunner) Ready() <-chan struct{} {
	return m.ready
}

// ServiceType returns the concrete service type used in diagnostics.
func (m *builtinServiceRunner) ServiceType() string {
	return fmt.Sprintf("%T", m.service)
}

// Run runs the service's background job, returning at once for a service
// without one.
func (m *builtinServiceRunner) Run(ctx context.Context) error {
	if background, ok := m.service.(BackgroundService); ok {
		return background.Run(ctx)
	}

	return nil
}

// Close closes the service if it implements io.Closer.
func (m *builtinServiceRunner) Close() error {
	if closer, ok := m.service.(io.Closer); ok {
		return closer.Close()
	}

	return nil
}
