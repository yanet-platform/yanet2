package fwstate

// Lock ordering across the three fwstate/acl services:
//
//	FWStateMapService.mu  →  FWStateService.mu  →  ACLService.mu
//
// map.mu is always acquired first when a code path needs both map and sync
// config locks. The two call sites that hold map.mu and then acquire a
// consumer lock are:
//
//   - FWStateMapService.DeleteMap holds map.mu while asking both consumers
//     for their references via ConfigsUsingMap (which lock fwstate.mu /
//     acl.mu internally).
//   - FWStateService.UpdateConfig and ACLService.UpdateConfig reference map
//     names that are checked by DeleteMap's consumer scan.
//
// No code path acquires these locks in the reverse direction, so no AB-BA
// deadlock is possible.

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
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
)

// maxWorkerCount is the highest value accepted for worker_count, matching
// the width of the C-side uint16 parameter of fwstate_map_create_map.
const maxWorkerCount uint32 = 65535

const (
	// defaultListEntriesBatchSize is the batch size used when the caller
	// sends zero in the request.
	defaultListEntriesBatchSize uint32 = 100

	// maxListEntriesBatchSize caps the number of entries fetched per
	// ListEntries round-trip to prevent unbounded allocation under the
	// service mutex.
	maxListEntriesBatchSize uint32 = 10000
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

// FWStateMapServiceName is the fully-qualified gRPC service name for the
// fwstate-map management service, derived from the generated service
// descriptor.
var FWStateMapServiceName = fwstatepb.FWStateMapService_ServiceDesc.ServiceName

// MapConsumer is a service that references a named fwstate-map.
//
// Implemented by FWStateService (for sync configs) and the ACL adapter
// (for ACL configs). FWStateMapService uses ConfigsUsingMap to refuse
// DeleteMap while consumers exist. Layer insertion is transparent to
// consumers because they link the map by name and the dataplane resolves
// the fwtable through per-worker object ectx, so no republish is needed
// when the active head changes.
type MapConsumer interface {
	// ConfigsUsingMap returns the names of configs that reference the
	// given map name. Implementations lock internally.
	ConfigsUsingMap(mapName string) []string
}

// MapLayerTrimmer reclaims layers whose deadline has passed.
//
// [cfwstate.MapObjectConfig] satisfies this interface; the seam lets unit
// tests record trim calls without a live shared-memory handle.
type MapLayerTrimmer interface {
	TrimStaleLayers(now uint64) error
}

// Compile-time assertion that [cfwstate.MapObjectConfig] satisfies
// [MapLayerTrimmer].
var _ MapLayerTrimmer = (*cfwstate.MapObjectConfig)(nil)

// MapOption configures an FWStateMapService.
type MapOption func(*mapOptions)

type mapOptions struct {
	Metrics grpcmetrics.Factory
	Log     *zap.Logger
}

func newMapOptions() *mapOptions {
	return &mapOptions{
		Log: zap.NewNop(),
	}
}

// WithMapLog sets the map service logger.
func WithMapLog(log *zap.Logger) MapOption {
	return func(o *mapOptions) {
		o.Log = log
	}
}

// WithMapMetrics enables collection of gRPC call metrics for the map
// service using the supplied factory.
func WithMapMetrics(factory grpcmetrics.Factory) MapOption {
	return func(o *mapOptions) {
		o.Metrics = factory
	}
}

// NewMapMetricsFactory returns a [grpcmetrics.Factory] scoped to the
// FWStateMapService.
func NewMapMetricsFactory(extra ...grpcmetrics.Option) grpcmetrics.Factory {
	opts := make([]grpcmetrics.Option, 0, len(extra)+2)
	opts = append(opts, grpcmetrics.WithLabeler(MapLabeler))
	opts = append(opts, grpcmetrics.WithServiceFilter(
		func(service string) bool {
			return service == FWStateMapServiceName
		},
	))
	opts = append(opts, extra...)
	return grpcmetrics.NewFactory(opts...)
}

func MapLabeler(fullMethod string, req any) metrics.Labels {
	switch r := req.(type) {
	case *fwstatepb.CreateMapRequest:
		return metrics.Labels{"map": r.GetName()}
	case *fwstatepb.DeleteMapRequest:
		return metrics.Labels{"map": r.GetName()}
	case *fwstatepb.GetMapStatsRequest:
		return metrics.Labels{"map": r.GetName()}
	case *fwstatepb.InsertLayerRequest:
		return metrics.Labels{"map": r.GetName()}
	default:
		return nil
	}
}

// FWStateMap manages a standalone named fwstate-map object in shared
// memory.
type FWStateMap struct {
	name   string
	config *cfwstate.MapObjectConfig
}

// Name returns the map name.
func (m *FWStateMap) Name() string { return m.name }

// Config returns the underlying map object config handle.
func (m *FWStateMap) Config() *cfwstate.MapObjectConfig { return m.config }

// FWStateMapService implements the gRPC service for standalone named
// fwstate-map management.
type FWStateMapService struct {
	fwstatepb.UnimplementedFWStateMapServiceServer

	// mu protects maps and is the head of the cross-service lock chain
	// (map → fwstate → acl). See the package-level lock-ordering note.
	mu           sync.Mutex
	agent        *ffi.Agent
	maps         map[string]*FWStateMap
	syncConsumer MapConsumer
	aclConsumer  MapConsumer
	metrics      *grpcmetrics.ServerMetrics

	// barrier advances the dataplane to a new config generation and waits
	// for every worker to observe it, acting as the RCU grace period that
	// must elapse between unlinking stale layers and freeing their memory.
	//
	// Defaults to republishing the map object via agent.UpdateObjects
	// (cp_config_gen_install → dp_config_wait_for_gen); tests override it
	// to record barrier calls without a live shared-memory agent.
	barrier func(cfwstate.MapObjectConfig) error
	log     *zap.Logger
}

// NewFWStateMapService creates a new FWStateMapService.
//
// aclConsumer is used by DeleteMap to refuse deletion while ACL configs
// reference the map. The sync consumer must be wired separately via
// SetSyncConsumer once FWStateService is constructed.
func NewFWStateMapService(
	agent *ffi.Agent,
	aclConsumer MapConsumer,
	options ...MapOption,
) *FWStateMapService {
	opts := newMapOptions()
	for _, o := range options {
		o(opts)
	}

	m := &FWStateMapService{
		agent:       agent,
		maps:        map[string]*FWStateMap{},
		aclConsumer: aclConsumer,
		log:         opts.Log,
	}
	m.barrier = m.publishGeneration

	if opts.Metrics != nil {
		m.metrics = opts.Metrics(m.mapRetention)
	}

	return m
}

// publishGeneration upserts the map object into a new config generation
// and blocks until every dataplane worker has advanced to it.
//
// This is the default generation barrier for reclaimStaleLayers: the
// agent.UpdateObjects call drives cp_config_gen_install, which performs
// SET_OFFSET_OF on the new generation followed by dp_config_wait_for_gen.
// Re-upserting the same map pointer is safe — the registry uses reference
// counting, so the ref/unref pair nets to zero and the map survives intact
// while only the generation advances.
func (m *FWStateMapService) publishGeneration(mapCP cfwstate.MapObjectConfig) error {
	return m.agent.UpdateObjects([]ffi.ObjectConfig{mapCP.AsFFIObject()})
}

// SetSyncConsumer wires the fwstate sync-config service as a map consumer.
//
// Must be called once after both FWStateMapService and FWStateService are
// constructed.
func (m *FWStateMapService) SetSyncConsumer(consumer MapConsumer) {
	m.syncConsumer = consumer
}

// UnaryServerInterceptor returns the service's gRPC metrics interceptor,
// or nil when metrics are not configured.
func (m *FWStateMapService) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	if m.metrics == nil {
		return nil
	}
	return m.metrics.UnaryServerInterceptor()
}

