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

// ACLServiceProvider is the interface through which the fwstate service drives
// ACL config lifecycle. Implementations must be safe for concurrent use.
type ACLServiceProvider interface {
	// LinkedConfigNames returns ACL config names linked to the given fwstate
	// config. Implementations lock internally.
	LinkedConfigNames(fwstateConfigName string) []string

	// RelinkConfigs rebuilds all ACL configs currently linked to fwstateConfig
	// and invokes publish with their FFI handles. publish is called even when
	// there are no linked configs (with nil) so the caller can still publish
	// its own configs atomically.
	RelinkConfigs(
		fwstateConfig *FwStateConfig,
		publish func(linkedFFI []ffi.ModuleConfig) error,
	) error

	// LinkConfigs links the given explicit list of ACL config names to
	// fwstateConfig and invokes publish with their FFI handles so the caller
	// can publish the combined update atomically.
	LinkConfigs(
		names []string,
		fwstateConfig *FwStateConfig,
		publish func(linkedFFI []ffi.ModuleConfig) error,
	) error
}

// mutationObserver is optional instrumentation for verifying updateMu
// acquisition order. It observes synchronization but never controls it.
type mutationObserver interface {
	ObserveFWStateMutation(operation, phase string)
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

	// updateMu serializes mutations and owns pendingOutdatedLayers. stateMu
	// protects configs and published handle lifetime. The only valid order is
	// updateMu followed by stateMu.
	updateMu    sync.Mutex
	stateMu     sync.RWMutex
	agent       *ffi.Agent
	configs     map[string]*FwStateConfig
	aclProvider ACLServiceProvider
	metrics     *grpcmetrics.ServerMetrics

	// Pending outdated layers to be freed after successful UpdateModules
	pendingOutdatedLayers []*OutdatedLayers

	log *zap.Logger
}

