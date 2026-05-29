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

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb"
)

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

// FWStateService implements the gRPC service for FWState management.
type FWStateService struct {
	fwstatepb.UnimplementedFWStateServiceServer

	mu          sync.Mutex
	agent       *ffi.Agent
	configs     map[string]*FwStateConfig
	aclProvider ACLServiceProvider

	// Pending outdated layers to be freed after successful UpdateModules
	pendingOutdatedLayers []*OutdatedLayers

	log *zap.Logger
}

// NewFWStateService creates a new FWState service
func NewFWStateService(agent *ffi.Agent, aclProvider ACLServiceProvider, log *zap.Logger) *FWStateService {
	return &FWStateService{
		agent:       agent,
		configs:     make(map[string]*FwStateConfig),
		aclProvider: aclProvider,
		log:         log,
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

	m.log.Debug("update fwstate config", zap.String("config", name))

	m.mu.Lock()
	defer m.mu.Unlock()

	oldConfig := m.configs[name]

	newConfig, err := NewFWStateModuleConfig(m.agent, name)
	if err != nil {
		m.log.Error("failed to create fwstate config",
			zap.String("config", name),
			zap.Error(err),
		)
		return nil, status.Errorf(codes.Internal, "failed to create fwstate config: %v", err)
	}
	if oldConfig != nil {
		newConfig.PropagateConfig(oldConfig)

		// Trim stale layers from the transferred configuration
		// Layers with expired deadlines will be collected and added to pending list
		// They will be freed after successful UpdateModules
		now := uint64(time.Now().UnixNano())
		outdatedLayers := newConfig.TrimStaleLayers(now)
		if outdatedLayers == nil {
			// Only nil on memory allocation failure
			newConfig.DetachMaps()
			newConfig.Free()
			m.log.Error("failed to allocate memory for outdated layers", zap.String("config", name))
			return nil, status.Error(codes.Internal, "failed to allocate memory for outdated layer list")
		}
		// Always add to pending list - will be freed after successful UpdateModules
		m.pendingOutdatedLayers = append(m.pendingOutdatedLayers, outdatedLayers)
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

	if err = newConfig.CreateMaps(req.MapConfig, uint16(dpConfig.WorkerCount())); err != nil {
		newConfig.DetachMaps() // in order not to pull them out from under the feet of another module
		newConfig.Free()
		m.log.Error("failed to create fwstate maps", zap.String("config", name), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to create fwstate maps: %v", err)
	}

	m.log.Debug("update fwstate module config", zap.String("config", name))

	// Rebuild all linked ACL configs against the new fwstate config and publish
	// both atomically.
	//
	// RelinkConfigs holds the ACL lock for the entire window.
	if err := m.aclProvider.RelinkConfigs(newConfig, func(linkedFFI []ffi.ModuleConfig) error {
		return m.agent.UpdateModules(append(linkedFFI, newConfig.AsFFIModule()))
	}); err != nil {
		newConfig.DetachMaps()
		newConfig.Free()
		m.log.Error("failed to relink ACL configs", zap.String("config", name), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to relink ACL configs: %v", err)
	}

	// Drain pending outdated layers after successful UpdateModules
	// This is safe because dataplane now uses the new configuration
	for _, pending := range m.pendingOutdatedLayers {
		newConfig.FreeOutdatedLayers(pending)
	}
	m.pendingOutdatedLayers = nil

	if oldConfig != nil {
		oldConfig.DetachMaps()
		oldConfig.Free()
	}

	m.configs[name] = newConfig

	m.log.Info("successfully updated FWState module", zap.String("config", name))

	return &fwstatepb.UpdateConfigResponse{}, nil
}

func (m *FWStateService) LinkFWState(
	ctx context.Context,
	req *fwstatepb.LinkFWStateRequest,
) (*fwstatepb.LinkFWStateResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

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

	// Check that fwstate config exists
	fwstateConfig, ok := m.configs[fwstateName]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "FWState config %q not found", fwstateName)
	}

	// Link the given ACL configs to this fwstate and publish both atomically.
	// LinkConfigs holds the ACL lock for the entire window.
	if err := m.aclProvider.LinkConfigs(aclConfigNames, fwstateConfig, func(linkedFFI []ffi.ModuleConfig) error {
		return m.agent.UpdateModules(append(linkedFFI, fwstateConfig.AsFFIModule()))
	}); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to link ACL configs: %v", err)
	}

	m.log.Info("successfully linked FWState to ACL configs",
		zap.String("fwstate", fwstateName),
		zap.Strings("acl_configs", aclConfigNames),
	)

	return &fwstatepb.LinkFWStateResponse{}, nil
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

	// LinkedConfigNames is self-locking.
	linkedACLs := m.aclProvider.LinkedConfigNames(name)

	response := &fwstatepb.ShowConfigResponse{
		Name:       name,
		MapConfig:  config.GetMapConfig(),
		SyncConfig: config.GetSyncConfig(),
		LinkedAcls: linkedACLs,
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

	if err := m.agent.DeleteModuleConfig(name); err != nil {
		return nil, status.Errorf(codes.Internal, "could not delete fwstate module config '%s': %v", name, err)
	}

	m.log.Info("successfully deleted FWState module config", zap.String("name", name))
	config.Free()

	delete(m.configs, name)

	return &fwstatepb.DeleteConfigResponse{}, nil
}

func (m *FWStateService) GetStats(
	ctx context.Context,
	req *fwstatepb.GetStatsRequest,
) (*fwstatepb.GetStatsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	config, ok := m.configs[name]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	// Get stats for both IPv4 and IPv6 maps
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

		count := req.GetBatchSize()
		if count == 0 {
			count = 100
		}

		m.mu.Lock()
		config, ok := m.configs[configName]
		if !ok {
			m.mu.Unlock()
			return status.Errorf(codes.NotFound, "config %q not found", configName)
		}

		now := uint64(time.Now().UnixNano())
		backward := req.GetDirection() == fwstatepb.Direction_BACKWARD

		var entries []CursorEntry
		var newIndex int64
		var hasMore bool

		if backward {
			entries, newIndex, hasMore, err = config.ReadBackward(
				req.GetIsIpv6(), req.GetLayerIndex(),
				req.GetIndex(), req.GetIncludeExpired(),
				now, count,
			)
		} else {
			entries, newIndex, hasMore, err = config.ReadForward(
				req.GetIsIpv6(), req.GetLayerIndex(),
				req.GetIndex(), req.GetIncludeExpired(),
				now, count,
			)
		}
		generation := config.Generation()
		m.mu.Unlock()

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

// validateSyncConfig validates that required sync config fields are set
func validateSyncConfig(cfg *fwstatepb.SyncConfig) error {
	var missing []string

	// Check src_addr (16 bytes for IPv6)
	if len(cfg.GetSrcAddr().GetAddr()) != 16 || isAllZeroBytes(cfg.GetSrcAddr().GetAddr()) {
		missing = append(missing, "src_addr")
	}

	// Check dst_ether (6 bytes for MAC)
	if len(cfg.DstEther) != 6 || isAllZeroBytes(cfg.DstEther) {
		missing = append(missing, "dst_ether")
	}

	// Check that at least one destination pair is configured
	hasMulticast := len(cfg.GetDstAddrMulticast().GetAddr()) == 16 && !isAllZeroBytes(cfg.GetDstAddrMulticast().GetAddr()) && cfg.PortMulticast != 0
	hasUnicast := len(cfg.GetDstAddrUnicast().GetAddr()) == 16 && !isAllZeroBytes(cfg.GetDstAddrUnicast().GetAddr()) && cfg.PortUnicast != 0

	if !hasMulticast && !hasUnicast {
		missing = append(missing, "dst_addr_multicast+port_multicast or dst_addr_unicast+port_unicast")
	}

	if len(missing) > 0 {
		return status.Errorf(codes.InvalidArgument, "missing required sync config fields: %v", missing)
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
