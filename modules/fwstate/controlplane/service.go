package fwstate

import (
	"context"
	"sync"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/go/grpcmetrics"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
)

// Option configures an FWStateService.
type Option func(*options)

// options holds the optional parameters for FWStateService construction.
type options struct {
	Metrics grpcmetrics.Factory
	Log     *zap.Logger
}

func newOptions() *options {
	return &options{
		Log: zap.NewNop(),
	}
}

// WithLog sets the service logger.
func WithLog(log *zap.Logger) Option {
	return func(o *options) {
		o.Log = log
	}
}

// WithMetrics enables collection of gRPC call metrics using the supplied
// factory.
//
// The factory owns label extraction and bucketing. The service injects its own
// retention provider at construction time. Use [NewMetricsFactory] to build a
// factory scoped to this module's services.
func WithMetrics(factory grpcmetrics.Factory) Option {
	return func(o *options) {
		o.Metrics = factory
	}
}

// NewMetricsFactory returns a [grpcmetrics.Factory] pre-bound to this module's
// own labeler and service filter, applying any extra options (e.g. custom
// histogram buckets) supplied by the caller.
//
// The [grpcmetrics.Retention] is injected by the service itself at construction
// time, so it must not be passed here.
func NewMetricsFactory(extra ...grpcmetrics.Option) grpcmetrics.Factory {
	opts := make([]grpcmetrics.Option, 0, len(extra)+2)
	opts = append(opts, grpcmetrics.WithLabeler(labeler))

	// Scope the collector to this module's own services.
	opts = append(opts, grpcmetrics.WithServiceFilter(
		func(service string) bool {
			return service == FWStateServiceName ||
				service == FWStateMetricsServiceName ||
				service == FWStateMapServiceName
		},
	))
	opts = append(opts, extra...)
	return grpcmetrics.NewFactory(opts...)
}

const (
	// moduleType is the registered shared-memory type for fwstate configs.
	moduleType = "fwstate"
)

// FWStateServiceName and MetricsServiceName are the fully-qualified gRPC
// service names exposed by this module, derived from the generated service
// descriptors so they cannot drift from the proto definitions.
//
// They are used to scope the module's [grpcmetrics.ServerMetrics] to its own
// services when several modules share a single [grpc.Server].
var (
	FWStateServiceName        = fwstatepb.FWStateService_ServiceDesc.ServiceName
	FWStateMetricsServiceName = fwstatepb.MetricsService_ServiceDesc.ServiceName
)

// FWStateService implements the gRPC service for FWState management.
type FWStateService struct {
	fwstatepb.UnimplementedFWStateServiceServer

	mu         sync.Mutex
	agent      *ffi.Agent
	configs    map[string]*FwStateConfig
	mapService *FWStateMapService
	metrics    *grpcmetrics.ServerMetrics

	log *zap.Logger
}

// NewFWStateService creates a new FWState service.
//
// When the WithMetrics option is supplied, gRPC call metrics are collected and
// exposed through the module's MetricsService.
//
// mapService resolves fwtable_name_v4 / fwtable_name_v6 references to
// concrete fwtable addresses in UpdateConfig and must be non-nil for that
// RPC to succeed.
func NewFWStateService(
	agent *ffi.Agent,
	mapService *FWStateMapService,
	options ...Option,
) *FWStateService {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	m := &FWStateService{
		agent:      agent,
		configs:    map[string]*FwStateConfig{},
		mapService: mapService,
		log:        opts.Log,
	}
	if opts.Metrics != nil {
		m.metrics = opts.Metrics(m.retention)
	}

	return m
}

// UnaryServerInterceptor returns the service's gRPC metrics interceptor, or
// nil when metrics are not configured.
func (m *FWStateService) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	if m.metrics == nil {
		return nil
	}

	return m.metrics.UnaryServerInterceptor()
}

func labeler(fullMethod string, req any) metrics.Labels {
	switch r := req.(type) {
	case *fwstatepb.UpdateConfigRequest:
		return metrics.Labels{"config": r.GetName()}
	case *fwstatepb.DeleteConfigRequest:
		return metrics.Labels{"config": r.GetName()}
	case *fwstatepb.ShowConfigRequest:
		return metrics.Labels{"config": r.GetName()}
	default:
		return nil
	}
}

func (m *FWStateService) UpdateConfig(
	ctx context.Context,
	req *fwstatepb.UpdateConfigRequest,
) (*fwstatepb.UpdateConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	fwtableNameV4 := req.GetFwtableNameV4()
	fwtableNameV6 := req.GetFwtableNameV6()
	if fwtableNameV4 == "" || fwtableNameV6 == "" {
		return nil, status.Error(codes.InvalidArgument, "fwtable_name_v4 and fwtable_name_v6 are required")
	}

	// Get fwstate configuration from req
	if req.SyncConfig == nil {
		return nil, status.Error(codes.InvalidArgument, "sync_config is required")
	}
	if err := validateSyncPorts(req.SyncConfig); err != nil {
		return nil, err
	}

	if m.mapService == nil {
		return nil, status.Error(codes.FailedPrecondition, "map service is not configured")
	}

	m.log.Debug("update fwstate config",
		zap.String("config", name),
		zap.String("fwtable_v4", fwtableNameV4),
		zap.String("fwtable_v6", fwtableNameV6),
	)

	if err := m.doUpdateConfig(req, name, fwtableNameV4, fwtableNameV6); err != nil {
		return nil, err
	}
	return &fwstatepb.UpdateConfigResponse{}, nil
}

