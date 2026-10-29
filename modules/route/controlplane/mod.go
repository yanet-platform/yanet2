package route

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/common/go/grpcmetrics"
	"github.com/yanet-platform/yanet2/controlplane/modulebase"
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
type Option = modulebase.Option

// WithLog sets the logger for the route module.
func WithLog(log *zap.Logger) Option {
	return modulebase.WithLog(log)
}

// RouteModule is the slim route-module shim that owns shared memory and
// exposes the routepb.RouteService gRPC surface.
//
// The module no longer owns a RIB or a neighbour table. The
// yanet-route-operator agent rebuilds the FIB and pushes it via
// UpdateFIB.
type RouteModule struct {
	*modulebase.Base

	service *RouteService
}

// NewRouteModule creates a new RouteModule.
func NewRouteModule(cfg *Config, options ...Option) (*RouteModule, error) {
	base, err := modulebase.NewBase(cfg.AttachConfig, agentName, cfg.Endpoint.Unwrap(), options...)
	if err != nil {
		return nil, err
	}

	serviceOptions := []RouteServiceOption{
		WithMetrics(grpcmetrics.NewFactory(
			grpcmetrics.WithLabeler(labeler),
		)),
	}
	if cfg.DisableNexthopCounters {
		serviceOptions = append(serviceOptions, WithNexthopCountersDisabled())
	}

	service := NewRouteService(NewBackend(base.Agent()), serviceOptions...)
	metricsService := NewMetricsService(service)

	base.AddService(routepb.RouteService_ServiceDesc.ServiceName, func(server *grpc.Server) {
		routepb.RegisterRouteServiceServer(server, service)
	})
	base.AddService(routepb.MetricsService_ServiceDesc.ServiceName, func(server *grpc.Server) {
		routepb.RegisterMetricsServiceServer(server, metricsService)
	})

	return &RouteModule{Base: base, service: service}, nil
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
