package bundle_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/controlplane/bundle"
	l3b "github.com/yanet-platform/yanet2/modules/l3b/controlplane"
)

// Test_Decode_OnlyListedModulesArePresent verifies that only named modules are
// populated and omitted options retain their defaults.
func Test_Decode_OnlyListedModulesArePresent(t *testing.T) {
	var cfg bundle.ModulesConfig
	err := xcfg.Decode([]byte(`
route:
  instance_id: 2
acl:
  instance_id: 3
`), &cfg)
	require.NoError(t, err)

	require.NotNil(t, cfg.Route.Unwrap())
	require.NotNil(t, cfg.ACL.Unwrap())
	require.Nil(t, cfg.RouteMPLS.Unwrap())
	require.Nil(t, cfg.Decap.Unwrap())
	require.Nil(t, cfg.DSCP.Unwrap())
	require.Nil(t, cfg.Forward.Unwrap())
	require.Nil(t, cfg.Mirror.Unwrap())
	require.Nil(t, cfg.NAT64.Unwrap())
	require.Nil(t, cfg.Pdump.Unwrap())
	require.Nil(t, cfg.Blackhole.Unwrap())
	require.Nil(t, cfg.Unrdup.Unwrap())
	require.Nil(t, cfg.L3B.Unwrap())

	require.Equal(t, uint32(2), cfg.Route.Unwrap().InstanceID.Unwrap())
	require.Equal(t, uint32(3), cfg.ACL.Unwrap().InstanceID.Unwrap())

	require.Equal(t, "/dev/hugepages/yanet", cfg.Route.Unwrap().MemoryPath.Unwrap())
}

// Test_Decode_L3BPresence verifies that an explicit L3B block survives decoding
// with its required instance and the module's default memory budget.
func Test_Decode_L3BPresence(t *testing.T) {
	var config bundle.ModulesConfig
	require.NoError(t, xcfg.Decode([]byte("l3b:\n  instance_id: 0\n"), &config))
	require.NotNil(t, config.L3B.Unwrap())
	require.Equal(t, uint32(0), config.L3B.Unwrap().InstanceID.Unwrap())
	require.Equal(t, l3b.DefaultConfig().MemoryRequirements, config.L3B.Unwrap().MemoryRequirements)
}

// Test_Decode_UnrdupKeepsDefaults verifies that omitted options retain their
// defaults.
func Test_Decode_UnrdupKeepsDefaults(t *testing.T) {
	var cfg bundle.ModulesConfig
	err := xcfg.Decode([]byte(`
unrdup:
  instance_id: 0
  memory_requirements: 8MB
`), &cfg)
	require.NoError(t, err)

	unrdup := cfg.Unrdup.Unwrap()
	require.NotNil(t, unrdup)
	require.Equal(t, "/dev/hugepages/yanet", unrdup.MemoryPath.Unwrap())
	require.Equal(t, "[::1]:0", unrdup.Endpoint.Unwrap())
}

// Test_Decode_NullModuleBlock_Rejected verifies that an empty module entry
// reports its full configuration path.
func Test_Decode_NullModuleBlock_Rejected(t *testing.T) {
	var cfg struct {
		Modules bundle.ModulesConfig `yaml:"modules"`
	}
	err := xcfg.Decode([]byte("modules:\n  route:\n"), &cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "modules.route")
}

// Test_Decode_NullDeviceBlock_Rejected verifies that an empty device entry
// reports its full configuration path.
func Test_Decode_NullDeviceBlock_Rejected(t *testing.T) {
	var cfg struct {
		Devices bundle.DevicesConfig `yaml:"devices"`
	}
	err := xcfg.Decode([]byte("devices:\n  plain:\n"), &cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "devices.plain")
}
