package fwstate

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/modulebase"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
	fwstatemap "github.com/yanet-platform/yanet2/objects/fwstate/controlplane"
)

const agentName = moduleType

// ModuleOption configures the FWStateModule constructor.
type ModuleOption = modulebase.Option

// WithModuleLog sets the logger for the fwstate module.
func WithModuleLog(log *zap.Logger) ModuleOption {
	return modulebase.WithLog(log)
}

// FWStateModule is a control-plane component of the firewall-state sync
// module.
//
// The module shares state with ACL solely through named fwstate-map
// objects: the map service publishes them into the instance-wide config
// generation, where module configs of any agent resolve them by name.
type FWStateModule struct {
	*modulebase.Base

	cfg                   *Config
	fwstateService        *FWStateService
	fwstateMetricsService *MetricsService
	mapService            *fwstatemap.FWStateMapService
}

// NewFWStateModule creates a new fwstate module instance.
func NewFWStateModule(cfg *Config, options ...ModuleOption) (*FWStateModule, error) {
	base, err := modulebase.NewBase(cfg.AttachConfig, agentName, cfg.Endpoint.Unwrap(), options...)
	if err != nil {
		return nil, err
	}

	// The map service owns the fwstate-map objects both this module's
	// configs and ACL configs link by name; a published object resolves
	// through the shared config generation regardless of the linking
	// module's agent.
	mapService := fwstatemap.NewFWStateMapService(
		base.Agent(),
		fwstatemap.WithLog(base.Log()),
		fwstatemap.WithMetrics(fwstatemap.NewMetricsFactory()),
	)

	fwstateService := NewFWStateService(
		base.Agent(),
		WithLog(base.Log()),
		WithMetrics(NewMetricsFactory()),
	)
	// The metrics endpoint aggregates the fwstate and map services, so
	// the gRPC metrics the map service's interceptor records are
	// reachable through the module's metrics RPC.
	fwstateMetricsService := NewMetricsService(fwstateService, mapService)

	base.AddService(FWStateServiceName, func(server *grpc.Server) {
		fwstatepb.RegisterFWStateServiceServer(server, fwstateService)
	})
	base.AddService(FWStateMetricsServiceName, func(server *grpc.Server) {
		fwstatepb.RegisterMetricsServiceServer(server, fwstateMetricsService)
	})
	base.AddService(fwstatemap.ServiceName, func(server *grpc.Server) {
		mapService.Register(server)
	})

	return &FWStateModule{
		Base:                  base,
		cfg:                   cfg,
		fwstateService:        fwstateService,
		fwstateMetricsService: fwstateMetricsService,
		mapService:            mapService,
	}, nil
}

// UnaryServerInterceptors returns the gRPC unary interceptors for this module.
func (m *FWStateModule) UnaryServerInterceptors() []grpc.UnaryServerInterceptor {
	var interceptors []grpc.UnaryServerInterceptor
	if si := m.fwstateService.UnaryServerInterceptor(); si != nil {
		interceptors = append(interceptors, si)
	}
	if si := m.mapService.UnaryServerInterceptor(); si != nil {
		interceptors = append(interceptors, si)
	}
	return interceptors
}

// Run reclaims stale map layers until the context is cancelled.
//
// The sweep publishes config generations through the module's agent, so it
// belongs to the phase that ends before anything is closed rather than to
// the module's own construction: a module whose ownership never transfers
// leaves nothing running behind it.
func (m *FWStateModule) Run(ctx context.Context) error {
	m.mapService.RunStaleLayerSweeper(ctx, m.cfg.StaleLayerSweepInterval)
	return nil
}
