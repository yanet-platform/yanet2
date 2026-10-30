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
	"github.com/yanet-platform/yanet2/controlplane/crud"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
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

// crudObserver forwards the shared core's mutation lifecycle to this
// module's observer.
type crudObserver struct {
	observer MutationObserver
}

func (m crudObserver) ObserveMutation(operation, phase string) {
	m.observer.ObserveFWStateMutation(operation, phase)
}

// publishBackend publishes fwstate configs through the shared-memory
// agent.
type publishBackend struct {
	agent *ffi.Agent
}

func (m *publishBackend) Update(name string, payload *FwStateConfig) (crud.Handle, error) {
	if err := crud.PublishModule(m.agent, payload, nil); err != nil {
		return nil, err
	}

	return payload, nil
}

func (m *publishBackend) Delete(name string) error {
	return m.agent.DeleteModuleConfig(moduleType, name)
}

// FWStateService implements the gRPC service for FWState management.
type FWStateService struct {
	fwstatepb.UnimplementedFWStateServiceServer

	// core owns the published configs and the superseded ones whose
	// free was refused because a live configuration generation still
	// referenced them. It retries those on the next update, and nothing
	// else remembers them. The core serializes whole mutations per name,
	// publish included, while its read side guards the entries alone and
	// is never held across a shared-memory call, so read paths stay
	// responsive while a publish is slow.
	core *crud.Core[*FwStateConfig]

	agent *ffi.Agent

	metrics *grpcmetrics.ServerMetrics

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

	var coreOptions []crud.Option
	if opts.Observer != nil {
		coreOptions = append(coreOptions,
			crud.WithMutationObserver(crudObserver{observer: opts.Observer}))
	}

	m := &FWStateService{
		core:  crud.NewCore[*FwStateConfig](&publishBackend{agent: agent}, coreOptions...),
		agent: agent,
		log:   opts.Log,
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

	m.log.Debug("update fwstate config", zap.String("config", name))

	err := m.core.Mutate(name, func(current *FwStateConfig, ok bool) (*FwStateConfig, error) {
		return m.prepareUpdate(name, current, req)
	})
	if err != nil {
		return nil, m.updateStatus(err)
	}

	m.log.Info("successfully updated FWState module", zap.String("config", name))
	return &fwstatepb.UpdateConfigResponse{}, nil
}

// updateStatus maps a failed mutation to the gRPC status it reports: a
// status the mutation itself produced passes through, a publish
// failure reports by its own kind.
func (m *FWStateService) updateStatus(err error) error {
	if _, ok := status.FromError(err); ok {
		return err
	}
	if errors.Is(err, ffi.ErrFailedPrecondition) {
		return status.Errorf(codes.FailedPrecondition, "failed to publish fwstate config: %v", err)
	}
	return status.Errorf(codes.Internal, "failed to publish fwstate config: %v", err)
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
	mapNameV4, mapNameV6 := mergedMapNames(oldConfig, req)
	// Validate the merged sync config before any C state is touched.
	syncConfig := mergedSyncConfig(oldConfig, req.GetSyncConfig())
	if err := syncConfig.ValidateMerged(); err != nil {
		m.log.Error("invalid sync config", zap.String("config", name), zap.Error(err))
		return nil, status.Errorf(codes.InvalidArgument, "invalid sync config: %v", err)
	}

	// The construction only allocates and initializes the replacement
	// and declares the map-name links: the names resolve against
	// published objects when the new generation installs, so an unknown
	// name surfaces from the publish, not from here.
	nativeSyncConfig, err := SyncConfigToC(syncConfig)
	if err != nil {
		m.log.Error("invalid sync config", zap.String("config", name), zap.Error(err))
		return nil, status.Errorf(codes.InvalidArgument, "invalid sync config: %v", err)
	}
	newConfig, err := newFWStateModuleConfig(
		m.agent,
		name,
		nativeSyncConfig,
		mapNameV4,
		mapNameV6,
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

	config, ok := m.core.Get(name)
	if !ok {
		if req.GetOkIfNotFound() {
			return &fwstatepb.ShowConfigResponse{}, nil
		}
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	response := &fwstatepb.ShowConfigResponse{
		Name:       name,
		MapNameV4:  config.MapNameV4(),
		MapNameV6:  config.MapNameV6(),
		SyncConfig: config.GetSyncConfig(),
	}

	return response, nil
}

func (m *FWStateService) ListConfigs(
	ctx context.Context,
	req *fwstatepb.ListConfigsRequest,
) (*fwstatepb.ListConfigsResponse, error) {
	return &fwstatepb.ListConfigsResponse{Configs: m.core.List()}, nil
}

func (m *FWStateService) DeleteConfig(
	ctx context.Context,
	req *fwstatepb.DeleteConfigRequest,
) (*fwstatepb.DeleteConfigResponse, error) {
	name := req.GetName()

	err := m.core.Delete(name)
	if errors.Is(err, configstore.ErrNotFound) {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}
	if err != nil {
		code := codes.Internal
		if errors.Is(err, ffi.ErrFailedPrecondition) {
			// A chain still references the config.
			code = codes.FailedPrecondition
		}
		return nil, status.Errorf(code, "could not delete fwstate module config '%s': %v", name, err)
	}

	m.log.Info("successfully deleted FWState module config", zap.String("name", name))
	return &fwstatepb.DeleteConfigResponse{}, nil
}