func (m *FWStateMapService) mapRetention() func(metrics.MetricID) bool {
	m.mu.Lock()
	names := make(map[string]struct{}, len(m.maps))
	for name := range m.maps {
		names[name] = struct{}{}
	}
	m.mu.Unlock()

	return func(id metrics.MetricID) bool {
		name := id.Labels["map"]
		if name == "" {
			return true
		}
		_, ok := names[name]
		return ok
	}
}

// CreateMap creates a new named fwstate-map for one address family and
// publishes it to the dataplane.
func (m *FWStateMapService) CreateMap(
	ctx context.Context,
	req *fwstatepb.CreateMapRequest,
) (*fwstatepb.CreateMapResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "map name is required")
	}
	if err := ValidateWorkerCount(req.GetWorkerCount()); err != nil {
		return nil, err
	}
	kind := kindFromProto(req.GetKind())

	m.log.Debug("create fwstate-map",
		zap.String("map", name),
		zap.String("kind", kind.String()),
	)

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.maps[name]; exists {
		return nil, status.Errorf(codes.AlreadyExists, "fwstate-map %q already exists", name)
	}

	mapConfig, err := cfwstate.NewMapObjectConfig(m.agent, name, kind)
	if err != nil {
		m.log.Error("failed to create fwstate-map config",
			zap.String("map", name), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to create fwstate-map config: %v", err)
	}

	if err := mapConfig.CreateMap(
		req.GetIndexSize(),
		req.GetExtraBucketCount(),
		uint16(req.GetWorkerCount()),
	); err != nil {
		mapConfig.Free()
		m.log.Error("failed to create fwstate-map table",
			zap.String("map", name), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to create fwstate-map table: %v", err)
	}

	if err := m.agent.UpdateObjects([]ffi.ObjectConfig{mapConfig.AsFFIObject()}); err != nil {
		mapConfig.Free()
		m.log.Error("failed to publish fwstate-map",
			zap.String("map", name), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to publish fwstate-map: %v", err)
	}

	m.maps[name] = &FWStateMap{name: name, config: mapConfig}

	m.log.Info("successfully created fwstate-map",
		zap.String("map", name),
		zap.String("kind", kind.String()),
	)
	return &fwstatepb.CreateMapResponse{}, nil
}

// DeleteMap removes a named fwstate-map, refusing if any consumer still
// references it.
func (m *FWStateMapService) DeleteMap(
	ctx context.Context,
	req *fwstatepb.DeleteMapRequest,
) (*fwstatepb.DeleteMapResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "map name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	fwMap, ok := m.maps[name]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "fwstate-map %q not found", name)
	}

	var consumers []string
	if m.syncConsumer != nil {
		consumers = append(consumers, m.syncConsumer.ConfigsUsingMap(name)...)
	}
	if m.aclConsumer != nil {
		consumers = append(consumers, m.aclConsumer.ConfigsUsingMap(name)...)
	}
	if len(consumers) > 0 {
		return nil, status.Errorf(codes.FailedPrecondition,
			"fwstate-map %q is referenced by configs: %v", name, consumers)
	}

	if err := m.agent.DeleteObject(fwMap.Config().Kind().ObjectType(), name); err != nil {
		m.log.Error("failed to delete fwstate-map",
			zap.String("map", name), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to delete fwstate-map: %v", err)
	}

	fwMap.Config().Free()
	delete(m.maps, name)

	m.log.Info("successfully deleted fwstate-map", zap.String("map", name))
	return &fwstatepb.DeleteMapResponse{}, nil
}

// ListMaps returns the names of all registered fwstate-map objects.
func (m *FWStateMapService) ListMaps(
	ctx context.Context,
	req *fwstatepb.ListMapsRequest,
) (*fwstatepb.ListMapsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	response := &fwstatepb.ListMapsResponse{
		Maps: make([]string, 0, len(m.maps)),
	}
	for name := range m.maps {
		response.Maps = append(response.Maps, name)
	}
	return response, nil
}

// GetMapStats returns statistics for the single fwtable of a named
// fwstate-map.
func (m *FWStateMapService) GetMapStats(
	ctx context.Context,
	req *fwstatepb.GetMapStatsRequest,
) (*fwstatepb.GetMapStatsResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "map name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	fwMap, ok := m.maps[name]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "fwstate-map %q not found", name)
	}

	return &fwstatepb.GetMapStatsResponse{
		Stats: MapStatsToProto(fwMap.Config().GetStats()),
	}, nil
}

// InsertLayer inserts a new layer into the fwtable chain of a named
// fwstate-map.
//
// After the new layer is committed in place (the table head now points at
// the new active layer), layers whose deadline has passed are reclaimed.
// Layer insertion is transparent to consumers: both fwstate-sync and ACL
// configs link the map by name, and the dataplane resolves the fwtable
// through per-worker object ectx, so every consumer sees the new active
// layer without republishing.
//
// map.mu stays held across the insert and the stale-layer reclamation.
// ReclaimStaleLayers publishes its own generation barrier before freeing,
// because the fwmap chain is shared memory walked across all generations
// for fallback lookups and a worker can be mid-walk on a just-unlinked
// layer.
func (m *FWStateMapService) InsertLayer(
	ctx context.Context,
	req *fwstatepb.InsertLayerRequest,
) (*fwstatepb.InsertLayerResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "map name is required")
	}
	if err := ValidateWorkerCount(req.GetWorkerCount()); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	fwMap, ok := m.maps[name]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "fwstate-map %q not found", name)
	}

	if err := fwMap.Config().InsertLayer(
		req.GetIndexSize(),
		req.GetExtraBucketCount(),
		uint16(req.GetWorkerCount()),
	); err != nil {
		m.log.Error("failed to insert fwstate-map layer",
			zap.String("map", name), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to insert layer: %v", err)
	}

	// Layer insertion is transparent to consumers: both fwstate-sync and
	// ACL configs link the map by name, and the dataplane resolves the
	// fwtable through per-worker object ectx, so every consumer
	// automatically sees the new active layer without republishing. Only
	// stale-layer reclamation remains, which needs its own generation
	// barrier before freeing unlinked layer memory.
	mapCP := *fwMap.Config()
	m.ReclaimStaleLayers(fwMap.Config(), mapCP, uint64(time.Now().UnixNano()))

	m.log.Info("successfully inserted fwstate-map layer", zap.String("map", name))
	return &fwstatepb.InsertLayerResponse{}, nil
}

