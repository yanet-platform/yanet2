package fwstate

import (
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
)

type FwStateConfig struct {
	*cfwstate.ModuleConfig
	// fwtableNameV4 is the name of the standalone fwstate-map (kind V4)
	// whose fwtable this config borrows.
	fwtableNameV4 string
	// fwtableNameV6 is the name of the standalone fwstate-map (kind V6)
	// whose fwtable this config borrows.
	fwtableNameV6 string
}

type CursorEntry = cfwstate.CursorEntry
type mapsStats = cfwstate.MapsStats
type mapStats = cfwstate.MapStats

func NewFWStateModuleConfig(agent *ffi.Agent, name string) (*FwStateConfig, error) {
	moduleCfg, err := cfwstate.NewModuleConfig(agent, name)
	if err != nil {
		return nil, err
	}
	return &FwStateConfig{ModuleConfig: moduleCfg}, nil
}

// FwtableNameV4 returns the name of the standalone fwstate-map (kind V4)
// this config references.
func (m *FwStateConfig) FwtableNameV4() string {
	return m.fwtableNameV4
}

// SetFwtableNameV4 records the name of the standalone fwstate-map (kind V4)
// this config references.
func (m *FwStateConfig) SetFwtableNameV4(name string) {
	m.fwtableNameV4 = name
}

// FwtableNameV6 returns the name of the standalone fwstate-map (kind V6)
// this config references.
func (m *FwStateConfig) FwtableNameV6() string {
	return m.fwtableNameV6
}

// SetFwtableNameV6 records the name of the standalone fwstate-map (kind V6)
// this config references.
func (m *FwStateConfig) SetFwtableNameV6(name string) {
	m.fwtableNameV6 = name
}

// UsesFwtable reports whether this config references the given fwstate-map
// name as either its v4 or v6 table.
func (m *FwStateConfig) UsesFwtable(mapName string) bool {
	return m.fwtableNameV4 == mapName || m.fwtableNameV6 == mapName
}

func (m *FwStateConfig) GetSyncConfig() *fwstatepb.SyncConfig {
	return fwstatepb.FromCSyncConfig(m.ModuleConfig.GetSyncConfig())
}
