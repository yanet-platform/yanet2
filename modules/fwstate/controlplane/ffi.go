package fwstate

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
)

// maskedUpdate merges only selected fields over the current configuration.
//
// The caller holds the mutation lock until publication, so independent
// updates preserve one another regardless of their arrival order.
func maskedUpdate(old *FwStateConfig, req *fwstatepb.UpdateConfigRequest) (*fwstatepb.UpdateConfigRequest, error) {
	if req.ClearMulticast || req.ClearUnicast {
		return nil, fmt.Errorf("endpoint clear flags cannot be combined with an update mask")
	}
	merged := &fwstatepb.UpdateConfigRequest{
		SyncConfig: mergedSyncConfig(old, nil),
	}
	if old != nil {
		merged.MapNameV4 = old.MapNameV4()
		merged.MapNameV6 = old.MapNameV6()
	}
	for _, path := range req.UpdateMask.Paths {
		switch path {
		case "map_name_v4":
			merged.MapNameV4 = req.MapNameV4
		case "map_name_v6":
			merged.MapNameV6 = req.MapNameV6
		case "sync_config.dst_ether":
			merged.SyncConfig.DstEther = req.GetSyncConfig().GetDstEther()
		case "sync_config.dst_addr_unicast":
			merged.SyncConfig.DstAddrUnicast = req.GetSyncConfig().GetDstAddrUnicast()
		case "sync_config.port_unicast":
			merged.SyncConfig.PortUnicast = req.GetSyncConfig().GetPortUnicast()
		case "sync_config.src_addr":
			merged.SyncConfig.SrcAddr = req.GetSyncConfig().GetSrcAddr()
		case "sync_config.dst_addr_multicast":
			merged.SyncConfig.DstAddrMulticast = req.GetSyncConfig().GetDstAddrMulticast()
		case "sync_config.port_multicast":
			merged.SyncConfig.PortMulticast = req.GetSyncConfig().GetPortMulticast()
		case "sync_config.tcp_syn_ack":
			merged.SyncConfig.TcpSynAck = req.GetSyncConfig().GetTcpSynAck()
		case "sync_config.tcp_syn":
			merged.SyncConfig.TcpSyn = req.GetSyncConfig().GetTcpSyn()
		case "sync_config.tcp_fin":
			merged.SyncConfig.TcpFin = req.GetSyncConfig().GetTcpFin()
		case "sync_config.tcp":
			merged.SyncConfig.Tcp = req.GetSyncConfig().GetTcp()
		case "sync_config.udp":
			merged.SyncConfig.Udp = req.GetSyncConfig().GetUdp()
		case "sync_config.default":
			merged.SyncConfig.Default = req.GetSyncConfig().GetDefault()
		case "sync_config.sync_suppress_timeout":
			merged.SyncConfig.SyncSuppressTimeout = req.GetSyncConfig().GetSyncSuppressTimeout()
		default:
			return nil, fmt.Errorf("unknown update mask path %q", path)
		}
	}
	return merged, nil
}

// FwStateConfig is a service-owned fwstate module config plus the names of
// the fwstate-map objects it links, which the service needs for ShowConfig
// and for stats and entry reads delegated to the map objects.
//
// The exposed snapshot — the map names and the sync settings — is
// captured at construction and never read back from shared memory, so
// readers may hold a config past its retirement: the Go values stay
// valid even after the shared-memory handle is freed.
type FwStateConfig struct {
	*cfwstate.ModuleConfig

	mapNameV4  string
	mapNameV6  string
	syncConfig cfwstate.SyncConfig
}

// NewFWStateModuleConfig builds the config in one step, ready to
// publish.
//
// The request's sync settings merge over the values they replace, and
// each named map is declared as an object link resolving against
// published objects when the config is published. An empty map name
// declares no link, and the module then counts and drops that family's
// synced state. The map names are remembered only after the construction
// succeeds, so a failed construction leaves the previous linkage visible
// to readers.
func NewFWStateModuleConfig(
	agent *ffi.Agent,
	name string,
	old *FwStateConfig,
	syncConfig *fwstatepb.SyncConfig,
	fw4MapName, fw6MapName string,
) (*FwStateConfig, error) {
	merged := mergedSyncConfigC(old, syncConfig)
	return newFWStateModuleConfig(agent, name, merged, fw4MapName, fw6MapName)
}

