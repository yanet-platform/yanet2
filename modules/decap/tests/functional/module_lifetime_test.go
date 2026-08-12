package decap_test

import (
	"math"
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	decap "github.com/yanet-platform/yanet2/modules/decap/controlplane"
)

// newLifetimeHarness starts a minimal single-worker decap harness, matching
// the restart-leak suite's own setup, for module-configuration lifetime
// tests that never process a packet.
func newLifetimeHarness(t *testing.T) *dataplaneut.Harness {
	t.Helper()

	h, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(datasize.MB * 16),
		DPMemory:      uint64(datasize.MB * 4),
		WorkerCount:   1,
		Devices:       []string{"port0"},
		Modules:       []string{"decap"},
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(h.Free)

	return h
}

// agentRootMemoryNode returns the named agent's own root memory-context
// node.
//
// A module's outer structure is always allocated and freed directly against
// this root context, so its free count advances once and only once per
// module actually torn down. The live generation-reference count is not
// used here: it moves whether or not a teardown ran, so it cannot
// distinguish a real destroy from a skipped one.
func agentRootMemoryNode(t *testing.T, shm *ffi.SharedMemory, name string) ffi.AgentMemoryNode {
	t.Helper()

	for _, agentInfo := range shm.DPConfig(0).Agents() {
		if agentInfo.Name != name {
			continue
		}
		require.Lenf(t, agentInfo.Instances, 1, "agent %q: expected exactly one live instance", name)

		for _, node := range agentInfo.Instances[0].MemoryTree {
			if node.ParentIdx == math.MaxUint32 {
				return node
			}
		}
		t.Fatalf("agent %q: no root memory-context node in its snapshot", name)
	}

	t.Fatalf("agent %q not found in dataplane config", name)
	return ffi.AgentMemoryNode{}
}

// TestModuleLifetime_LiveGeneration_ReleaseDoesNotDestroy verifies that
// releasing a module's creator handle does not tear it down while the
// currently published generation still references it.
//
// A regression that frees unconditionally on release would advance the
// agent's root context free count by one right after release, even though
// nothing has yet superseded the generation this module is published in.
func TestModuleLifetime_LiveGeneration_ReleaseDoesNotDestroy(t *testing.T) {
	h := newLifetimeHarness(t)
	shm := h.SharedMemory()

	agent, err := shm.AgentAttach("cpml-live", 0, datasize.MB*1)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	handle, err := decap.NewBackend(agent).UpdateModule(
		"decap0", []netip.Prefix{netip.MustParsePrefix("2001:db8::/32")},
	)
	require.NoError(t, err)

	before := agentRootMemoryNode(t, shm, "cpml-live")

	handle.Free()

	after := agentRootMemoryNode(t, shm, "cpml-live")
	require.Equalf(
		t, before.BFreeCount, after.BFreeCount,
		"creator release destroyed the module while the live generation still referenced it",
	)
}

// TestModuleLifetime_Superseded_DestroyedOnNextOwnAPICall guards a superseded
// module's destroy timing.
//
// Publishing "decap0" a second time parks the first module, but only the next
// construction of decap's type drains that park list and destroys it, never a
// release by itself.
func TestModuleLifetime_Superseded_DestroyedOnNextOwnAPICall(t *testing.T) {
	h := newLifetimeHarness(t)
	shm := h.SharedMemory()

	agent, err := shm.AgentAttach("cpml-supersede", 0, datasize.MB*1)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	backend := decap.NewBackend(agent)

	first, err := backend.UpdateModule(
		"decap0", []netip.Prefix{netip.MustParsePrefix("2001:db8::/32")},
	)
	require.NoError(t, err)
	first.Free()

	mid := agentRootMemoryNode(t, shm, "cpml-supersede")

	second, err := backend.UpdateModule(
		"decap0", []netip.Prefix{netip.MustParsePrefix("2001:db9::/32")},
	)
	require.NoError(t, err)

	afterPublish := agentRootMemoryNode(t, shm, "cpml-supersede")
	require.Equalf(
		t, mid.BFreeCount, afterPublish.BFreeCount,
		"the superseded module was destroyed before decap's own API drained it",
	)

	second.Free()

	afterSecondRelease := agentRootMemoryNode(t, shm, "cpml-supersede")
	require.Equalf(
		t, mid.BFreeCount, afterSecondRelease.BFreeCount,
		"releasing the live generation's own creator handle destroyed the parked module by itself",
	)

	_, err = backend.UpdateModule(
		"decap0", []netip.Prefix{netip.MustParsePrefix("2001:dba::/32")},
	)
	require.NoError(t, err)

	afterThirdCreate := agentRootMemoryNode(t, shm, "cpml-supersede")
	require.Equalf(
		t, mid.BFreeCount+1, afterThirdCreate.BFreeCount,
		"the superseded module was not destroyed once decap's own API constructed again",
	)
}

// TestModuleLifetime_PinnedGeneration_DefersDestruction guards deferred
// destruction under a pinned reader generation.
//
// An unlocked counters reader's pin on a superseded generation defers that
// module's destruction until the pin drops, and destruction then still needs
// decap's own API to run before it happens, exactly once.
func TestModuleLifetime_PinnedGeneration_DefersDestruction(t *testing.T) {
	h := newLifetimeHarness(t)
	shm := h.SharedMemory()

	agent, err := shm.AgentAttach("cpml-pinned", 0, datasize.MB*1)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	backend := decap.NewBackend(agent)

	first, err := backend.UpdateModule(
		"decap0", []netip.Prefix{netip.MustParsePrefix("2001:db8::/32")},
	)
	require.NoError(t, err)
	first.Free()

	// Pin the generation that publishes the first module before superseding
	// it, the same way an unlocked counters reader would.
	pin := pinCurrentGeneration(agent.AsRawPtr())

	mid := agentRootMemoryNode(t, shm, "cpml-pinned")

	second, err := backend.UpdateModule(
		"decap0", []netip.Prefix{netip.MustParsePrefix("2001:db9::/32")},
	)
	require.NoError(t, err)
	second.Free()

	afterSecond := agentRootMemoryNode(t, shm, "cpml-pinned")
	require.Equalf(
		t, mid.BFreeCount, afterSecond.BFreeCount,
		"the pinned generation's module was destroyed while the pin was still held",
	)

	pin.release()

	afterUnpin := agentRootMemoryNode(t, shm, "cpml-pinned")
	require.Equalf(
		t, mid.BFreeCount, afterUnpin.BFreeCount,
		"dropping the pin destroyed the module directly, without decap's own API draining it",
	)

	_, err = backend.UpdateModule(
		"decap0", []netip.Prefix{netip.MustParsePrefix("2001:dba::/32")},
	)
	require.NoError(t, err)

	afterThird := agentRootMemoryNode(t, shm, "cpml-pinned")
	require.Equalf(
		t, mid.BFreeCount+1, afterThird.BFreeCount,
		"the module was not destroyed once the pin dropped and decap's own API ran again",
	)
}
