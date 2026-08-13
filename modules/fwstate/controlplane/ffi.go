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

// NewFWStateModuleConfig builds the config in one step: the request's
// sync config merges over the replaced config's values (or the defaults
// for a fresh config) and both map names are declared as object links,
// resolving against published objects when the config is published.
//
// The map names are remembered only after the C construction succeeds,
// so a failed construction leaves the previous linkage visible to
// readers.
func NewFWStateModuleConfig(
	agent *ffi.Agent,
	name string,
	old *FwStateConfig,
	syncConfig *fwstatepb.SyncConfig,
	fw4MapName, fw6MapName string,
) (*FwStateConfig, error) {
	current := cfwstate.DefaultSyncConfig()
	if old != nil {
		current = old.ModuleConfig.GetSyncConfig()
	}

	var finalSync *cfwstate.SyncConfig
	if syncConfig != nil {
		merged := syncConfig.ToCWithDefaults(current)
		finalSync = &merged
	}

	moduleCfg, err := cfwstate.NewModuleConfig(
		agent,
		name,
		finalSync,
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

// mergedSyncConfig merges the request's sync config over the replaced
// config's current values (or the defaults for a fresh config): the
// exact values a construction would install.
func mergedSyncConfig(
	old *FwStateConfig,
	syncConfig *fwstatepb.SyncConfig,
) *fwstatepb.SyncConfig {
	current := cfwstate.DefaultSyncConfig()
	if old != nil {
		current = old.ModuleConfig.GetSyncConfig()
	}
	return fwstatepb.FromCSyncConfig(syncConfig.ToCWithDefaults(current))
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
