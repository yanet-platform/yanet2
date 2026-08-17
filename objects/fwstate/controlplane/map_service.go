package fwstatemap

// FWStateMapService.mu is the only lock in this service. Every RPC and
// ReclaimStaleLayers acquires it for the whole operation: CreateMap and
// InsertLayer mutate the in-memory map registry and the shared-memory
// layer chain atomically, and ListEntries runs its cursor read under it
// so a concurrent DeleteMap or layer reclamation cannot free the fwtable
// or a layer mid-read. There are no collaborating services to order
// against in this standalone objects controlplane.

import (
	"context"
	"io"
	"math"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/go/grpcmetrics"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/objects/fwstate/bindings/go/cfwstate"
	fwstatemappb "github.com/yanet-platform/yanet2/objects/fwstate/controlplane/fwstatemappb/v1"
)

// maxWorkerCount is the highest value accepted for worker_count, matching
// the width of the C-side uint16 parameter of fwstate_map insert_layer.
const maxWorkerCount uint32 = 65535

const maxMapNameLen = 80

const (
	// DefaultListEntriesBatchSize is the batch size used when the caller
	// sends zero in the request.
	DefaultListEntriesBatchSize uint32 = 100

	// MaxListEntriesBatchSize caps the number of entries fetched per
	// ListEntries round-trip to prevent unbounded allocation under the
	// service mutex.
	MaxListEntriesBatchSize uint32 = 10000
)

// ClampBatchSize returns a batch size that is within the allowed range:
// zero is replaced with DefaultListEntriesBatchSize, and values above
// MaxListEntriesBatchSize are clamped to MaxListEntriesBatchSize.
func ClampBatchSize(n uint32) uint32 {
	if n == 0 {
		return DefaultListEntriesBatchSize
	}
	if n > MaxListEntriesBatchSize {
		return MaxListEntriesBatchSize
	}
	return n
}

// mapStats is the bindings-level stats shape carried across CGo.
type mapStats = cfwstate.MapStats

// MapLayerReclaimer parks and releases a fwtable's stale layers around
// the generation barrier.
//
// [cfwstate.MapObjectConfig] satisfies this interface; the seam lets unit
// tests record reclamation calls without a live shared-memory handle.
type MapLayerReclaimer interface {
	UnlinkStaleLayers(now uint64) error
	FreeStaleLayers() error
}

// Compile-time assertion that [cfwstate.MapObjectConfig] satisfies
// [MapLayerReclaimer].
var _ MapLayerReclaimer = (*cfwstate.MapObjectConfig)(nil)

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

// WithLog sets the map service logger.
func WithLog(log *zap.Logger) MapOption {
	return func(o *mapOptions) {
		o.Log = log
	}
}

// WithMetrics enables collection of gRPC call metrics for the map
// service using the supplied factory.
func WithMetrics(factory grpcmetrics.Factory) MapOption {
	return func(o *mapOptions) {
		o.Metrics = factory
	}
}

// NewMetricsFactory returns a [grpcmetrics.Factory] scoped to the
// FWStateMapService.
func NewMetricsFactory(extra ...grpcmetrics.Option) grpcmetrics.Factory {
	opts := make([]grpcmetrics.Option, 0, len(extra)+2)
	opts = append(opts, grpcmetrics.WithLabeler(MapLabeler))
	opts = append(opts, grpcmetrics.WithServiceFilter(
		func(service string) bool {
			return service == ServiceName
		},
	))
	opts = append(opts, extra...)
	return grpcmetrics.NewFactory(opts...)
}

