package fwstate

import (
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb"
)

type FwStateConfig struct {
	*cfwstate.ModuleConfig
}

type CursorEntry = cfwstate.CursorEntry
type OutdatedLayers = cfwstate.OutdatedLayers
type mapsStats = cfwstate.MapsStats

func NewFWStateModuleConfig(agent *ffi.Agent, name string) (*FwStateConfig, error) {
	moduleCfg, err := cfwstate.NewModuleConfig(agent, name)
	if err != nil {
		return nil, err
	}
	return &FwStateConfig{ModuleConfig: moduleCfg}, nil
}

func (m *FwStateConfig) CreateMaps(
	mapConfig *fwstatepb.MapConfig,
	workerCount uint16,
) error {
	return m.ModuleConfig.CreateMaps(mapConfig.ToC(), workerCount)
}

func (m *FwStateConfig) PropagateConfig(old *FwStateConfig) {
	m.ModuleConfig.PropagateConfig(old.ModuleConfig)
}

func (m *FwStateConfig) SetSyncConfig(req *fwstatepb.SyncConfig) {
	cfg := req.ToCWithDefaults(m.ModuleConfig.GetSyncConfig())
	m.ModuleConfig.SetSyncConfig(cfg)
}

func (m *FwStateConfig) GetSyncConfig() *fwstatepb.SyncConfig {
	return fwstatepb.FromCSyncConfig(m.ModuleConfig.GetSyncConfig())
}

func (m *FwStateConfig) GetMapConfig() *fwstatepb.MapConfig {
	return fwstatepb.FromCMapConfig(m.ModuleConfig.GetMapConfig())
}
