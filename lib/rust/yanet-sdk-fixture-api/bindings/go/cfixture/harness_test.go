//go:build sdk_rust_fixtures

package cfixture_test

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/lib/rust/yanet-sdk-fixture-api/bindings/go/cfixture"
)

// startHarness starts a harness that loads the requested modules from the
// plugins of the directory, and frees it when the test ends.
func startHarness(t *testing.T, pluginDir string, modules ...string) (*dataplaneut.Harness, error) {
	t.Helper()

	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:    uint64(16 * datasize.MB),
		DPMemory:    uint64(4 * datasize.MB),
		WorkerCount: 1,
		Modules:     modules,
		PluginDir:   pluginDir,
	})
	if err == nil {
		t.Cleanup(harness.Free)
	}
	return harness, err
}

// moduleNames returns the names of the modules the harness has registered.
func moduleNames(harness *dataplaneut.Harness) []string {
	modules := harness.SharedMemory().DPConfig(0).Modules()
	names := make([]string, 0, len(modules))
	for idx := range modules {
		names = append(names, modules[idx].Name())
	}
	return names
}

// Test_PluginDir_RequestedModule_IsRegistered verifies that a module the
// harness is asked for is loaded from the plugin of the directory and
// registered.
func Test_PluginDir_RequestedModule_IsRegistered(t *testing.T) {
	harness, err := startHarness(t, cfixture.PluginDir, "fixture")
	require.NoError(t, err)

	require.Contains(t, moduleNames(harness), "fixture")
}

// Test_PluginDir_UnrequestedModule_IsNotRegistered verifies that a plugin in
// the directory is not loaded when its module is not asked for.
func Test_PluginDir_UnrequestedModule_IsNotRegistered(t *testing.T) {
	harness, err := startHarness(t, cfixture.PluginDir)
	require.NoError(t, err)

	require.NotContains(t, moduleNames(harness), "fixture")
}

// Test_PluginDir_RequestedModuleWithoutPlugin_RefusesToStart verifies that the
// harness does not start when a module it is asked for has no plugin in the
// directory.
func Test_PluginDir_RequestedModuleWithoutPlugin_RefusesToStart(t *testing.T) {
	_, err := startHarness(t, t.TempDir(), "fixture")

	require.Error(t, err)
}
