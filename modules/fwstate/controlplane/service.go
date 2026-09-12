package fwstate

import (
	"context"
	"errors"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/go/grpcmetrics"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	"github.com/yanet-platform/yanet2/controlplane/configstore"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
	fwstatemap "github.com/yanet-platform/yanet2/objects/fwstate/controlplane"
)

// Option configures an FWStateService.
type Option func(*options)

// MutationObserver observes a mutation's lifecycle in the config
// store: before it enters the store's writer section, once an admitted
// mutation is about to touch shared memory inside that section, and as
// that work ends. A mutation the store rejects before running it — a
// delete of an unknown name — reports only the first phase. It
// observes synchronization but never controls it, and lets tests prove
// ordering between concurrent mutations.
type MutationObserver interface {
	ObserveFWStateMutation(operation, phase string)
}

// WithMutationObserver installs an observer receiving every mutation's
// lock lifecycle events.
func WithMutationObserver(observer MutationObserver) Option {
	return func(o *options) {
		o.Observer = observer
	}
}

// options holds the optional parameters for FWStateService construction.
type options struct {
	Metrics  grpcmetrics.Factory
	Observer MutationObserver
	Log      *zap.Logger
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
				service == FWStateMetricsServiceName
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

// Mutation phases report a goroutine before it enters the store's
// writer section, once an admitted mutation is about to touch shared
// memory inside it, and as that work ends.
const (
	mutationWaiting   = "waiting"
	mutationAcquired  = "acquired"
	mutationReleasing = "releasing"
)

// FWStateService implements the gRPC service for FWState management.
type FWStateService struct {
	fwstatepb.UnimplementedFWStateServiceServer

	// configs owns the published configs and the superseded ones whose
	// free was refused because a live configuration generation still
	// referenced them; it retries those on the next update, through
	// ReclaimDeferred, and nothing else remembers them. The store's
	// writer side serializes whole mutations, publish included, while
	// its read side guards the entries alone and is never held across
	// a shared-memory call, so read paths stay responsive while a
	// publish is slow.
	configs *configstore.Store[*FwStateConfig]

	agent    *ffi.Agent
	observer MutationObserver
	metrics  *grpcmetrics.ServerMetrics

	log *zap.Logger
}

// NewFWStateService creates a new FWState service.
//
// When the WithMetrics option is supplied, gRPC call metrics are collected and
// exposed through the module's MetricsService.
func NewFWStateService(
	agent *ffi.Agent,
	options ...Option,
) *FWStateService {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	m := &FWStateService{
		agent:    agent,
		configs:  configstore.NewStore[*FwStateConfig](),
		observer: opts.Observer,
		log:      opts.Log,
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

	m.log.Debug("update fwstate config", zap.String("config", name))

	m.observeMutation("update", mutationWaiting)
	err := m.configs.Update(name, func(current *FwStateConfig, ok bool) (*FwStateConfig, error) {
		m.observeMutation("update", mutationAcquired)
		defer m.observeMutation("update", mutationReleasing)

		newConfig, err := m.prepareUpdate(name, current, req)
		if err != nil {
			return nil, err
		}

		m.log.Debug("update fwstate module config", zap.String("config", name))

		if err := m.agent.UpdateModules([]ffi.ModuleConfig{newConfig.AsFFIModule()}); err != nil {
			if err := newConfig.Free(); err != nil {
				m.log.Error("failed to free unpublished fwstate config",
					zap.String("config", name), zap.Error(err))
			}
			m.log.Error("failed to publish fwstate config", zap.String("config", name), zap.Error(err))
			if errors.Is(err, ffi.ErrFailedPrecondition) {
				return nil, status.Errorf(codes.FailedPrecondition, "failed to publish fwstate config: %v", err)
			}
			return nil, status.Errorf(codes.Internal, "failed to publish fwstate config: %v", err)
		}

		return newConfig, nil
	})
	if err != nil {
		return nil, err
	}

	m.log.Info("successfully updated FWState module", zap.String("config", name))
	return &fwstatepb.UpdateConfigResponse{}, nil
}

// prepareUpdate builds the replacement config for name in one step.
//
// The old config's sync settings and map links propagate, the request
// merges over them, and the resulting map names are declared as object
// links. The mutation lock stays held so the old handle cannot
// disappear mid-construction.
func (m *FWStateService) prepareUpdate(
	name string,
	oldConfig *FwStateConfig,
	req *fwstatepb.UpdateConfigRequest,
) (*FwStateConfig, error) {
	if req.UpdateMask != nil {
		merged, err := maskedUpdate(oldConfig, req)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		req = merged
	} else {
		// Validate before conversion can narrow a legacy numeric value.
		if err := req.GetSyncConfig().ValidateFields(); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid sync config: %v", err)
		}
		if err := req.ValidateEndpointClears(); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid sync endpoint update: %v", err)
		}
		mapNameV4, mapNameV6 := mergedMapNames(oldConfig, req)
		req = &fwstatepb.UpdateConfigRequest{
			MapNameV4:  mapNameV4,
			MapNameV6:  mapNameV6,
			SyncConfig: mergedSyncConfigWithClears(oldConfig, req.SyncConfig, req.GetClearMulticast(), req.GetClearUnicast()),
		}
	}
	for _, mapName := range []string{req.MapNameV4, req.MapNameV6} {
		if mapName != "" {
			if err := fwstatemap.ValidateMapName(mapName); err != nil {
				return nil, err
			}
		}
	}
	if err := req.SyncConfig.ValidateFields(); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid sync config: %v", err)
	}
	// Validate the merged sync config before any C state is touched.
	syncConfig := req.SyncConfig
	if err := syncConfig.Validate(); err != nil {
		m.log.Error("invalid sync config", zap.String("config", name), zap.Error(err))
		return nil, status.Errorf(codes.InvalidArgument, "invalid sync config: %v", err)
	}

	// The construction only allocates and initializes the replacement
	// and declares the map-name links: the names resolve against
	// published objects when the new generation installs, so an unknown
	// name surfaces from the publish, not from here.
	newConfig, err := newFWStateModuleConfig(
		m.agent,
		name,
		syncConfig.ToC(),
		req.MapNameV4,
		req.MapNameV6,
	)
	if err != nil {
		m.log.Error("failed to build fwstate config", zap.String("config", name), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to build fwstate config: %v", err)
	}

	return newConfig, nil
}

