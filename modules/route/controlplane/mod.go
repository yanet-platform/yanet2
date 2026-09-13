package route

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/common/go/grpcmetrics"
	cpffi "github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
)

const (
	moduleType = "route"
	agentName  = moduleType
	// fibObjectType is the shared-object type a config's table is
	// published under, linked by the module config of the same name.
	fibObjectType = "route_fib"
)

// Option configures the RouteModule constructor.
type Option func(*moduleOptions)

type moduleOptions struct {
	Log *zap.Logger
}

func newModuleOptions() *moduleOptions {
	return &moduleOptions{
		Log: zap.NewNop(),
	}
}

// WithLog sets the logger for the route module.
func WithLog(log *zap.Logger) Option {
	return func(o *moduleOptions) {
		o.Log = log
	}
}

// RouteModule is the slim route-module shim that owns shared memory and
// exposes the routepb.RouteService gRPC surface.
//
// The module no longer owns a RIB or a neighbour table. The
// yanet-route-operator agent rebuilds the FIB and pushes it via
// UpdateFIB.
type RouteModule struct {
	cfg            *Config
	attachment     *cpffi.Attachment
	service        *RouteService
	metricsService *MetricsService
}

// NewRouteModule creates a new RouteModule.
func NewRouteModule(cfg *Config, options ...Option) (*RouteModule, error) {
	opts := newModuleOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("module", "modules.route.controlplane.routepb.v1.RouteService"))

	attachment, err := cpffi.Attach(cfg.AttachConfig, agentName, log)
	if err != nil {
		return nil, err
	}
	agent := attachment.Agent

	serviceOptions := []RouteServiceOption{
		WithMetrics(grpcmetrics.NewFactory(
			grpcmetrics.WithLabeler(labeler),
		)),
	}
	if cfg.DisableNexthopCounters {
		serviceOptions = append(serviceOptions, WithNexthopCountersDisabled())
	}

	service := NewRouteService(NewBackend(agent), serviceOptions...)

	metricsService := NewMetricsService(service)

	return &RouteModule{
		cfg:            cfg,
		attachment:     attachment,
		service:        service,
		metricsService: metricsService,
	}, nil
}

// Name returns the module name.
func (m *RouteModule) Name() string {
	return moduleType
}

// Endpoint returns the gRPC endpoint for the route module shim.
func (m *RouteModule) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

// ServicesNames returns the gRPC service names exposed by the module.
func (m *RouteModule) ServicesNames() []string {
	return []string{
		"modules.route.controlplane.routepb.v1.RouteService",
		routepb.MetricsService_ServiceDesc.ServiceName,
	}
}

// RegisterService registers the route module's gRPC service.
func (m *RouteModule) RegisterService(server *grpc.Server) {
	routepb.RegisterRouteServiceServer(server, m.service)
	routepb.RegisterMetricsServiceServer(server, m.metricsService)
}

// UnaryServerInterceptors returns the gRPC unary interceptors for this
// module.
func (m *RouteModule) UnaryServerInterceptors() []grpc.UnaryServerInterceptor {
	interceptor := m.service.UnaryServerInterceptor()
	if interceptor == nil {
		return nil
	}

	return []grpc.UnaryServerInterceptor{interceptor}
}

// Close closes the module.
func (m *RouteModule) Close() error {
	return m.attachment.Close()
}
