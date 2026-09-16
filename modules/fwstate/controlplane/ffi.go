package fwstate

import (
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
)

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
	merged := mergedSyncConfig(old, syncConfig)
	return newFWStateModuleConfig(agent, name, merged.ToC(), fw4MapName, fw6MapName)
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
// A request naming a map relinks that family, and an empty name unlinks it.
// A family the request leaves out keeps the link the replaced config had.
func mergedMapNames(
	old *FwStateConfig,
	req *fwstatepb.UpdateConfigRequest,
) (string, string) {
	var mapNameV4, mapNameV6 string
	if old != nil {
		mapNameV4, mapNameV6 = old.MapNameV4(), old.MapNameV6()
	}
	if req.MapNameV4 != nil {
		mapNameV4 = req.GetMapNameV4()
	}
	if req.MapNameV6 != nil {
		mapNameV6 = req.GetMapNameV6()
	}
	return mapNameV4, mapNameV6
}

// mergedSyncConfig returns the sync settings a construction would
// install: the update merged over the values it replaces.
func mergedSyncConfig(
	old *FwStateConfig,
	update *fwstatepb.SyncConfig,
) *fwstatepb.SyncConfig {
	merged := fwstatepb.FromCSyncConfig(cfwstate.DefaultSyncConfig())
	if old != nil {
		merged = old.GetSyncConfig()
	}
	merged.Merge(update)
	return merged
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
