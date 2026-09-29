package bundle_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	testshm "github.com/yanet-platform/yanet2/common/go/testutils/shm"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/controlplane/bundle"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	blackhole "github.com/yanet-platform/yanet2/modules/blackhole/controlplane"
	decap "github.com/yanet-platform/yanet2/modules/decap/controlplane"
	dscp "github.com/yanet-platform/yanet2/modules/dscp/controlplane"
)

// Test_NewBundle_EmptyConfig_NoServicesNoAgents asserts that an empty bundle
// constructs no services and never attaches shared memory.
func Test_NewBundle_EmptyConfig_NoServicesNoAgents(t *testing.T) {
	b, err := bundle.NewBundle(bundle.ModulesConfig{}, bundle.DevicesConfig{})
	require.NoError(t, err)
	require.Empty(t, b.Services())
}

// Test_NewBundle_ConfiguredModuleWithBadPath_FailsNamingModule verifies that a
// configured module reaches its constructor and reports its name on failure.
func Test_NewBundle_ConfiguredModuleWithBadPath_FailsNamingModule(t *testing.T) {
	cfg := bundle.ModulesConfig{
		Decap: xcfg.NewOptional(decap.Config{
			AttachConfig: ffi.AttachConfig{
				InstanceID:         xcfg.NewRequired(uint32(0)),
				MemoryPath:         xcfg.MustNonEmptyString(filepath.Join(t.TempDir(), "missing")),
				MemoryRequirements: xcfg.MustNonZero(16 * datasize.MB),
			},
			Endpoint: xcfg.MustNonEmptyString("[::1]:0"),
		}),
	}

	_, err := bundle.NewBundle(cfg, bundle.DevicesConfig{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "decap module")
}

// Test_NewBundle_ReleasedOnLaterFailure verifies that a later constructor
// failure closes an earlier real module service and releases its mapping.
func Test_NewBundle_ReleasedOnLaterFailure(t *testing.T) {
	decapConfig := decap.DefaultConfig()
	decapConfig.InstanceID = xcfg.NewRequired(uint32(0))
	decapConfig.MemoryPath = xcfg.MustNonEmptyString(testshm.NewStorage(t, testshm.StorageTypes{}))
	dscpConfig := dscp.DefaultConfig()
	dscpConfig.InstanceID = xcfg.NewRequired(uint32(0))
	dscpConfig.MemoryPath = decapConfig.MemoryPath
	blackholeConfig := blackhole.DefaultConfig()
	blackholeConfig.InstanceID = xcfg.NewRequired(uint32(0))
	blackholeConfig.MemoryPath = xcfg.MustNonEmptyString(filepath.Join(t.TempDir(), "missing"))

	result, err := bundle.NewBundle(bundle.ModulesConfig{
		Decap:     xcfg.NewOptional(*decapConfig),
		DSCP:      xcfg.NewOptional(*dscpConfig),
		Blackhole: xcfg.NewOptional(*blackholeConfig),
	}, bundle.DevicesConfig{})
	require.Nil(t, result)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.ErrorContains(t, err, "blackhole module")
}