// ListEntries is a bidirectional stream that reads entries from a named
// fwstate-map's fwtable via cursor.
func (m *FWStateMapService) ListEntries(
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

		mapName := req.GetMapName()
		if mapName == "" {
			return status.Error(codes.InvalidArgument, "map_name is required")
		}

		count := clampBatchSize(req.GetBatchSize())

		m.mu.Lock()
		fwMap, ok := m.maps[mapName]
		if !ok {
			m.mu.Unlock()
			return status.Errorf(codes.NotFound, "fwstate-map %q not found", mapName)
		}

		mapCfg := *fwMap.Config()
		m.mu.Unlock()

		now := uint64(time.Now().UnixNano())
		backward := req.GetDirection() == fwstatepb.Direction_BACKWARD

		var entries []cfwstate.CursorEntry
		var newIndex int64
		var hasMore bool

		if backward {
			entries, newIndex, hasMore, err = mapCfg.ReadBackward(
				req.GetLayerIndex(),
				req.GetIndex(), req.GetIncludeExpired(),
				now, count,
			)
		} else {
			entries, newIndex, hasMore, err = mapCfg.ReadForward(
				req.GetLayerIndex(),
				req.GetIndex(), req.GetIncludeExpired(),
				now, count,
			)
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
			Generation: mapCfg.Generation(),
		}

		if err := stream.Send(resp); err != nil {
			return err
		}
	}
}

