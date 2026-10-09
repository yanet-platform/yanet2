//go:build !yanet_rust_cp

package decap

import (
	"fmt"
	"net/netip"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/decap/bindings/go/cdecap"
)

// newModuleConfig builds a decap configuration with the C api.
func newModuleConfig(agent *ffi.Agent, name string, prefixes []netip.Prefix) (moduleConfig, error) {
	mod, err := cdecap.NewModuleConfig(agent, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)
	}

	for _, prefix := range prefixes {
		if err := mod.PrefixAdd(prefix); err != nil {
			if err := mod.Free(); err != nil {
				return nil, fmt.Errorf("failed to free abandoned config: %w", err)
			}
			return nil, fmt.Errorf("failed to add prefix %v: %w", prefix, err)
		}
	}

	return mod, nil
}
