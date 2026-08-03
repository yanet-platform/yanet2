package bundle_test

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/controlplane/bundle"
	decap "github.com/yanet-platform/yanet2/modules/decap/controlplane"
)

// Test_Decode_OnlyListedModulesArePresent asserts that decoding a document
// listing only two modules leaves the other nine nil, that the listed ones
// carry the instance_id given in the document, and that a listed module
// omitting an optional field keeps its DefaultConfig value.
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

	require.Equal(t, uint32(2), cfg.Route.Unwrap().InstanceID.Unwrap())
	require.Equal(t, uint32(3), cfg.ACL.Unwrap().InstanceID.Unwrap())

	require.Equal(t, "/dev/hugepages/yanet", cfg.Route.Unwrap().MemoryPath.Unwrap())
}

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

// Test_NewBundle_EmptyConfig_NoServicesNoAgents asserts that a bundle built
// from all-nil module and device config attaches no agent: it constructs no
// service at all, so it never reaches ffi.AttachSharedMemory.
func Test_NewBundle_EmptyConfig_NoServicesNoAgents(t *testing.T) {
	b, err := bundle.NewBundle(bundle.ModulesConfig{}, bundle.DevicesConfig{})
	require.NoError(t, err)
	require.Empty(t, b.Services())
}

// Test_NewBundle_ConfiguredModuleWithBadPath_FailsNamingModule is a
// positive control for the nil-skip in buildServices: a single configured
// module still reaches its constructor and a bad memory_path surfaces as an
// error naming that module, proving the skip isn't silently dropping every
// module.
func Test_NewBundle_ConfiguredModuleWithBadPath_FailsNamingModule(t *testing.T) {
	cfg := bundle.ModulesConfig{
		Decap: xcfg.NewOptional(decap.Config{
			InstanceID:         xcfg.NewRequired(uint32(0)),
			MemoryPath:         xcfg.MustNonEmptyString("/nonexistent/path/for/bundle/cfg/test"),
			MemoryRequirements: xcfg.MustNonZero(16 * datasize.MB),
			Endpoint:           xcfg.MustNonEmptyString("[::1]:0"),
		}),
	}

	_, err := bundle.NewBundle(cfg, bundle.DevicesConfig{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "decap module")
}

// Test_Decode_NullModuleBlock_Rejected asserts that a null module block is rejected naming the module.
func Test_Decode_NullModuleBlock_Rejected(t *testing.T) {
	var cfg struct {
		Modules bundle.ModulesConfig `yaml:"modules"`
	}
	err := xcfg.Decode([]byte("modules:\n  route:\n"), &cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "modules.route")
}

// Test_Decode_NullDeviceBlock_Rejected asserts that a null device block is rejected naming the device.
func Test_Decode_NullDeviceBlock_Rejected(t *testing.T) {
	var cfg struct {
		Devices bundle.DevicesConfig `yaml:"devices"`
	}
	err := xcfg.Decode([]byte("devices:\n  plain:\n"), &cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "devices.plain")
}
