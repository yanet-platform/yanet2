package route_mpls

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/modulebase"
	"github.com/yanet-platform/yanet2/modules/route-mpls/controlplane/routemplspb/v1"
)

const (
	moduleType = "route-mpls"
	agentName  = moduleType
)

// Option configures the RouteMPLSModule constructor.
type Option = modulebase.Option

// WithLog sets the logger for the route-mpls module.
func WithLog(log *zap.Logger) Option {
	return modulebase.WithLog(log)
}

// RouteMPLSModule is the controlplane part of the route-mpls module that owns
// shared memory and exposes the routemplspb.RouteMPLSService gRPC surface.
type RouteMPLSModule struct {
	*modulebase.Base
}

// NewRouteMPLSModule creates a new RouteMPLSModule.
func NewRouteMPLSModule(cfg *Config, options ...Option) (*RouteMPLSModule, error) {
	base, err := modulebase.NewBase(cfg.AttachConfig, agentName, cfg.Endpoint.Unwrap(), options...)
	if err != nil {
		return nil, err
	}

	service := NewRouteMPLSService(NewBackend(base.Agent()), WithRouteMPLSServiceLog(base.Log()))
	base.AddService(routemplspb.RouteMPLSService_ServiceDesc.ServiceName, func(server *grpc.Server) {
		routemplspb.RegisterRouteMPLSServiceServer(server, service)
	})

	return &RouteMPLSModule{Base: base}, nil
}
