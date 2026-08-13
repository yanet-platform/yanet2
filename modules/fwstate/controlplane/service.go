package fwstate

import (
	"context"
	"io"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/go/grpcmetrics"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
	objfwstate "github.com/yanet-platform/yanet2/objects/fwstate/bindings/go/cfwstate"
)

// Option configures an FWStateService.
type Option func(*options)

// MutationObserver observes updateMu acquisition for a mutation, before
// Lock, after Lock succeeds, and immediately before Unlock. It observes
// synchronization but never controls it, and lets tests prove ordering
// between concurrent mutations.
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
	Metrics   grpcmetrics.Factory
	Observer  MutationObserver
	MapLookup MapLookup
	Log       *zap.Logger
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

// MapLookup resolves a fwstate-map object by name and family from the
// objects controlplane's internal registry. It returns a borrowed
// handle that stays valid until the object is deleted, or nil when no
// such object exists.
type MapLookup func(name string, kind objfwstate.Kind) *objfwstate.MapObjectConfig

// WithMapLookup installs the resolver used for reads delegated to linked
// map objects (stats, entry cursors), replacing the shared-memory
// registry lookup via the C API.
func WithMapLookup(lookup MapLookup) Option {
	return func(o *options) {
		o.MapLookup = lookup
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

	// defaultListEntriesBatchSize is the batch size used when the caller
	// sends zero in the request.
	defaultListEntriesBatchSize uint32 = 100

	// maxListEntriesBatchSize caps the number of entries fetched per
	// ListEntries round-trip to prevent unbounded allocation under the
	// service state lock.
	maxListEntriesBatchSize uint32 = 10000

	// maxSyncPort is the highest value accepted for port_multicast,
	// matching the width of the C-side uint16 port field.
	maxSyncPort uint32 = 65535
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

// clampBatchSize returns a batch size that is within the allowed range:
// zero is replaced with defaultListEntriesBatchSize, and values above
// maxListEntriesBatchSize are clamped to maxListEntriesBatchSize.
func clampBatchSize(n uint32) uint32 {
	if n == 0 {
		return defaultListEntriesBatchSize
	}
	if n > maxListEntriesBatchSize {
		return maxListEntriesBatchSize
	}
	return n
}

// Mutation phases report a goroutine before Lock, after Lock succeeds, and
// immediately before Unlock.
const (
	mutationWaiting   = "waiting"
	mutationAcquired  = "acquired"
	mutationReleasing = "releasing"
)

// FWStateService implements the gRPC service for FWState management.
type FWStateService struct {
	fwstatepb.UnimplementedFWStateServiceServer

	// updateMu serializes mutations. stateMu protects configs and the
	// published handle lifetime. The only valid order is updateMu followed
	// by stateMu.
	updateMu  sync.Mutex
	stateMu   sync.RWMutex
	agent     *ffi.Agent
	configs   map[string]*FwStateConfig
	observer  MutationObserver
	metrics   *grpcmetrics.ServerMetrics
	mapLookup MapLookup

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
		agent:     agent,
		configs:   make(map[string]*FwStateConfig),
		observer:  opts.Observer,
		mapLookup: opts.MapLookup,
		log:       opts.Log,
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
	case *fwstatepb.GetStatsRequest:
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

	// Get fwstate configuration from req
	if req.SyncConfig == nil {
		return nil, status.Error(codes.InvalidArgument, "sync_config is required")
	}
	if req.GetMapNameV4() == "" {
		return nil, status.Error(codes.InvalidArgument, "map_name_v4 is required")
	}
	if req.GetMapNameV6() == "" {
		return nil, status.Error(codes.InvalidArgument, "map_name_v6 is required")
	}
	if err := validateSyncPorts(req.SyncConfig); err != nil {
		return nil, err
	}

	m.log.Debug("update fwstate config", zap.String("config", name))

	err := m.withMutation("update", func() error {
		oldConfig, newConfig, err := m.prepareUpdate(name, req)
		if err != nil {
			return err
		}

		m.log.Debug("update fwstate module config", zap.String("config", name))

		if err := m.publishUpdate(name, newConfig); err != nil {
			newConfig.Free()
			m.log.Error("failed to publish fwstate config", zap.String("config", name), zap.Error(err))
			return status.Errorf(codes.Internal, "failed to publish fwstate config: %v", err)
		}

		if oldConfig != nil {
			oldConfig.Free()
		}

		m.log.Info("successfully updated FWState module", zap.String("config", name))
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &fwstatepb.UpdateConfigResponse{}, nil
}

// prepareUpdate builds the replacement config for name in one step: the
// old config's sync config and map chain propagate, the request's sync
// config merges over them, and the maps are created or grown. The state
// lock stays held so the old handle cannot disappear mid-construction.
func (m *FWStateService) prepareUpdate(
	name string,
	req *fwstatepb.UpdateConfigRequest,
) (*FwStateConfig, *FwStateConfig, error) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	oldConfig := m.configs[name]

	// Validate the merged sync config before any C state is touched.
	syncConfig := mergedSyncConfig(oldConfig, req.SyncConfig)
	if err := validateSyncConfig(syncConfig); err != nil {
		m.log.Error("invalid sync config", zap.String("config", name), zap.Error(err))
		return nil, nil, status.Errorf(codes.InvalidArgument, "invalid sync config: %v", err)
	}

	// The construction resolves both map names against the agent's
	// current object generation, so an unknown name fails here and
	// names the object.
	newConfig, err := NewFWStateModuleConfig(
		m.agent,
		name,
		oldConfig,
		req.SyncConfig,
		req.GetMapNameV4(),
		req.GetMapNameV6(),
	)
	if err != nil {
		m.log.Error("failed to update fwstate config", zap.String("config", name), zap.Error(err))
		return nil, nil, status.Errorf(codes.InvalidArgument, "failed to update fwstate config: %v", err)
	}

	return oldConfig, newConfig, nil
}

func (m *FWStateService) publishUpdate(
	name string,
	newConfig *FwStateConfig,
) error {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	if err := m.agent.UpdateModules([]ffi.ModuleConfig{newConfig.AsFFIModule()}); err != nil {
		return err
	}
	newConfig.MarkPublished()
	m.configs[name] = newConfig

	return nil
}

// configForMutation returns a handle whose lifetime remains protected by the
// caller's updateMu lock.
func (m *FWStateService) configForMutation(name string) (*FwStateConfig, bool) {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()

	config, ok := m.configs[name]
	return config, ok
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

func (m *FWStateService) configSnapshot(
	name string,
) (string, string, *fwstatepb.SyncConfig, bool) {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()

	config, ok := m.configs[name]
	if !ok {
		return "", "", nil, false
	}

	return config.MapNameV4(), config.MapNameV6(), config.GetSyncConfig(), true
}

func (m *FWStateService) ListConfigs(
	ctx context.Context,
	req *fwstatepb.ListConfigsRequest,
) (*fwstatepb.ListConfigsResponse, error) {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()

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
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	err := m.withMutation("delete", func() error {
		config, ok := m.configForMutation(name)
		if !ok {
			return status.Error(codes.NotFound, "config not found")
		}

		// DeleteModuleConfig removes the shared-memory publication but does not
		// free the module. Keeping stateMu unlocked lets readers finish against
		// the old handle before unpublishConfig establishes the Free barrier.
		if err := m.agent.DeleteModuleConfig(moduleType, name); err != nil {
			return status.Errorf(codes.Internal, "could not delete fwstate module config '%s': %v", name, err)
		}

		m.unpublishConfig(name)
		config.Free()
		m.log.Info("successfully deleted FWState module config", zap.String("name", name))
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &fwstatepb.DeleteConfigResponse{}, nil
}

func (m *FWStateService) withMutation(operation string, mutate func() error) error {
	m.observeMutation(operation, mutationWaiting)
	m.updateMu.Lock()
	defer func() {
		m.observeMutation(operation, mutationReleasing)
		m.updateMu.Unlock()
	}()
	m.observeMutation(operation, mutationAcquired)

	return mutate()
}

func (m *FWStateService) observeMutation(operation, phase string) {
	if m.observer != nil {
		m.observer.ObserveFWStateMutation(operation, phase)
	}
}

func (m *FWStateService) unpublishConfig(name string) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	delete(m.configs, name)
}

func (m *FWStateService) GetStats(
	ctx context.Context,
	req *fwstatepb.GetStatsRequest,
) (*fwstatepb.GetStatsResponse, error) {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()

	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	config, ok := m.configs[name]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	mapsStats := m.lookupMapsStats(config)

	response := &fwstatepb.GetStatsResponse{
		Ipv4Stats: &fwstatepb.MapStats{
			IndexSize:        uint32(mapsStats.IPv4.IndexSize),
			ExtraBucketCount: uint32(mapsStats.IPv4.ExtraBucketCount),
			MaxChainLength:   uint32(mapsStats.IPv4.MaxChainLength),
			LayerCount:       uint32(mapsStats.IPv4.LayerCount),
			TotalElements:    uint64(mapsStats.IPv4.TotalElements),
			MaxDeadline:      uint64(mapsStats.IPv4.MaxDeadline),
			MemoryUsed:       uint64(mapsStats.IPv4.MemoryUsed),
			Note:             "Statistics are currently shown for the first layer only",
		},
		Ipv6Stats: &fwstatepb.MapStats{
			IndexSize:        uint32(mapsStats.IPv6.IndexSize),
			ExtraBucketCount: uint32(mapsStats.IPv6.ExtraBucketCount),
			MaxChainLength:   uint32(mapsStats.IPv6.MaxChainLength),
			LayerCount:       uint32(mapsStats.IPv6.LayerCount),
			TotalElements:    uint64(mapsStats.IPv6.TotalElements),
			MaxDeadline:      uint64(mapsStats.IPv6.MaxDeadline),
			MemoryUsed:       uint64(mapsStats.IPv6.MemoryUsed),
			Note:             "Statistics are currently shown for the first layer only",
		},
	}

	return response, nil
}

// lookupMapsStats reads both linked map objects' stats from the objects
// controlplane's internal registry. A family whose object is absent
// reports zero stats: the module config's link pins the object against
// deletion, so absence can only be transient topology drift, not a
// missing table the module still inserts into.
func (m *FWStateService) lookupMapsStats(config *FwStateConfig) objfwstate.MapsStats {
	var mapsStats objfwstate.MapsStats
	if object := m.lookupMap(config.MapNameV4(), objfwstate.KindV4); object != nil {
		mapsStats.IPv4 = object.GetStats()
	}
	if object := m.lookupMap(config.MapNameV6(), objfwstate.KindV6); object != nil {
		mapsStats.IPv6 = object.GetStats()
	}
	return mapsStats
}

// lookupMap returns the linked map object for the given family from the
// objects controlplane's internal registry, or nil when no resolver is
// installed or the object is absent.
func (m *FWStateService) lookupMap(
	name string,
	kind objfwstate.Kind,
) *objfwstate.MapObjectConfig {
	if m.mapLookup == nil || name == "" {
		return nil
	}
	return m.mapLookup(name, kind)
}

// lookupMapObject returns a borrowed read handle for the family's linked
// map object, resolved against the objects controlplane's internal
// registry.
func (m *FWStateService) lookupMapObject(
	config *FwStateConfig,
	isIPv6 bool,
) (*objfwstate.MapObjectConfig, error) {
	kind := objfwstate.KindV4
	mapName := config.MapNameV4()
	if isIPv6 {
		kind = objfwstate.KindV6
		mapName = config.MapNameV6()
	}

	object := m.lookupMap(mapName, kind)
	if object == nil {
		return nil, status.Errorf(
			codes.NotFound, "linked fwstate-map %q not found", mapName,
		)
	}
	return object, nil
}

func (m *FWStateService) ListEntries(
	stream grpc.BidiStreamingServer[fwstatepb.ListEntriesRequest, fwstatepb.ListEntriesResponse],
) error {
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		configName := req.GetConfigName()
		if configName == "" {
			return status.Error(codes.InvalidArgument, "config_name is required")
		}

		count := clampBatchSize(req.GetBatchSize())

		entries, newIndex, hasMore, generation, err := m.readConfigEntries(req, count)
		if status.Code(err) == codes.NotFound {
			return err
		}
		if err != nil {
			return status.Errorf(codes.Internal, "cursor read failed: %v", err)
		}

		pbEntries := make([]*fwstatepb.FwStateEntry, 0, len(entries))
		for idx := range entries {
			pbEntries = append(pbEntries, fwstatepb.FromCursorEntry(entries[idx]))
		}

		resp := &fwstatepb.ListEntriesResponse{
			Entries:    pbEntries,
			HasMore:    hasMore,
			Index:      newIndex,
			Generation: generation,
		}

		if err := stream.Send(resp); err != nil {
			return err
		}
	}
}

func (m *FWStateService) readConfigEntries(
	req *fwstatepb.ListEntriesRequest,
	count uint32,
) ([]objfwstate.CursorEntry, int64, bool, uint64, error) {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()

	configName := req.GetConfigName()
	config, ok := m.configs[configName]
	if !ok {
		return nil, 0, false, 0, status.Errorf(codes.NotFound, "config %q not found", configName)
	}

	object, err := m.lookupMapObject(config, req.GetIsIpv6())
	if err != nil {
		return nil, 0, false, 0, err
	}

	now := uint64(time.Now().UnixNano())
	var entries []objfwstate.CursorEntry
	var newIndex int64
	var hasMore bool
	if req.GetDirection() == fwstatepb.Direction_BACKWARD {
		entries, newIndex, hasMore, err = object.ReadBackward(
			req.GetLayerIndex(),
			req.GetIndex(), req.GetIncludeExpired(),
			now, count,
		)
	} else {
		entries, newIndex, hasMore, err = object.ReadForward(
			req.GetLayerIndex(),
			req.GetIndex(), req.GetIncludeExpired(),
			now, count,
		)
	}

	return entries, newIndex, hasMore, config.Generation(), err
}

// validateSyncPorts rejects sync config ports that do not fit into the
// C-side uint16 port field.
//
// A zero port means "unset / keep current" and is allowed here. The
// required-destination check in validateSyncConfig rejects a request
// that leaves the multicast destination unset.
func validateSyncPorts(cfg *fwstatepb.SyncConfig) error {
	if portMulticast := cfg.GetPortMulticast(); portMulticast > maxSyncPort {
		return status.Errorf(codes.InvalidArgument, "port_multicast %d exceeds maximum allowed value %d", portMulticast, maxSyncPort)
	}
	return nil
}

// validateSyncConfig validates that required sync config fields are set
func validateSyncConfig(cfg *fwstatepb.SyncConfig) error {
	var missing []string

	// Check src_addr (16 bytes for IPv6)
	if len(cfg.GetSrcAddr().GetAddr()) != 16 || isAllZeroBytes(cfg.GetSrcAddr().GetAddr()) {
		missing = append(missing, "src_addr")
	}

	// The module matches incoming sync packets against the multicast
	// destination, so both the address and the port are required.
	if len(cfg.GetDstAddrMulticast().GetAddr()) != 16 || isAllZeroBytes(cfg.GetDstAddrMulticast().GetAddr()) {
		missing = append(missing, "dst_addr_multicast")
	}
	if cfg.GetPortMulticast() == 0 {
		missing = append(missing, "port_multicast")
	}

	if len(missing) > 0 {
		return status.Errorf(codes.InvalidArgument, "missing required sync config fields: %v", missing)
	}

	if err := cfg.ValidateTimeouts(); err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid sync config timeouts: %v", err)
	}

	return nil
}

// isAllZeroBytes checks if all bytes in the slice are zero
func isAllZeroBytes(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}
