//go:build yanet_rust_cp

package decap

import (
	"fmt"
	"net/netip"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/decap/bindings/go/rsdecap"
)

// newModuleConfig builds and validates a decap configuration with the Rust
// api; only the Rust dataplane module accepts its layout.
func newModuleConfig(agent *ffi.Agent, name string, prefixes []netip.Prefix) (moduleConfig, error) {
	mod, err := rsdecap.NewModuleConfig(agent, name, prefixes)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)
	}
	return mod, nil
}
