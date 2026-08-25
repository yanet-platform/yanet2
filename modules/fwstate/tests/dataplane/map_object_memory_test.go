package fwstate_test

import (
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
