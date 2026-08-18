package fwstate

import (
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
)

type FwStateConfig struct {
	*cfwstate.ModuleConfig
}

type CursorEntry = cfwstate.CursorEntry
type OutdatedLayers = cfwstate.OutdatedLayers
type mapStats = cfwstate.MapStats

func NewFWStateModuleConfig(
	agent *ffi.Agent,
	name string,
	old *FwStateConfig,
	syncConfig *fwstatepb.SyncConfig,
	mapConfig *fwstatepb.MapConfig,
	workerCount uint16,
) (*FwStateConfig, error) {
	var oldModule *cfwstate.ModuleConfig
	current := cfwstate.DefaultSyncConfig()
	if old != nil {
		oldModule = old.ModuleConfig
		current = old.ModuleConfig.GetSyncConfig()
	}

	var finalSync *cfwstate.SyncConfig
	if syncConfig != nil {
		// The request's zero fields inherit the propagated or default
		// values, exactly the values a second update used to merge.
		merged := syncConfig.ToCWithDefaults(current)
		finalSync = &merged
	}

	moduleCfg, err := cfwstate.NewModuleConfig(
		agent,
		name,
		oldModule,
		finalSync,
		mapConfig.ToC(),
		workerCount,
	)
	if err != nil {
		return nil, err
	}
	return &FwStateConfig{ModuleConfig: moduleCfg}, nil
}

func (m *FwStateConfig) InsertLayer(
	mapConfig *fwstatepb.MapConfig,
	workerCount uint16,
) error {
	return m.ModuleConfig.InsertLayer(mapConfig.ToC(), workerCount)
}

func (m *FwStateConfig) GetSyncConfig() *fwstatepb.SyncConfig {
	return fwstatepb.FromCSyncConfig(m.ModuleConfig.GetSyncConfig())
}

func (m *FwStateConfig) GetMapConfig() *fwstatepb.MapConfig {
	return fwstatepb.FromCMapConfig(m.ModuleConfig.GetMapConfig())
}
