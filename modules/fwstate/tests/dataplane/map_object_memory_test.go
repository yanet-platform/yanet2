package fwstate_test

import (
	"syscall"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/testutils"
	fwstate "github.com/yanet-platform/yanet2/modules/fwstate/tests/dataplane"
)

// verifies that map-object layers are charged to the object's own memory
// context: growing and reclaiming the layer chain moves only the object's
// accounting counters, never the agent's.
func Test_MapObjectMemory_LayerBytesChargedToObjectContext(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_map_obj_mem", datasize.MB*64)
	defer memCtx.Free()

	agent := fwstate.NewHarnessAgent(memCtx)
	require.NotNil(t, agent)

	object := fwstate.NewMapV4Object(agent, "mem_v4")
	require.NotNil(t, object)

	agentBefore := fwstate.AgentMemCounters(agent)
	objectBefore := fwstate.ObjectMemCounters(object)

	require.Zero(t, fwstate.InsertMapV4Layer(object, 1024, 64, 1))

	agentAfter := fwstate.AgentMemCounters(agent)
	objectAfter := fwstate.ObjectMemCounters(object)
	require.Greater(t, objectAfter.BallocSize, objectBefore.BallocSize)
	require.Equal(t, agentBefore, agentAfter)

	// A far-future expiry parks the empty tail layer; releasing it must
	// charge the same context the layer was allocated from.
	require.Zero(t, fwstate.UnlinkStaleMapV4Layers(object, 1<<62))
	fwstate.FreeStaleMapV4Layers(object)

	agentAfter = fwstate.AgentMemCounters(agent)
	objectAfter = fwstate.ObjectMemCounters(object)
	require.Greater(t, objectAfter.BfreeSize, objectBefore.BfreeSize)
	require.Equal(t, agentBefore, agentAfter)
}

// verifies that a zero stash size selects the default of 64 records, that
// a map is created only once and refuses a zero worker count or an
// out-of-range stash size, and that destroying the object returns every
// byte the stash, the layer and the object took.
func Test_MapObjectMemory_StashCreatedOnceAndFreedWithObject(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_map_obj_stash", datasize.MB*64)
	defer memCtx.Free()

	agent := fwstate.NewHarnessAgent(memCtx)
	require.NotNil(t, agent)
	freeBefore := fwstate.AgentFreeBytes(agent)

	object := fwstate.NewBareMapV4Object(agent, "stash_v4")
	require.NotNil(t, object)

	for _, invalid := range []struct {
		workerCount uint16
		stashSize   uint64
	}{
		{workerCount: 2, stashSize: 1<<20 + 1},
		{workerCount: 2, stashSize: fwstate.StashRecordSize - 1},
		{workerCount: 0, stashSize: 0},
	} {
		rc, err := fwstate.CreateMapV4(object, invalid.workerCount, 1024, invalid.stashSize)
		require.Equal(t, -1, rc)
		require.ErrorIs(t, err, syscall.EINVAL)
		require.Zero(t, fwstate.MapV4StashSize(object))
	}

	rc, _ := fwstate.CreateMapV4(object, 2, 1024, 0)
	require.Zero(t, rc)
	require.Equal(t, 64*fwstate.StashRecordSize, fwstate.MapV4StashSize(object))
	require.Equal(t, fwstate.DefaultStashSize, fwstate.MapV4StashSize(object))

	rc, err := fwstate.CreateMapV4(object, 2, 1024, 1024)
	require.Equal(t, -1, rc)
	require.ErrorIs(t, err, syscall.EEXIST)
	require.Equal(t, fwstate.DefaultStashSize, fwstate.MapV4StashSize(object))
	require.Less(t, fwstate.AgentFreeBytes(agent), freeBefore)

	fwstate.DestroyMapV4Object(agent, object)
	require.Equal(t, freeBefore, fwstate.AgentFreeBytes(agent))
}

// verifies that a module config holds one read position per dataplane
// worker and that creating and destroying it returns every byte it took,
// the positions included, to the allocator.
func Test_ModuleConfigMemory_FreedWithConfig(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_config_cycle", datasize.MB*64)
	defer memCtx.Free()

	agent := fwstate.NewHarnessAgent(memCtx)
	require.NotNil(t, agent)

	before := fwstate.AgentFreeBytes(agent)
	positions, err := fwstate.CreateAndFreeModuleConfig(agent)
	require.NoError(t, err)
	require.Equal(t, uint64(fwstate.HarnessWorkerCount), positions)
	require.Equal(t, before, fwstate.AgentFreeBytes(agent))
}

// verifies that a stash whose buffers run out of memory part way frees
// everything it allocated, leaves the object without a stash and lets a
// later, smaller create succeed.
func Test_MapObjectMemory_FailedStashCreateUnwinds(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_map_obj_stash_fail", datasize.MB*64)
	defer memCtx.Free()

	agent := fwstate.NewHarnessAgent(memCtx)
	require.NotNil(t, agent)
	object := fwstate.NewBareMapV4Object(agent, "stash_fail_v4")
	require.NotNil(t, object)
	freeBefore := fwstate.AgentFreeBytes(agent)

	// 65535 workers with a default buffer each need far more than the
	// arena.
	rc, err := fwstate.CreateMapV4(object, 65535, 1024, 0)
	require.Equal(t, -1, rc)
	require.ErrorIs(t, err, syscall.ENOMEM)
	require.Equal(t, freeBefore, fwstate.AgentFreeBytes(agent))
	require.Zero(t, fwstate.MapV4StashSize(object))

	rc, _ = fwstate.CreateMapV4(object, 2, 1024, 0)
	require.Zero(t, rc)
	require.Equal(t, fwstate.DefaultStashSize, fwstate.MapV4StashSize(object))

	fwstate.DestroyMapV4Object(agent, object)
}

// verifies that a map whose first layer cannot be built frees the stash it
// had already allocated and stays uncreated.
func Test_MapObjectMemory_FailedLayerCreateFreesStash(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_map_obj_layer_fail", datasize.MB*64)
	defer memCtx.Free()

	agent := fwstate.NewHarnessAgent(memCtx)
	require.NotNil(t, agent)
	object := fwstate.NewBareMapV4Object(agent, "layer_fail_v4")
	require.NotNil(t, object)
	freeBefore := fwstate.AgentFreeBytes(agent)

	// An index far beyond the arena cannot be allocated.
	rc, _ := fwstate.CreateMapV4(object, 2, 1<<30, 0)
	require.Equal(t, -1, rc)
	require.Equal(t, freeBefore, fwstate.AgentFreeBytes(agent))
	require.Zero(t, fwstate.MapV4StashSize(object))

	rc, _ = fwstate.CreateMapV4(object, 2, 1024, 0)
	require.Zero(t, rc)

	fwstate.DestroyMapV4Object(agent, object)
}
