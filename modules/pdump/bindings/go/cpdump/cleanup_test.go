package cpdump_test

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/ffi/ffitest"
	cpdump "github.com/yanet-platform/yanet2/modules/pdump/bindings/go/cpdump"
	"github.com/yanet-platform/yanet2/modules/pdump/internal/testutils"
	"github.com/yanet-platform/yanet2/objects/ring/bindings/go/cring"
)

// publishCleanupPair leaves its capture and ring alive until fixture teardown.
func publishCleanupPair(t *testing.T, agent *ffi.Agent, name string) {
	t.Helper()
	object, err := cring.NewObject(agent, name, cpdump.MinRingCapacity, cring.DefaultPublishBatch)
	require.NoError(t, err)
	require.NoError(t, object.Publish())
	config, err := cpdump.NewModuleConfig(agent, name)
	require.NoError(t, err)
	require.NoError(t, config.LinkRing(name))
	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}))
}

// Test_StaleTargets_SelectsOnlyPredecessorOwners verifies that current and
// foreign publications are excluded even when the foreign agent shares a prefix.
func Test_StaleTargets_SelectsOnlyPredecessorOwners(t *testing.T) {
	path := ffitest.NewStorage(t, ffitest.StorageTypes{
		Modules: []string{"pdump"}, Objects: []string{"ring"},
	},
	)
	memory, err := ffi.AttachSharedMemory(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, memory.Detach()) })
	require.NoError(t, testutils.PrepareWorker(memory))
	t.Cleanup(func() { testutils.FreeWorker(memory) })

	previous, err := memory.AgentAttach("pdump", 0, 16*datasize.MB)
	require.NoError(t, err)
	publishCleanupPair(t, previous, "previous")
	current, err := memory.AgentAttach("pdump", 0, 16*datasize.MB)
	require.NoError(t, err)
	publishCleanupPair(t, current, "current")
	foreign, err := memory.AgentAttach("pdump-foreign", 0, 16*datasize.MB)
	require.NoError(t, err)
	publishCleanupPair(t, foreign, "foreign")

	targets, err := cpdump.StaleTargets(current)
	require.NoError(t, err)
	require.Equal(t, cpdump.CleanupTargets{
		Modules: []string{"previous"},
		Rings:   []string{"previous"},
	}, targets,
	)
}