// ReclaimStaleLayers unlinks expired layers and waits for every dataplane
// worker to advance past the current generation.
//
// The two-phase reclaim mirrors the controlplane's config-generation
// update (lib/controlplane/config/zone.c:cp_config_gen_install):
// TrimStaleLayers atomically moves stale layers from the active chain to
// the fwtable's stale chain so new walks skip them, but a worker already
// mid-chain-walk (layermap_get_internal walks active→next→RO→RO…) can
// still be reading a just-unlinked layer. Publishing a new generation via
// the barrier (agent.UpdateObjects → cp_config_gen_install →
// dp_config_wait_for_gen) waits until every worker changed generation,
// guaranteeing no in-flight walk can touch the unlinked memory. The
// trimmed layers are then freed by the next TrimStaleLayers call, giving
// the dataplane one trim cycle to quiesce.
//
// If the barrier fails the stale chain is not advanced on the next trim,
// so the layers remain allocated for one more cycle — a rare leak, not a
// correctness or use-after-free issue. The caller must hold m.mu because
// trim mutates the shared map head chain. now is real-time nanoseconds,
// matching the domain the dataplane stamps layer deadlines in.
func (m *FWStateMapService) ReclaimStaleLayers(
	trimmer MapLayerTrimmer,
	mapCP cfwstate.MapObjectConfig,
	now uint64,
) {
	if err := trimmer.TrimStaleLayers(now); err != nil {
		m.log.Error(
			"failed to trim stale layers; stale chain not advanced",
			zap.Error(err),
		)
		return
	}
	if err := m.barrier(mapCP); err != nil {
		m.log.Error(
			"generation barrier failed after layer trim; stale chain not advanced on next trim",
			zap.Error(err),
		)
		return
	}
}

