package decap_test

import (
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/decap/bindings/go/cdecap"
)

// Memory sizes for the failed-publish regression harness.
const (
	failedPublishCPSize    = 32 * datasize.MB
	failedPublishDPSize    = 4 * datasize.MB
	failedPublishAgentSize = 2 * datasize.MB
)

// TestFailedPublish_LeavesCallersModuleIntact guards a failed publish after
// upsert, keeping the caller's handle usable afterward.
//
// A module whose publish failed was never registered in a live
// generation, so it is dangling throughout: only its owner's free may
// destroy it, and that free succeeds immediately. The checkpoints below
// prove the failed publish itself destroyed nothing and that the module
// stayed a valid, usable config until its owner freed it.
func TestFailedPublish_LeavesCallersModuleIntact(t *testing.T) {
	h, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(failedPublishCPSize),
		DPMemory:      uint64(failedPublishDPSize),
		WorkerCount:   1,
		Devices:       []string{"port0"},
		Modules:       []string{"decap"},
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(h.Free)

	shm := h.SharedMemory()
	const agentName = "cpml-failed-publish"

	agent, err := shm.AgentAttach(agentName, 0, failedPublishAgentSize)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	beforeCreate := agentRootMemoryNode(t, shm, agentName)

	mod, err := cdecap.NewModuleConfig(agent, "decap0")
	require.NoError(t, err)
	require.NoError(t, mod.PrefixAdd(netip.MustParsePrefix("2001:db8::/32")))

	afterCreate := agentRootMemoryNode(t, shm, agentName)
	require.Equalf(
		t, beforeCreate.BAllocCount+1, afterCreate.BAllocCount,
		"module creation must allocate its config struct on the agent's root context",
	)

	// Zeroing the worker count fails the execution-context build
	// deterministically, in the exact phase this test targets.
	//
	// A byte-target arena starve instead lands in a different phase under a
	// sanitizer build, where every allocation carries red zones: the arena
	// runs out earlier, during the candidate generation's registry copy,
	// before the module is ever upserted into it.
	originalWorkerCount := zeroWorkerCount(agent.AsRawPtr())

	err = agent.UpdateModules([]ffi.ModuleConfig{mod.AsFFIModule()})
	restoreWorkerCount(agent.AsRawPtr(), originalWorkerCount)
	require.Error(
		t, err,
		"a zero worker count must fail the execution-context build",
	)
	require.ErrorContains(t, err, "execution context")

	afterFailedPublish := agentRootMemoryNode(t, shm, agentName)
	require.Equalf(
		t, afterCreate.BFreeCount, afterFailedPublish.BFreeCount,
		"a failed publish must not destroy the module: only its owner's free may",
	)

	// Create an unrelated config and free it: never published, so it is
	// dangling, and its owner's free destroys exactly this one config.
	other, err := cdecap.NewModuleConfig(agent, "decap1")
	require.NoError(t, err)

	afterUnrelatedCreate := agentRootMemoryNode(t, shm, agentName)
	require.Equalf(
		t, afterFailedPublish.BFreeCount, afterUnrelatedCreate.BFreeCount,
		"creating the unrelated config must not destroy anything by itself",
	)

	other.Free()

	afterUnrelatedFree := agentRootMemoryNode(t, shm, agentName)
	require.Equalf(
		t, afterUnrelatedCreate.BFreeCount+1, afterUnrelatedFree.BFreeCount,
		"the unrelated config is dangling, so its owner's free must destroy it",
	)

	// The module must still be a valid, uncorrupted config, not merely an
	// untouched reference count: exercise it the same way a caller that
	// goes on using it would.
	require.NoError(
		t, mod.PrefixAdd(netip.MustParsePrefix("2001:db9::/32")),
		"the caller must still be able to use the module after the failed publish",
	)

	mod.Free()

	afterModFree := agentRootMemoryNode(t, shm, agentName)
	require.Equalf(
		t, afterUnrelatedFree.BFreeCount+1, afterModFree.BFreeCount,
		"the failed-publish module is dangling, so its owner's free must destroy it",
	)

	// Construct decap's type once more: nothing is left for it to
	// destroy, since each earlier config was destroyed by its own owner.
	_, err = cdecap.NewModuleConfig(agent, "decap2")
	require.NoError(t, err)

	afterDrain := agentRootMemoryNode(t, shm, agentName)
	require.Equalf(
		t, afterModFree.BFreeCount, afterDrain.BFreeCount,
		"a new construction must destroy nothing: every earlier config was already destroyed by its own owner",
	)
}
