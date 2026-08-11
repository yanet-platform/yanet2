package framework_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/controlplane/yncp"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// TestDefaultControlplaneConfig_NoUnknownKeys guards the functional harness's
// baseline controlplane YAML against a key that matches no field in
// yncp.Config, the same check the director now applies at startup.
func TestDefaultControlplaneConfig_NoUnknownKeys(t *testing.T) {
	require.NoError(t, xcfg.CheckKnownKeys[yncp.Config]([]byte(framework.DefaultControlplaneConfig())))
}

// TestDefaultControlplaneConfig_StartsIntendedSet decodes the harness's
// baseline controlplane YAML through the same path the director's main()
// drives, and asserts every module and device the YAML lists actually
// starts, since an absent modules/devices key now means the module or
// device is not started at all.
func TestDefaultControlplaneConfig_StartsIntendedSet(t *testing.T) {
	cfg := &yncp.Config{}
	cfg.Default()

	err := xcfg.Decode([]byte(framework.DefaultControlplaneConfig()), cfg, xcfg.WithKnownFields())
	require.NoError(t, err)

	require.NotNil(t, cfg.Modules.Route.Unwrap())
	require.NotNil(t, cfg.Modules.RouteMPLS.Unwrap())
	require.NotNil(t, cfg.Modules.Decap.Unwrap())
	require.NotNil(t, cfg.Modules.DSCP.Unwrap())
	require.NotNil(t, cfg.Modules.Forward.Unwrap())
	require.NotNil(t, cfg.Modules.NAT64.Unwrap())
	require.NotNil(t, cfg.Modules.Pdump.Unwrap())
	require.NotNil(t, cfg.Modules.ACL.Unwrap())
	require.NotNil(t, cfg.Modules.Mirror.Unwrap())
	require.NotNil(t, cfg.Modules.Blackhole.Unwrap())

	require.NotNil(t, cfg.Devices.Plain.Unwrap())
	require.NotNil(t, cfg.Devices.Vlan.Unwrap())
	// Trafgen is deliberately absent from the harness config, matching the
	// shipped default: the harness never relied on it.
	require.Nil(t, cfg.Devices.Trafgen.Unwrap())
}