// doUpdateConfig performs the full sync config update under m.mu, linking
// the named v4 and v6 fwstate-map objects.
//
// The merged cfwstate.SetModuleConfig links the named objects (resolved by
// the dataplane at ectx build time) and copies the sync parameters in a
// single call. The sync config never owns the fwtable memory: the maps
// stay owned and freed solely by the standalone fwstate-map objects.
//
// Caller must NOT hold m.mu; this method acquires it.
func (m *FWStateService) doUpdateConfig(
	req *fwstatepb.UpdateConfigRequest,
	name string,
	fwtableNameV4 string,
	fwtableNameV6 string,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	oldConfig := m.configs[name]

	newConfig, err := NewFWStateModuleConfig(m.agent, name)
	if err != nil {
		m.log.Error("failed to create fwstate config",
			zap.String("config", name), zap.Error(err))
		return status.Errorf(codes.Internal, "failed to create fwstate config: %v", err)
	}

	// Read the previous sync config directly so ToCWithDefaults can inherit
	// unspecified fields from the prior generation.
	var oldSync cfwstate.SyncConfig
	if oldConfig != nil {
		oldSync = oldConfig.ModuleConfig.GetSyncConfig()
	}

	// Resolve the merged sync config (request plus inherited defaults)
	// and validate it before touching the C config.
	mergedSync := req.SyncConfig.ToCWithDefaults(oldSync)
	if err := validateSyncConfig(fwstatepb.FromCSyncConfig(mergedSync)); err != nil {
		newConfig.Free()
		m.log.Error("invalid sync config", zap.String("config", name), zap.Error(err))
		return status.Errorf(codes.InvalidArgument, "invalid sync config: %v", err)
	}

	// Link the named v4/v6 fwstate-map objects and stamp the merged sync
	// config in a single fwstate_module_config_set call. The dataplane
	// resolves the fwtables at ectx build time via per-worker object ectx.
	if err := cfwstate.SetModuleConfig(newConfig.AsFFIModule(), fwtableNameV4, fwtableNameV6, mergedSync); err != nil {
		newConfig.Free()
		m.log.Error("failed to set fwstate module config",
			zap.String("config", name), zap.Error(err))
		return status.Errorf(codes.Internal, "failed to set fwstate module config: %v", err)
	}
	newConfig.SetFwtableNameV4(fwtableNameV4)
	newConfig.SetFwtableNameV6(fwtableNameV6)

	m.log.Debug("update fwstate module config", zap.String("config", name))

	if err := m.agent.UpdateModules([]ffi.ModuleConfig{newConfig.AsFFIModule()}); err != nil {
		newConfig.Free()
		m.log.Error("failed to publish fwstate config",
			zap.String("config", name), zap.Error(err))
		return status.Errorf(codes.Internal, "failed to publish fwstate config: %v", err)
	}

	if oldConfig != nil {
		oldConfig.Free()
	}

	m.configs[name] = newConfig

	m.log.Info("successfully updated FWState module", zap.String("config", name))
	return nil
}

func (m *FWStateService) ShowConfig(
	ctx context.Context,
	req *fwstatepb.ShowConfigRequest,
) (*fwstatepb.ShowConfigResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	config, ok := m.configs[name]
	if !ok {
		if req.OkIfNotFound {
			return nil, nil
		}
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	response := &fwstatepb.ShowConfigResponse{
		Name:          name,
		FwtableNameV4: config.FwtableNameV4(),
		FwtableNameV6: config.FwtableNameV6(),
		SyncConfig:    config.GetSyncConfig(),
	}

	return response, nil
}

func (m *FWStateService) ListConfigs(
	ctx context.Context,
	req *fwstatepb.ListConfigsRequest,
) (*fwstatepb.ListConfigsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	response := &fwstatepb.ListConfigsResponse{
		Configs: make([]string, 0, len(m.configs)),
	}

	for name := range m.configs {
		response.Configs = append(response.Configs, name)
	}

	return response, nil
}

func (m *FWStateService) DeleteConfig(
	ctx context.Context,
	req *fwstatepb.DeleteConfigRequest,
) (*fwstatepb.DeleteConfigResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	config, ok := m.configs[name]
	if !ok {
		return nil, status.Error(codes.NotFound, "config not found")
	}

	if err := m.agent.DeleteModule(moduleType, name); err != nil {
		return nil, status.Errorf(codes.Internal, "could not delete fwstate module config '%s': %v", name, err)
	}

	m.log.Info("successfully deleted FWState module config", zap.String("name", name))

	// The sync config only links the maps by name, so Free releases the
	// config struct itself; the standalone fwstate-map objects stay owned
	// by the map service.
	config.Free()

	delete(m.configs, name)

	return &fwstatepb.DeleteConfigResponse{}, nil
}

// validateSyncPorts rejects sync config ports that do not fit into the
// C-side uint16 port field.
//
// Thin wrapper over [fwstatepb.SyncConfig.ValidatePorts] that returns a
// gRPC status error so the handler can return it directly.
func validateSyncPorts(cfg *fwstatepb.SyncConfig) error {
	if err := cfg.ValidatePorts(); err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	return nil
}

// validateSyncConfig returns a plain error when the merged sync config is
// missing required fields or carries invalid timeouts.
//
// The caller wraps it with a gRPC status. Delegates to
// [fwstatepb.SyncConfig.Validate] so FWState and ACL share one validation
// path.
func validateSyncConfig(cfg *fwstatepb.SyncConfig) error {
	return cfg.Validate()
}