func ValidateWorkerCount(workerCount uint32) error {
	if workerCount == 0 {
		return status.Error(codes.InvalidArgument, "worker_count must be greater than zero")
	}
	if workerCount > maxWorkerCount {
		return status.Errorf(codes.InvalidArgument, "worker_count %d exceeds maximum %d", workerCount, maxWorkerCount)
	}
	return nil
}

func MapStatsToProto(stats mapStats) *fwstatepb.MapStats {
	return &fwstatepb.MapStats{
		IndexSize:        stats.IndexSize,
		ExtraBucketCount: stats.ExtraBucketCount,
		MaxChainLength:   stats.MaxChainLength,
		LayerCount:       stats.LayerCount,
		TotalElements:    stats.TotalElements,
		MaxDeadline:      stats.MaxDeadline,
		MemoryUsed:       stats.MemoryUsed,
	}
}

// ConfigsUsingMap returns the names of sync configs that reference the
// given map name (as either their v4 or v6 fwtable). Implements
// MapConsumer.ConfigsUsingMap on the FWStateService side for DeleteMap
// consumer checking.
func (m *FWStateService) ConfigsUsingMap(mapName string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	names := make([]string, 0, len(m.configs))
	for name, config := range m.configs {
		if config.UsesFwtable(mapName) {
			names = append(names, name)
		}
	}
	return names
}

// kindFromProto converts the proto Kind enum to the cfwstate Kind used by
// the C API.
func kindFromProto(kind fwstatepb.Kind) cfwstate.Kind {
	switch kind {
	case fwstatepb.Kind_V6:
		return cfwstate.KindV6
	default:
		return cfwstate.KindV4
	}
}

// NewFWStateMapServiceForTest creates an FWStateMapService without a live
// shared-memory agent, for exercising stale-layer reclamation helpers.
//
// syncConsumer and aclConsumer may be nil when the test does not exercise
// consumer fan-out. barrier may be nil when the test does not exercise
// stale-layer reclamation. A logger is supplied via the options pattern
// (WithMapLog); it defaults to a no-op logger when omitted.
func NewFWStateMapServiceForTest(
	syncConsumer, aclConsumer MapConsumer,
	barrier func(cfwstate.MapObjectConfig) error,
	options ...MapOption,
) *FWStateMapService {
	opts := newMapOptions()
	for _, option := range options {
		option(opts)
	}
	m := &FWStateMapService{
		maps:         map[string]*FWStateMap{},
		syncConsumer: syncConsumer,
		aclConsumer:  aclConsumer,
		log:          opts.Log,
	}
	if barrier != nil {
		m.barrier = barrier
	}
	return m
}

// NewFWStateServiceForTest creates an FWStateService without a live
// shared-memory agent, for exercising ConfigsUsingMap lookup logic.
func NewFWStateServiceForTest() *FWStateService {
	return &FWStateService{
		configs: map[string]*FwStateConfig{},
	}
}

// PutConfigForTest stores a minimal config entry keyed by name with the
// given fwtable names, without compiling a sync config or resolving a map.
func (m *FWStateService) PutConfigForTest(name, fwtableNameV4, fwtableNameV6 string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.configs[name] = &FwStateConfig{
		fwtableNameV4: fwtableNameV4,
		fwtableNameV6: fwtableNameV6,
	}
}
