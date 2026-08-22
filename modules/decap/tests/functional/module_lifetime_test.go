package decap_test

import (
	"errors"
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

// parked holds handles whose free was refused because a live generation
// still referenced them, mirroring what each module control plane keeps:
// the owner remembers the handle and retries it once the generations
// drain.
type parked []decap.ModuleHandle // owner: the test itself

// freeOrPark frees the handle when it is dangling and parks it when the
// free is refused.
func (m *parked) freeOrPark(handle decap.ModuleHandle) {
	if err := handle.Free(); errors.Is(err, ffi.ErrStillReferenced) {
		*m = append(*m, handle)
	}
}

// retry re-frees every parked handle, dropping the ones whose
// generations have drained.
func (m *parked) retry() {
	kept := (*m)[:0]
	for _, handle := range *m {
		if err := handle.Free(); errors.Is(err, ffi.ErrStillReferenced) {
			kept = append(kept, handle)
		}
	}
	clear((*m)[len(kept):])
	*m = kept
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
// the owner's free attempt does not tear a module down while the
// currently published generation still references it.
//
// A regression that frees unconditionally would advance the agent's root
// context free count by one right after the attempt, even though nothing
// has yet superseded the generation this module is published in.
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

	var parkedHandles parked
	parkedHandles.freeOrPark(handle)

	after := agentRootMemoryNode(t, shm, "cpml-live")
	require.Equalf(
		t, before.BFreeCount, after.BFreeCount,
		"the free attempt destroyed the module while the live generation still referenced it",
	)
	require.Len(t, parkedHandles, 1, "the refused handle must be parked with its owner")
}

// TestModuleLifetime_Superseded_DestroyedOnceGenerationRetires guards a
// superseded module's destroy timing.
//
// Publishing "decap0" a second time retires the generation that
// referenced the first module, leaving it dangling. The owner's pending
// free — refused while the module was still referenced — succeeds once
// the update itself runs it again on the way out, and never before.
func TestModuleLifetime_Superseded_DestroyedOnceGenerationRetires(t *testing.T) {
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

	// The second publish retired the generation referencing the first
	// module, so the owner's retry of its parked free destroys it.
	var parkedHandles parked
	parkedHandles.freeOrPark(first)
	parkedHandles.retry()
	require.Empty(t, parkedHandles, "the first module must be destroyed once its generation retired")

	afterPublish := agentRootMemoryNode(t, shm, "cpml-supersede")
	require.Equalf(
		t, mid.BFreeCount+1, afterPublish.BFreeCount,
		"the superseded module must be destroyed once the owner retries after the retiring update",
	)

	// The second module is the live generation's own entry: its free
	// attempt is refused, destroys nothing, and is parked with the owner.
	parkedHandles.freeOrPark(second)
	require.Len(t, parkedHandles, 1, "the live generation's module must be parked, not destroyed")

	afterSecondRelease := agentRootMemoryNode(t, shm, "cpml-supersede")
	require.Equalf(
		t, afterPublish.BFreeCount, afterSecondRelease.BFreeCount,
		"the live generation's own module must not be destroyed by its refused free",
	)

	_, err = backend.UpdateModule(
		"decap0", []netip.Prefix{netip.MustParsePrefix("2001:dba::/32")},
	)
	require.NoError(t, err)
	parkedHandles.retry()

	afterThirdCreate := agentRootMemoryNode(t, shm, "cpml-supersede")
	require.Equalf(
		t, afterSecondRelease.BFreeCount+1, afterThirdCreate.BFreeCount,
		"the second module must be destroyed once the owner retries after the retiring update",
	)
}

// TestModuleLifetime_PinnedGeneration_DefersDestruction guards deferred
// destruction under a pinned reader generation.
//
// An unlocked counters reader's pin on a superseded generation defers that
// module's destruction until the pin drops. Dropping the pin releases the
// generation in the reader's own context, where no owner code runs, so
// destruction still waits for the owner's next update to retry the
// pending free — at which point every superseded module of the round is
// destroyed, exactly once each.
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

	var parkedHandles parked
	parkedHandles.freeOrPark(first)
	require.Len(t, parkedHandles, 1,
		"the pinned generation's module must be parked, not destroyed")

	// Pin the generation that publishes the first module before superseding
	// it, the same way an unlocked counters reader would.
	pin := pinCurrentGeneration(agent.AsRawPtr())

	mid := agentRootMemoryNode(t, shm, "cpml-pinned")

	second, err := backend.UpdateModule(
		"decap0", []netip.Prefix{netip.MustParsePrefix("2001:db9::/32")},
	)
	require.NoError(t, err)
	parkedHandles.freeOrPark(second)
	parkedHandles.retry()

	afterSecond := agentRootMemoryNode(t, shm, "cpml-pinned")
	require.Equalf(
		t, mid.BFreeCount, afterSecond.BFreeCount,
		"the pinned generation's module was destroyed while the pin was still held",
	)

	pin.release()

	afterUnpin := agentRootMemoryNode(t, shm, "cpml-pinned")
	require.Equalf(
		t, mid.BFreeCount, afterUnpin.BFreeCount,
		"dropping the pin must not destroy the module directly: only its owner's free may",
	)

	_, err = backend.UpdateModule(
		"decap0", []netip.Prefix{netip.MustParsePrefix("2001:dba::/32")},
	)
	require.NoError(t, err)

	// The pin dropped and the third publish retired the second module's
	// generation, so the owner's retry destroys both parked handles.
	parkedHandles.retry()
	require.Empty(t, parkedHandles,
		"both parked modules must be destroyed once their generations drained")

	afterThird := agentRootMemoryNode(t, shm, "cpml-pinned")
	require.Equalf(
		t, mid.BFreeCount+2, afterThird.BFreeCount,
		"the superseded modules must be destroyed once the owner retries after their generations drained",
	)
}
