package fwstate

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb"
)

type ACLConfigTransaction interface {
	Commit()
	Abort()
}

type ACLServiceProvider interface {
	Lock()
	Unlock()
	LinkedConfigNames(fwstateConfigName string) []string
	CreateACLConfigs(aclConfigs []string, fwstateConfig *FwStateConfig) ([]ffi.ModuleConfig, ACLConfigTransaction, error)
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

	log *zap.SugaredLogger
}

// NewFWStateService creates a new FWState service
func NewFWStateService(agent *ffi.Agent, aclProvider ACLServiceProvider, log *zap.SugaredLogger) *FWStateService {
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

	m.log.Debugw("update fwstate config", zap.String("config", name))

	m.mu.Lock()
	defer m.mu.Unlock()

	oldConfig := m.configs[name]

	newConfig, err := NewFWStateModuleConfig(m.agent, name)
	if err != nil {
		m.log.Errorw("failed to create fwstate config",
			zap.String("config", name),
			zap.Error(err),
		)
		return nil, status.Errorf(codes.Internal, "failed to create fwstate config: %v", err)
	}
	if oldConfig != nil {
		newConfig.PropogateConfig(oldConfig)

		// Trim stale layers from the transferred configuration
		// Layers with expired deadlines will be collected and added to pending list
		// They will be freed after successful UpdateModules
		now := uint64(time.Now().UnixNano())
		outdatedLayers := newConfig.TrimStaleLayers(now)
		if outdatedLayers == nil {
			// Only nil on memory allocation failure
			newConfig.DetachMaps()
			newConfig.Free()
			m.log.Errorw("failed to allocate memory for outdated layers", zap.String("config", name))
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
		m.log.Errorw("invalid sync config", zap.String("config", name), zap.Error(err))
		return nil, status.Errorf(codes.InvalidArgument, "invalid sync config: %v", err)
	}

	dpConfig := m.agent.DPConfig()

	if err = newConfig.CreateMaps(req.MapConfig, uint16(dpConfig.WorkerCount()), m.log); err != nil {
		newConfig.DetachMaps() // in order not to pull them out from under the feet of another module
		newConfig.Free()
		m.log.Errorw("failed to create fwstate maps", zap.String("config", name), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to create fwstate maps: %v", err)
	}

	m.log.Debugw("update fwstate module config", zap.String("config", name))

	// Get linked ACL configs and update them with new fwstate
	m.aclProvider.Lock()
	defer m.aclProvider.Unlock()
	linkedACLNames := m.aclProvider.LinkedConfigNames(name)

	var aclConfigsTx ACLConfigTransaction
	var newACLConfigs []ffi.ModuleConfig

	if len(linkedACLNames) > 0 {
		// Create new ACL configs linked to the new fwstate
		var err error
		newACLConfigs, aclConfigsTx, err = m.aclProvider.CreateACLConfigs(linkedACLNames, newConfig)
		if err != nil {
			newConfig.DetachMaps()
			newConfig.Free()
			m.log.Errorw("failed to create linked ACL configs", zap.String("config", name), zap.Error(err))
			return nil, status.Errorf(codes.Internal, "failed to create linked ACL configs: %v", err)
		}
	}

	// Combine fwstate and ACL configs for atomic update
	allConfigs := append(newACLConfigs, newConfig.AsFFIModule())

	if err := m.agent.UpdateModules(allConfigs); err != nil {
		if aclConfigsTx != nil {
			aclConfigsTx.Abort()
		}
		newConfig.DetachMaps()
		newConfig.Free()
		m.log.Errorw("failed to update modules", zap.String("config", name), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to update modules: %v", err)
	}

	// Commit ACL transaction if exists
	if aclConfigsTx != nil {
		aclConfigsTx.Commit()
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

	m.log.Infow("successfully updated FWState module", zap.String("config", name))

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

	// Lock ACL provider to work with ACL configs
	m.aclProvider.Lock()
	defer m.aclProvider.Unlock()

	// Create new ACL configs linked to this fwstate
	newACLConfigs, aclConfigsTx, err := m.aclProvider.CreateACLConfigs(aclConfigNames, fwstateConfig)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create ACL configs: %v", err)
	}

	// Combine fwstate config with all ACL configs for atomic update
	allConfigs := append(newACLConfigs, fwstateConfig.AsFFIModule())

	// Update all modules atomically
	if err := m.agent.UpdateModules(allConfigs); err != nil {
		aclConfigsTx.Abort()
		return nil, status.Errorf(codes.Internal, "failed to update modules: %v", err)
	}

	// Commit the transaction
	aclConfigsTx.Commit()

	m.log.Infow("successfully linked FWState to ACL configs",
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

	// Get linked ACL config names
	m.aclProvider.Lock()
	linkedACLs := m.aclProvider.LinkedConfigNames(name)
	m.aclProvider.Unlock()

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

	m.log.Infow("successfully deleted FWState module config", zap.String("name", name))
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
			IndexSize:        uint32(mapsStats.v4.index_size),
			ExtraBucketCount: uint32(mapsStats.v4.extra_bucket_count),
			MaxChainLength:   uint32(mapsStats.v4.max_chain_length),
			LayerCount:       uint32(mapsStats.v4.layer_count),
			TotalElements:    uint64(mapsStats.v4.total_elements),
			MaxDeadline:      uint64(mapsStats.v4.max_deadline),
			MemoryUsed:       uint64(mapsStats.v4.memory_used),
			Note:             "Statistics are currently shown for the first layer only",
		},
		Ipv6Stats: &fwstatepb.MapStats{
			IndexSize:        uint32(mapsStats.v6.index_size),
			ExtraBucketCount: uint32(mapsStats.v6.extra_bucket_count),
			MaxChainLength:   uint32(mapsStats.v6.max_chain_length),
			LayerCount:       uint32(mapsStats.v6.layer_count),
			TotalElements:    uint64(mapsStats.v6.total_elements),
			MaxDeadline:      uint64(mapsStats.v6.max_deadline),
			MemoryUsed:       uint64(mapsStats.v6.memory_used),
			Note:             "Statistics are currently shown for the first layer only",
		},
	}

	return response, nil
}

// validateSyncConfig validates that required sync config fields are set
func validateSyncConfig(cfg *fwstatepb.SyncConfig) error {
	var missing []string

	// Check src_addr (16 bytes for IPv6)
	if len(cfg.SrcAddr) != 16 || isAllZeroBytes(cfg.SrcAddr) {
		missing = append(missing, "src_addr")
	}

	// Check dst_ether (6 bytes for MAC)
	if len(cfg.DstEther) != 6 || isAllZeroBytes(cfg.DstEther) {
		missing = append(missing, "dst_ether")
	}

	// Check that at least one destination pair is configured
	hasMulticast := len(cfg.DstAddrMulticast) == 16 && !isAllZeroBytes(cfg.DstAddrMulticast) && cfg.PortMulticast != 0
	hasUnicast := len(cfg.DstAddrUnicast) == 16 && !isAllZeroBytes(cfg.DstAddrUnicast) && cfg.PortUnicast != 0

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
