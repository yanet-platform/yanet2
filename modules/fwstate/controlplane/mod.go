package fwstate

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
	fwstatemap "github.com/yanet-platform/yanet2/objects/fwstate/controlplane"
)

const agentName = moduleType

// FWStateModule is a control-plane component of the firewall-state sync
// module.
//
// The module shares state with ACL solely through named fwstate-map
// objects: the map service publishes them into the instance-wide config
// generation, where module configs of any agent resolve them by name.
type FWStateModule struct {
	cfg                   *Config
	attachment            *ffi.Attachment
	fwstateService        *FWStateService
	fwstateMetricsService *MetricsService
	mapService            *fwstatemap.FWStateMapService
}

// NewFWStateModule creates a new fwstate module instance.
func NewFWStateModule(cfg *Config, options ...Option) (*FWStateModule, error) {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("module", moduleType))

	attachment, err := ffi.Attach(cfg.AttachConfig, agentName, log)
	if err != nil {
		return nil, err
	}
	agent := attachment.Agent

	// The map service owns the fwstate-map objects both this module's
	// configs and ACL configs link by name; a published object resolves
	// through the shared config generation regardless of the linking
	// module's agent.
	mapService := fwstatemap.NewFWStateMapService(
		agent,
		fwstatemap.WithLog(log),
		fwstatemap.WithMetrics(fwstatemap.NewMetricsFactory()),
	)

	fwstateService := NewFWStateService(
		agent,
		WithLog(log),
		WithMetrics(NewMetricsFactory()),
	)
	// The metrics endpoint aggregates the fwstate and map services, so
	// the gRPC metrics the map service's interceptor records are
	// reachable through the module's metrics RPC.
	fwstateMetricsService := NewMetricsService(fwstateService, mapService)

	return &FWStateModule{
		cfg:                   cfg,
		attachment:            attachment,
		fwstateService:        fwstateService,
		fwstateMetricsService: fwstateMetricsService,
		mapService:            mapService,
	}, nil
}

func (m *FWStateModule) Name() string {
	return moduleType
}

func (m *FWStateModule) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

func (m *FWStateModule) ServicesNames() []string {
	return []string{
		FWStateServiceName,
		FWStateMetricsServiceName,
		fwstatemap.ServiceName,
	}
}

func (m *FWStateModule) RegisterService(server *grpc.Server) {
	fwstatepb.RegisterFWStateServiceServer(server, m.fwstateService)
	fwstatepb.RegisterMetricsServiceServer(server, m.fwstateMetricsService)
	m.mapService.Register(server)
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

// Close closes the module.
func (m *FWStateModule) Close() error {
	return m.attachment.Close()
}