func (m *FWStateService) ShowConfig(
	ctx context.Context,
	req *fwstatepb.ShowConfigRequest,
) (*fwstatepb.ShowConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	mapNameV4, mapNameV6, syncConfig, ok := m.configSnapshot(name)
	if !ok {
		if req.OkIfNotFound {
			return nil, nil
		}
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	response := &fwstatepb.ShowConfigResponse{
		Name:       name,
		MapNameV4:  mapNameV4,
		MapNameV6:  mapNameV6,
		SyncConfig: syncConfig,
	}

	return response, nil
}

// configSnapshot reads the published state of name in one short store
// read, so it never waits behind a publish of any name.
func (m *FWStateService) configSnapshot(
	name string,
) (string, string, *fwstatepb.SyncConfig, bool) {
	config, ok := m.configs.Get(name)
	if !ok {
		return "", "", nil, false
	}

	return config.MapNameV4(), config.MapNameV6(), config.GetSyncConfig(), true
}

func (m *FWStateService) ListConfigs(
	ctx context.Context,
	req *fwstatepb.ListConfigsRequest,
) (*fwstatepb.ListConfigsResponse, error) {
	return &fwstatepb.ListConfigsResponse{Configs: m.configs.Names()}, nil
}

func (m *FWStateService) DeleteConfig(
	ctx context.Context,
	req *fwstatepb.DeleteConfigRequest,
) (*fwstatepb.DeleteConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	m.observeMutation("delete", mutationWaiting)
	err := m.configs.Delete(name, func(*FwStateConfig) error {
		m.observeMutation("delete", mutationAcquired)
		defer m.observeMutation("delete", mutationReleasing)

		// DeleteModuleConfig removes the shared-memory publication but
		// does not free the module. The store removes the entry only
		// after this returns, letting readers finish against the old
		// handle before its free is attempted.
		return m.agent.DeleteModuleConfig(moduleType, name)
	})
	if errors.Is(err, configstore.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "config not found")
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "could not delete fwstate module config '%s': %v", name, err)
	}

	m.log.Info("successfully deleted FWState module config", zap.String("name", name))
	return &fwstatepb.DeleteConfigResponse{}, nil
}

func (m *FWStateService) observeMutation(operation, phase string) {
	if m.observer != nil {
		m.observer.ObserveFWStateMutation(operation, phase)
	}
}

// ReclaimDeferred retries every deferred config, dropping the ones whose
// generations have drained and keeping the rest deferred. It is the
// reclamation handler for this module's superseded configs; the service
// itself runs it after each successful publish, and anything else may
// call it at any time.
func (m *FWStateService) ReclaimDeferred() {
	m.configs.ReclaimDeferred()
}
