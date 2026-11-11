package cnat64_test

import (
	"syscall"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
	"github.com/yanet-platform/yanet2/modules/nat64/bindings/go/cnat64"
)

// Test_MalformedPackets_PublishedModuleLookup verifies that the diagnostic
// resolves only a published NAT64 module with the requested name.
func Test_MalformedPackets_PublishedModuleLookup(t *testing.T) {
	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:    uint64(32 * datasize.MB),
		DPMemory:    uint64(4 * datasize.MB),
		WorkerCount: 1,
		Modules:     []string{"nat64", "forward"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	sharedMemory := harness.SharedMemory()
	agent, err := sharedMemory.AgentAttach("nat64-diagnostic", 0, 4*datasize.MB)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, agent.CleanUp()) })

	moduleName := "nat64-test"
	module, err := cnat64.NewModuleConfig(agent, moduleName)
	require.NoError(t, err)
	wrongType, err := cforward.NewModuleConfig(agent, "forward-only")
	require.NoError(t, err)
	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{
		module.AsFFIModule(), wrongType.AsFFIModule(),
	}))
	t.Cleanup(func() {
		require.NoError(t, agent.DeleteModuleConfig("nat64", moduleName))
		require.NoError(t, module.Free())
		require.NoError(t, agent.DeleteModuleConfig("forward", "forward-only"))
		require.NoError(t, wrongType.Free())
	})

	for _, tc := range []struct {
		name       string
		moduleName string
		wantError  error
	}{
		{name: "current module", moduleName: moduleName},
		{name: "absent module", moduleName: "missing", wantError: syscall.ENOENT},
		{name: "wrong module type", moduleName: "forward-only", wantError: syscall.ENOENT},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, err := cnat64.MalformedPackets(sharedMemory, 0, tc.moduleName)
			if tc.wantError != nil {
				require.ErrorIs(t, err, tc.wantError)
			} else {
				require.NoError(t, err)
			}
			require.Zero(t, value)
		})
	}
}

// Test_MalformedPackets_InstanceOutOfRange verifies that an index beyond the
// shared-memory topology is rejected before accessing native configuration.
func Test_MalformedPackets_InstanceOutOfRange(t *testing.T) {
	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:    uint64(32 * datasize.MB),
		DPMemory:    uint64(4 * datasize.MB),
		WorkerCount: 1,
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	value, err := cnat64.MalformedPackets(harness.SharedMemory(), 1, "nat64-test")
	require.Error(t, err)
	require.Zero(t, value)
}