// NewFWStateModuleConfigWithEndpointClears builds a replacement config while
// honoring explicit requests to disable either synchronization endpoint.
func NewFWStateModuleConfigWithEndpointClears(
	agent *ffi.Agent,
	name string,
	old *FwStateConfig,
	syncConfig *fwstatepb.SyncConfig,
	clearMulticast, clearUnicast bool,
	fw4MapName, fw6MapName string,
) (*FwStateConfig, error) {
	merged := mergedSyncConfigCWithClears(old, syncConfig, clearMulticast, clearUnicast)
	return newFWStateModuleConfig(agent, name, merged, fw4MapName, fw6MapName)
}

// newFWStateModuleConfig installs the already merged values verbatim.
func newFWStateModuleConfig(
	agent *ffi.Agent,
	name string,
	syncConfig cfwstate.SyncConfig,
	fw4MapName, fw6MapName string,
) (*FwStateConfig, error) {
	moduleCfg, err := cfwstate.NewModuleConfig(
		agent,
		name,
		&syncConfig,
		fw4MapName,
		fw6MapName,
	)
	if err != nil {
		return nil, err
	}
	return &FwStateConfig{
		ModuleConfig: moduleCfg,
		mapNameV4:    fw4MapName,
		mapNameV6:    fw6MapName,
		syncConfig:   syncConfig,
	}, nil
}

// mergedMapNames returns the map object links a replacement config
// should declare.
//
// A legacy request naming a map relinks that family; leaving it unnamed keeps
// the link the replaced config had, the only spelling the request has
// for "leave this family alone". A fresh config left unnamed declares no
// link at all, and the maps can be attached by a later update.
func mergedMapNames(
	old *FwStateConfig,
	req *fwstatepb.UpdateConfigRequest,
) (string, string) {
	mapNameV4, mapNameV6 := req.GetMapNameV4(), req.GetMapNameV6()
	if old == nil {
		return mapNameV4, mapNameV6
	}
	if mapNameV4 == "" {
		mapNameV4 = old.MapNameV4()
	}
	if mapNameV6 == "" {
		mapNameV6 = old.MapNameV6()
	}
	return mapNameV4, mapNameV6
}

// mergedSyncConfig returns the sync settings a construction would
// install: the request merged over the values it replaces.
func mergedSyncConfig(
	old *FwStateConfig,
	syncConfig *fwstatepb.SyncConfig,
) *fwstatepb.SyncConfig {
	return mergedSyncConfigWithClears(old, syncConfig, false, false)
}

func mergedSyncConfigWithClears(
	old *FwStateConfig,
	syncConfig *fwstatepb.SyncConfig,
	clearMulticast, clearUnicast bool,
) *fwstatepb.SyncConfig {
	return fwstatepb.FromCSyncConfig(mergedSyncConfigCWithClears(old, syncConfig, clearMulticast, clearUnicast))
}

// mergedSyncConfigC is the merge in the form the construction consumes.
//
// A request carrying no sync settings at all asks for no sync change,
// which is not the same as asking for the defaults: it keeps the
// replaced config's values, so an update touching only the linked maps
// leaves synchronization exactly as it was.
func mergedSyncConfigC(
	old *FwStateConfig,
	syncConfig *fwstatepb.SyncConfig,
) cfwstate.SyncConfig {
	return mergedSyncConfigCWithClears(old, syncConfig, false, false)
}

func mergedSyncConfigCWithClears(
	old *FwStateConfig,
	syncConfig *fwstatepb.SyncConfig,
	clearMulticast, clearUnicast bool,
) cfwstate.SyncConfig {
	current := cfwstate.DefaultSyncConfig()
	if old != nil {
		current = old.ModuleConfig.GetSyncConfig()
	}
	if syncConfig == nil && !clearMulticast && !clearUnicast {
		return current
	}
	return syncConfig.ToCWithDefaultsAndClears(current, clearMulticast, clearUnicast)
}

// MergedSyncConfig returns the request's sync config merged with the
// defaults: the exact values a fresh construction would install.
func (m *FwStateConfig) MergedSyncConfig(syncConfig *fwstatepb.SyncConfig) *fwstatepb.SyncConfig {
	return mergedSyncConfig(nil, syncConfig)
}

// MapNameV4 returns the name of the linked IPv4 fwstate-map object.
func (m *FwStateConfig) MapNameV4() string {
	return m.mapNameV4
}

// MapNameV6 returns the name of the linked IPv6 fwstate-map object.
func (m *FwStateConfig) MapNameV6() string {
	return m.mapNameV6
}

func (m *FwStateConfig) GetSyncConfig() *fwstatepb.SyncConfig {
	return fwstatepb.FromCSyncConfig(m.syncConfig)
}