// MapLabeler extracts the map name from map-scoped requests for gRPC
// metrics labeling.
func MapLabeler(fullMethod string, req any) metrics.Labels {
	switch r := req.(type) {
	case *fwstatemappb.CreateMapRequest:
		return metrics.Labels{"map": r.GetName()}
	case *fwstatemappb.DeleteMapRequest:
		return metrics.Labels{"map": r.GetName()}
	case *fwstatemappb.GetMapStatsRequest:
		return metrics.Labels{"map": r.GetName()}
	case *fwstatemappb.InsertLayerRequest:
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
	fwstatemappb.UnimplementedFWStateMapServiceServer

	mu      sync.Mutex
	agent   *ffi.Agent
	maps    map[string]*FWStateMap
	metrics *grpcmetrics.ServerMetrics

	// barrier advances the dataplane to a new config generation and waits
	// for every worker to observe it, acting as the RCU grace period that
	// must elapse between unlinking stale layers and freeing their memory.
	//
	// Defaults to republishing the map object via Publish
	// (agent_update_objects → cp_config_gen_install → dp_config_wait_for_gen);
	// tests override it to record barrier calls without a live agent.
	barrier func(cfwstate.MapObjectConfig) error
	log     *zap.Logger
}

// NewFWStateMapService creates a new FWStateMapService.
func NewFWStateMapService(
	agent *ffi.Agent,
	options ...MapOption,
) *FWStateMapService {
	opts := newMapOptions()
	for _, o := range options {
		o(opts)
	}

	m := &FWStateMapService{
		agent: agent,
		maps:  map[string]*FWStateMap{},
		log:   opts.Log,
	}
	m.barrier = m.publishGeneration

	if opts.Metrics != nil {
		m.metrics = opts.Metrics(m.mapRetention)
	}

	return m
}

// publishGeneration upserts the map object into a new config generation
// and blocks until every dataplane worker has advanced to it.
func (m *FWStateMapService) publishGeneration(mapCP cfwstate.MapObjectConfig) error {
	return mapCP.Publish(m.agent)
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
	req *fwstatemappb.CreateMapRequest,
) (*fwstatemappb.CreateMapResponse, error) {
	name := req.GetName()
	if err := ValidateMapName(name); err != nil {
		return nil, err
	}

	workerCount, err := ResolveCreateWorkerCount(
		req.GetWorkerCount(), m.dpWorkerCountOf(),
	)
	if err != nil {
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
		workerCount,
	); err != nil {
		mapConfig.Free()
		m.log.Error("failed to create fwstate-map table",
			zap.String("map", name), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to create fwstate-map table: %v", err)
	}

	if err := mapConfig.Publish(m.agent); err != nil {
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
	return &fwstatemappb.CreateMapResponse{}, nil
}

// DeleteMap removes a named fwstate-map.
//
// TODO: once module configs (fwstate sync, ACL) link these objects, refuse
// deletion while any consumer references the map name, mirroring the
// module-service consumer scan. For now the objects controlplane owns no
// consumers and removes the object unconditionally when the name exists.
func (m *FWStateMapService) DeleteMap(
	ctx context.Context,
	req *fwstatemappb.DeleteMapRequest,
) (*fwstatemappb.DeleteMapResponse, error) {
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

	if err := cfwstate.DeleteMapObject(
		m.agent, fwMap.Config().Kind().ObjectType(), name,
	); err != nil {
		m.log.Error("failed to delete fwstate-map",
			zap.String("map", name), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to delete fwstate-map: %v", err)
	}

	fwMap.Config().Free()
	delete(m.maps, name)

	m.log.Info("successfully deleted fwstate-map", zap.String("map", name))
	return &fwstatemappb.DeleteMapResponse{}, nil
}

// ListMaps returns the names of all registered fwstate-map objects.
func (m *FWStateMapService) ListMaps(
	ctx context.Context,
	req *fwstatemappb.ListMapsRequest,
) (*fwstatemappb.ListMapsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	response := &fwstatemappb.ListMapsResponse{
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
	req *fwstatemappb.GetMapStatsRequest,
) (*fwstatemappb.GetMapStatsResponse, error) {
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

	return &fwstatemappb.GetMapStatsResponse{
		Stats: MapStatsToProto(fwMap.Config().GetStats()),
	}, nil
}

// InsertLayer inserts a new layer into the fwtable chain of a named
// fwstate-map.
//
// The insert and the stale-layer reclamation are separate routines
// bracketed by one config generation update: the new layer becomes the
// active head, expired tails are unlinked, and a single published
// generation that every worker advanced past both makes the rotation
// durable and gates the release of the unlinked layers. mu stays held
// across both.
func (m *FWStateMapService) InsertLayer(
	ctx context.Context,
	req *fwstatemappb.InsertLayerRequest,
) (*fwstatemappb.InsertLayerResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "map name is required")
	}
	workerCount, err := ResolveCreateWorkerCount(
		req.GetWorkerCount(), m.dpWorkerCountOf(),
	)
	if err != nil {
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
		workerCount,
	); err != nil {
		m.log.Error("failed to insert fwstate-map layer",
			zap.String("map", name), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to insert layer: %v", err)
	}

	mapCP := *fwMap.Config()
	m.ReclaimStaleLayers(fwMap.Config(), mapCP, uint64(time.Now().UnixNano()))

	m.log.Info("successfully inserted fwstate-map layer", zap.String("map", name))
	return &fwstatemappb.InsertLayerResponse{}, nil
}

// ListEntries is a bidirectional stream that reads entries from a named
// fwstate-map's fwtable via cursor.
func (m *FWStateMapService) ListEntries(
	stream grpc.BidiStreamingServer[fwstatemappb.ListEntriesRequest, fwstatemappb.ListEntriesResponse],
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

		count := ClampBatchSize(req.GetBatchSize())
		if req.GetDirection() == fwstatemappb.Direction_BACKWARD {
			backward = true
		} else if req.GetDirection() == fwstatemappb.Direction_FORWARD {
			backward = false
		} else {
			return status.Error(codes.InvalidArgument, "invalid direction")
		}

		index, err := ResolveReadIndex(backward, req.GetIndex())
		if err != nil {
			return err
		}

		now := uint64(time.Now().UnixNano())

		m.mu.Lock()
		fwMap, ok := m.maps[mapName]
		if !ok {
			m.mu.Unlock()
			return status.Errorf(codes.NotFound, "fwstate-map %q not found", mapName)
		}

		mapCfg := *fwMap.Config()

		var entries []cfwstate.CursorEntry
		var newIndex int64
		var hasMore bool

		if backward {
			entries, newIndex, hasMore, err = mapCfg.ReadBackward(
				req.GetLayerIndex(),
				index, req.GetIncludeExpired(),
				now, count,
			)
		} else {
			entries, newIndex, hasMore, err = mapCfg.ReadForward(
				req.GetLayerIndex(),
				index, req.GetIncludeExpired(),
				now, count,
			)
		}

		if err == nil && backward && newIndex == 0 {
			var tail []cfwstate.CursorEntry
			tail, newIndex, hasMore, err = mapCfg.ReadBackward(
				req.GetLayerIndex(),
				0, req.GetIncludeExpired(),
				now, 1,
			)
			entries = append(entries, tail...)
		}

		if backward && len(entries) == 0 {
			hasMore = false
		}

		m.mu.Unlock()

		if err != nil {
			return status.Errorf(codes.Internal, "cursor read failed: %v", err)
		}

		pbEntries := make([]*fwstatemappb.FwStateEntry, 0, len(entries))
		for idx := range entries {
			pbEntries = append(pbEntries, fwstatemappb.FromCursorEntry(entries[idx]))
		}

		resp := &fwstatemappb.ListEntriesResponse{
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

// ReclaimStaleLayers unlinks expired layers, advances every dataplane
// worker past a new config generation, then frees the unlinked layers.
//
// UnlinkStaleLayers atomically moves stale layers from the active chain
// to the fwtable's stale chain so new walks skip them, but a worker
// already mid-chain-walk can still be reading a just-unlinked layer.
// The barrier publishes a new generation and waits until every worker
// changed generation, proving no in-flight walk can touch the unlinked
// memory; only then does FreeStaleLayers release it.
//
// If the barrier fails the parked layers stay allocated and are freed
// by a later round after a successful barrier — a rare leak, never a
// use-after-free. The caller must hold mu because unlink mutates the
// shared map head chain. now is real-time nanoseconds, matching the
// domain the dataplane stamps layer deadlines in.
func (m *FWStateMapService) ReclaimStaleLayers(
	reclaimer MapLayerReclaimer,
	mapCP cfwstate.MapObjectConfig,
	now uint64,
) {
	if err := reclaimer.UnlinkStaleLayers(now); err != nil {
		m.log.Error(
			"failed to unlink stale layers; layers left on the active chain",
			zap.Error(err),
		)
		return
	}
	if err := m.barrier(mapCP); err != nil {
		m.log.Error(
			"generation barrier failed after layer unlink; parked layers retained for a later round",
			zap.Error(err),
		)
		return
	}
	if err := reclaimer.FreeStaleLayers(); err != nil {
		m.log.Error(
			"failed to free unlinked stale layers",
			zap.Error(err),
		)
		return
	}
}

// ValidateWorkerCount rejects zero and out-of-range worker_count values.
func ValidateWorkerCount(workerCount uint32) error {
	if workerCount == 0 {
		return status.Error(codes.InvalidArgument, "worker_count must be greater than zero")
	}
	if workerCount > maxWorkerCount {
		return status.Errorf(codes.InvalidArgument, "worker_count %d exceeds maximum %d", workerCount, maxWorkerCount)
	}
	return nil
}

func (m *FWStateMapService) dpWorkerCountOf() func() uint32 {
	if m.agent == nil {
		return nil
	}
	return func() uint32 { return m.agent.DPConfig().WorkerCount() }
}

// ResolveCreateWorkerCount settles the per-worker sizing for CreateMap and InsertLayer, deriving a zero count from the dataplane's worker count and rejecting an explicit mismatch.
func ResolveCreateWorkerCount(
	workerCount uint32,
	dpWorkerCount func() uint32,
) (uint16, error) {
	if dpWorkerCount != nil {
		dpCount := dpWorkerCount()
		if workerCount == 0 {
			workerCount = dpCount
		} else if workerCount != dpCount {
			return 0, status.Errorf(
				codes.InvalidArgument,
				"worker_count %d does not match the dataplane worker count %d",
				workerCount, dpCount,
			)
		}
	}
	if err := ValidateWorkerCount(workerCount); err != nil {
		return 0, err
	}
	return uint16(workerCount), nil
}

// ValidateMapName rejects names that cannot round-trip through the fixed-size C object registry.
func ValidateMapName(name string) error {
	if name == "" {
		return status.Error(codes.InvalidArgument, "map name is required")
	}
	if len(name) >= maxMapNameLen {
		return status.Errorf(codes.InvalidArgument, "map name must be shorter than %d bytes", maxMapNameLen)
	}
	if strings.IndexByte(name, 0) != -1 {
		return status.Error(codes.InvalidArgument, "map name must not contain NUL bytes")
	}
	return nil
}

// ResolveReadIndex rejects negative forward cursors and maps a zero backward cursor to the scan's upper bound.
func ResolveReadIndex(backward bool, index int64) (int64, error) {
	if backward {
		if index == 0 {
			return math.MaxInt64, nil
		}
		return index, nil
	}
	if index < 0 {
		return 0, status.Error(codes.InvalidArgument, "index must not be negative for forward reads")
	}
	return index, nil
}

// MapStatsToProto converts bindings-level map stats into the proto form.
func MapStatsToProto(stats mapStats) *fwstatemappb.MapStats {
	return &fwstatemappb.MapStats{
		IndexSize:        stats.IndexSize,
		ExtraBucketCount: stats.ExtraBucketCount,
		MaxChainLength:   stats.MaxChainLength,
		LayerCount:       stats.LayerCount,
		TotalElements:    stats.TotalElements,
		MaxDeadline:      stats.MaxDeadline,
		MemoryUsed:       stats.MemoryUsed,
	}
}

// kindFromProto converts the proto Kind enum to the cfwstate Kind used by
// the C API.
func kindFromProto(kind fwstatemappb.Kind) cfwstate.Kind {
	switch kind {
	case fwstatemappb.Kind_V6:
		return cfwstate.KindV6
	default:
		return cfwstate.KindV4
	}
}

// NewFWStateMapServiceForTest creates an FWStateMapService without a live
// agent, for exercising stale-layer reclamation and the in-memory map
// registry.
//
// barrier may be nil when the test does not exercise stale-layer
// reclamation. A logger is supplied via the options pattern (WithLog); it
// defaults to a no-op logger when omitted.
func NewFWStateMapServiceForTest(
	barrier func(cfwstate.MapObjectConfig) error,
	options ...MapOption,
) *FWStateMapService {
	opts := newMapOptions()
	for _, option := range options {
		option(opts)
	}
	m := &FWStateMapService{
		maps: map[string]*FWStateMap{},
		log:  opts.Log,
	}
	if barrier != nil {
		m.barrier = barrier
	}
	return m
}
