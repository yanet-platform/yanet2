package pdump

import (
	"errors"
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	cpdump "github.com/yanet-platform/yanet2/modules/pdump/bindings/go/cpdump"
	"github.com/yanet-platform/yanet2/objects/ring/bindings/go/cring"
)

type retainedRing struct {
	Name string
	Err  error
}

// cleanupStale removes predecessor publications before any service admits RPCs.
func cleanupStale(agent *ffi.Agent) ([]retainedRing, error) {
	targets, err := cpdump.StaleTargets(agent)
	if err != nil {
		return nil, err
	}
	if len(targets.Modules) == 0 && len(targets.Rings) == 0 {
		return nil, nil
	}

	stale := map[string]struct{}{}
	for _, name := range targets.Modules {
		stale[name] = struct{}{}
	}

	for _, function := range agent.DPConfig().Functions() {
		filtered, changed := withoutStaleCaptures(function, stale)
		if changed {
			if err := agent.UpdateFunction(filtered); err != nil {
				return nil, fmt.Errorf("failed to update function %q: %w", function.Name, err)
			}
		}
	}

	for _, name := range targets.Modules {
		if err := agent.DeleteModuleConfig(moduleType, name); err != nil && !errors.Is(err, ffi.ErrNotFound) {
			return nil, fmt.Errorf("failed to delete stale pdump config %q: %w", name, err)
		}
	}

	var retained []retainedRing
	for _, name := range targets.Rings {
		err := cring.DeleteObject(agent, name)
		switch {
		case err == nil, errors.Is(err, ffi.ErrNotFound):
		case errors.Is(err, ffi.ErrBusy):
			retained = append(retained, retainedRing{Name: name, Err: err})
		default:
			return retained, fmt.Errorf("failed to delete stale ring %q: %w", name, err)
		}
	}
	return retained, nil
}

func withoutStaleCaptures(function ffi.Function, stale map[string]struct{}) (ffi.FunctionConfig, bool) {
	filtered := ffi.FunctionConfig{
		Name:   function.Name,
		Chains: make([]ffi.FunctionChainConfig, 0, len(function.Chains)),
	}
	changed := false
	for _, chain := range function.Chains {
		modules := make([]ffi.ChainModuleConfig, 0, len(chain.Modules))
		for _, module := range chain.Modules {
			_, remove := stale[module.Name]
			if module.Type == moduleType && remove {
				changed = true
				continue
			}
			modules = append(modules, ffi.ChainModuleConfig{Type: module.Type, Name: module.Name})
		}
		filtered.Chains = append(filtered.Chains, ffi.FunctionChainConfig{
			Chain:  ffi.ChainConfig{Name: chain.Name, Modules: modules},
			Weight: chain.Weight,
		},
		)
	}
	return filtered, changed
}