// NewFWStateService creates a new FWState service.
//
// When the WithMetrics option is supplied, gRPC call metrics are collected and
// exposed through the module's MetricsService.
func NewFWStateService(
	agent *ffi.Agent,
	aclProvider ACLServiceProvider,
	options ...Option,
) *FWStateService {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	m := &FWStateService{
		agent:       agent,
		configs:     make(map[string]*FwStateConfig),
		aclProvider: aclProvider,
		log:         opts.Log,
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
	if req.MapConfig == nil {
		return nil, status.Error(codes.InvalidArgument, "map_config is required")
	}
	if err := validateSyncPorts(req.SyncConfig); err != nil {
		return nil, err
	}

	m.log.Debug("update fwstate config", zap.String("config", name))

	err := m.withMutation("update", func() error {
		newConfig, err := NewFWStateModuleConfig(m.agent, name)
		if err != nil {
			m.log.Error("failed to create fwstate config",
				zap.String("config", name),
				zap.Error(err),
			)
			return status.Errorf(codes.Internal, "failed to create fwstate config: %v", err)
		}

		oldConfig, err := m.prepareUpdate(name, newConfig, req)
		if err != nil {
			return err
		}

		m.log.Debug("update fwstate module config", zap.String("config", name))

		// Rebuild all linked ACL configs against the new fwstate config and publish
		// both atomically.
		//
		// RelinkConfigs holds each linked ACL config name's lock for the entire
		// window, so it never blocks a read or a compile on an unrelated name.
		if err := m.aclProvider.RelinkConfigs(newConfig, func(linkedFFI []ffi.ModuleConfig) error {
			return m.publishUpdate(name, oldConfig, newConfig, linkedFFI)
		}); err != nil {
			newConfig.DetachMaps()
			newConfig.Free()
			m.log.Error("failed to relink ACL configs", zap.String("config", name), zap.Error(err))
			return status.Errorf(codes.Internal, "failed to relink ACL configs: %v", err)
		}

		if oldConfig != nil {
			oldConfig.DetachMaps()
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

func (m *FWStateService) prepareUpdate(
	name string,
	newConfig *FwStateConfig,
	req *fwstatepb.UpdateConfigRequest,
) (*FwStateConfig, error) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	oldConfig := m.configs[name]
	if oldConfig != nil {
		newConfig.PropagateConfig(oldConfig)
	}

	// Set sync config
	newConfig.SetSyncConfig(req.SyncConfig)

	// Validate sync config after setting
	syncConfig := newConfig.GetSyncConfig()
	if err := validateSyncConfig(syncConfig); err != nil {
		newConfig.DetachMaps()
		newConfig.Free()
		m.log.Error("invalid sync config", zap.String("config", name), zap.Error(err))
		return nil, status.Errorf(codes.InvalidArgument, "invalid sync config: %v", err)
	}

	dpConfig := m.agent.DPConfig()

	if err := newConfig.CreateMaps(req.MapConfig, uint16(dpConfig.WorkerCount())); err != nil {
		newConfig.DetachMaps() // in order not to pull them out from under the feet of another module
		newConfig.Free()
		m.log.Error("failed to create fwstate maps", zap.String("config", name), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to create fwstate maps: %v", err)
	}

	return oldConfig, nil
}

func (m *FWStateService) publishUpdate(
	name string,
	oldConfig, newConfig *FwStateConfig,
	linkedFFI []ffi.ModuleConfig,
) error {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	modules := append(linkedFFI, newConfig.AsFFIModule())
	if err := m.agent.UpdateModules(modules); err != nil {
		return err
	}
	m.freePendingOutdatedLayers(newConfig)
	m.configs[name] = newConfig

	if oldConfig == nil {
		return nil
	}

	// Publish before trimming so a failed update leaves the old chain intact.
	// Readers stay behind stateMu while the extra generation barrier makes
	// detached layers safe to reclaim.
	outdatedLayers := m.trimStaleLayers(name, newConfig)
	if outdatedLayers == nil {
		return nil
	}
	m.pendingOutdatedLayers = append(m.pendingOutdatedLayers, outdatedLayers)
	if err := m.agent.UpdateModules(modules); err != nil {
		m.log.Warn("deferred stale fwstate layer reclamation",
			zap.String("config", name),
			zap.Error(err),
		)
		return nil
	}
	m.freePendingOutdatedLayers(newConfig)
	return nil
}

func (m *FWStateService) trimStaleLayers(
	name string,
	config *FwStateConfig,
) *OutdatedLayers {
	now := uint64(time.Now().UnixNano())
	outdatedLayers, err := config.TrimStaleLayers(now)
	if err == nil {
		return outdatedLayers
	}
	if outdatedLayers == nil {
		m.log.Error("failed to trim stale fwstate layers",
			zap.String("config", name),
			zap.Error(err),
		)
		return nil
	}

	// Publishing the partially trimmed chain releases the collected layers
	// and leaves the rest linked for the next update.
	m.log.Warn("trimmed stale layers only partially",
		zap.String("config", name),
		zap.Error(err),
	)
	return outdatedLayers
}

func (m *FWStateService) freePendingOutdatedLayers(config *FwStateConfig) {
	for _, pending := range m.pendingOutdatedLayers {
		config.FreeOutdatedLayers(pending)
	}
	m.pendingOutdatedLayers = nil
}

func (m *FWStateService) LinkFWState(
	ctx context.Context,
	req *fwstatepb.LinkFWStateRequest,
) (*fwstatepb.LinkFWStateResponse, error) {
	fwstateName := req.GetFwstateName()
	if fwstateName == "" {
		return nil, status.Error(codes.InvalidArgument, "fwstate name is required")
	}

	aclConfigNames := req.GetAclConfigNames()
	if len(aclConfigNames) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one ACL config name is required")
	}

	// Check for duplicates in ACL config names
	seen := make(map[string]bool)
	for _, name := range aclConfigNames {
		if seen[name] {
			return nil, status.Errorf(codes.InvalidArgument, "duplicate ACL config name: %q", name)
		}
		seen[name] = true
	}

	err := m.withMutation("link", func() error {
		fwstateConfig, ok := m.configForMutation(fwstateName)
		if !ok {
			return status.Errorf(codes.NotFound, "FWState config %q not found", fwstateName)
		}

		// Link the given ACL configs to this fwstate and publish both atomically.
		// LinkConfigs holds each named ACL config's lock for the entire window,
		// so it never blocks a read or a compile on an unrelated name.
		if err := m.aclProvider.LinkConfigs(aclConfigNames, fwstateConfig, func(linkedFFI []ffi.ModuleConfig) error {
			if err := m.agent.UpdateModules(append(linkedFFI, fwstateConfig.AsFFIModule())); err != nil {
				return err
			}
			m.freePendingOutdatedLayers(fwstateConfig)
			return nil
		}); err != nil {
			return status.Errorf(codes.Internal, "failed to link ACL configs: %v", err)
		}

		m.log.Info("successfully linked FWState to ACL configs",
			zap.String("fwstate", fwstateName),
			zap.Strings("acl_configs", aclConfigNames),
		)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &fwstatepb.LinkFWStateResponse{}, nil
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

	mapConfig, syncConfig, ok := m.configSnapshot(name)
	if !ok {
		if req.OkIfNotFound {
			return nil, nil
		}
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	// LinkedConfigNames is self-locking: a brief RLock, never blocked behind
	// an in-flight ACL compile.
	linkedACLs := m.aclProvider.LinkedConfigNames(name)

	response := &fwstatepb.ShowConfigResponse{
		Name:       name,
		MapConfig:  mapConfig,
		SyncConfig: syncConfig,
		LinkedAcls: linkedACLs,
	}

	return response, nil
}

func (m *FWStateService) configSnapshot(
	name string,
) (*fwstatepb.MapConfig, *fwstatepb.SyncConfig, bool) {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()

	config, ok := m.configs[name]
	if !ok {
		return nil, nil, false
	}

	return config.GetMapConfig(), config.GetSyncConfig(), true
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
		m.freePendingOutdatedLayers(config)
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
	if observer, ok := m.aclProvider.(mutationObserver); ok {
		observer.ObserveFWStateMutation(operation, phase)
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

	mapsStats := config.GetMapsStats()

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
) ([]CursorEntry, int64, bool, uint64, error) {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()

	configName := req.GetConfigName()
	config, ok := m.configs[configName]
	if !ok {
		return nil, 0, false, 0, status.Errorf(codes.NotFound, "config %q not found", configName)
	}

	now := uint64(time.Now().UnixNano())
	readEntries := config.ReadForward
	if req.GetDirection() == fwstatepb.Direction_BACKWARD {
		readEntries = config.ReadBackward
	}
	entries, newIndex, hasMore, err := readEntries(
		req.GetIsIpv6(), req.GetLayerIndex(),
		req.GetIndex(), req.GetIncludeExpired(),
		now, count,
	)

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
