package route_mpls

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	cpffi "github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/route-mpls/controlplane/routemplspb/v1"
)

const (
	moduleType = "route-mpls"
	agentName  = moduleType
)

// Option configures the RouteMPLSModule constructor.
type Option func(*moduleOptions)

type moduleOptions struct {
	Log *zap.Logger
}

func newModuleOptions() *moduleOptions {
	return &moduleOptions{
		Log: zap.NewNop(),
	}
}

// WithLog sets the logger for the route-mpls module.
func WithLog(log *zap.Logger) Option {
	return func(o *moduleOptions) {
		o.Log = log
	}
}

// RouteMPLSModule is the controlplane part of the route-mpls module that owns
// shared memory and exposes the routemplspb.RouteMPLSService gRPC surface.
type RouteMPLSModule struct {
	cfg        *Config
	attachment *cpffi.Attachment
	service    *RouteMPLSService
}

// NewRouteMPLSModule creates a new RouteMPLSModule.
func NewRouteMPLSModule(cfg *Config, options ...Option) (*RouteMPLSModule, error) {
	opts := newModuleOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("module", "modules.route_mpls.controlplane.routemplspb.v1.RouteMPLSService"))

	attachment, err := cpffi.Attach(cfg.AttachConfig, agentName, log)
	if err != nil {
		return nil, err
	}
	agent := attachment.Agent

	service := NewRouteMPLSService(NewBackend(agent), WithRouteMPLSServiceLog(log))

	return &RouteMPLSModule{
		cfg:        cfg,
		attachment: attachment,
		service:    service,
	}, nil
}

// Name returns the module name.
func (m *RouteMPLSModule) Name() string {
	return moduleType
}

// Endpoint returns the gRPC endpoint for the route-mpls module.
func (m *RouteMPLSModule) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

// ServicesNames returns the gRPC service names exposed by the module.
func (m *RouteMPLSModule) ServicesNames() []string {
	return []string{
		"modules.route_mpls.controlplane.routemplspb.v1.RouteMPLSService",
	}
}

// RegisterService registers the route-mpls module's gRPC service.
func (m *RouteMPLSModule) RegisterService(server *grpc.Server) {
	routemplspb.RegisterRouteMPLSServiceServer(server, m.service)
}

// Close closes the module.
func (m *RouteMPLSModule) Close() error {
	return m.attachment.Close()
}
