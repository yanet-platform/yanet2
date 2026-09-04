package fwstate

import (
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
)

// FwStateConfig is a service-owned fwstate module config plus the names of
// the fwstate-map objects it links, which the service needs for ShowConfig
// and for stats and entry reads delegated to the map objects.
type FwStateConfig struct {
	*cfwstate.ModuleConfig

	mapNameV4 string
	mapNameV6 string
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

	moduleCfg, err := cfwstate.NewModuleConfig(
		agent,
		name,
		&merged,
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
	}, nil
}

// mergedMapNames returns the map object links a replacement config
// should declare.
//
// A request naming a map relinks that family; leaving it unnamed keeps
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
	return fwstatepb.FromCSyncConfig(mergedSyncConfigC(old, syncConfig))
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
	current := cfwstate.DefaultSyncConfig()
	if old != nil {
		current = old.ModuleConfig.GetSyncConfig()
	}
	if syncConfig == nil {
		return current
	}
	return syncConfig.ToCWithDefaults(current)
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
	return fwstatepb.FromCSyncConfig(m.ModuleConfig.GetSyncConfig())
}
