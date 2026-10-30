package fwstate_test

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	modulefwstate "github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
	fwstate "github.com/yanet-platform/yanet2/objects/fwstate/bindings/go/cfwstate"
)

// Test_MapObjectMemory_LayerBytesChargedToObjectContext verifies that layers
// allocate and release memory through their owning object.
//
// Growing and reclaiming the chain changes the object accounting while the
// agent context counters remain unchanged.
func Test_MapObjectMemory_LayerBytesChargedToObjectContext(t *testing.T) {
	harness, agent := newHarnessAgent(t)

	object, err := fwstate.NewMapObjectConfig(agent, "mem_v4", fwstate.KindV4)
	require.NoError(t, err)
	err = object.CreateMap(fwstate.MapConfig{IndexSize: 1024, ExtraBucketCount: 64, WorkerCount: 2})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, object.Free()) })

	agentBefore := memoryNode(t, harness, "")
	objectBefore := memoryNode(t, harness, object.Name())

	require.NoError(t, object.InsertLayer(1024, 64, 2))

	agentAfter := memoryNode(t, harness, "")
	objectAfter := memoryNode(t, harness, object.Name())
	require.Greater(t, objectAfter.BAllocSize, objectBefore.BAllocSize)
	require.Equal(t, agentBefore, agentAfter)

	// A far-future expiry parks the empty tail layer; releasing it must charge the
	// same context the layer was allocated from.
	require.NoError(t, object.UnlinkStaleLayers(1<<62))
	require.NoError(t, object.FreeStaleLayers())

	agentAfter = memoryNode(t, harness, "")
	objectAfter = memoryNode(t, harness, object.Name())
	require.Greater(t, objectAfter.BFreeSize, objectBefore.BFreeSize)
	require.Equal(t, agentBefore, agentAfter)
}

// Test_MapObjectMemory_StashCreatedOnceAndFreedWithObject verifies that stash
// creation and destruction preserve the allocation contract.
//
// Zero size selects 64 records; invalid sizes or worker counts and duplicate
// creation are refused. Destruction returns all object, layer and stash bytes.
func Test_MapObjectMemory_StashCreatedOnceAndFreedWithObject(t *testing.T) {
	_, agent := newHarnessAgent(t)
	freeBefore := agent.BlockAllocatorFreeSize()

	object, err := fwstate.NewMapObjectConfig(agent, "stash_v4", fwstate.KindV4)
	require.NoError(t, err)

	for _, tc := range []struct {
		workerCount uint16
		stashSize   uint64
	}{
		{workerCount: 2, stashSize: 1<<20 + 1},
		{workerCount: 2, stashSize: uint64(fwstate.StashRecordSize) - 1},
		{workerCount: 0, stashSize: 0},
	} {
		err = object.CreateMap(fwstate.MapConfig{WorkerCount: tc.workerCount, IndexSize: 1024, ExtraBucketCount: 64, StashSize: tc.stashSize})
		requireCreateStatus(t, err, -1)
		require.ErrorIs(t, err, syscall.EINVAL)
		require.Zero(t, xerror.Unwrap(object.StashLayout(0)).StashSize)
	}

	require.NoError(t, object.CreateMap(fwstate.MapConfig{WorkerCount: 2, IndexSize: 1024, ExtraBucketCount: 64}))
	require.Equal(t, uint64(64*fwstate.StashRecordSize), xerror.Unwrap(object.StashLayout(0)).StashSize)
	require.Equal(t, fwstate.DefaultStashSize, xerror.Unwrap(object.StashLayout(0)).StashSize)

	err = object.CreateMap(fwstate.MapConfig{WorkerCount: 2, IndexSize: 1024, ExtraBucketCount: 64, StashSize: 1024})
	requireCreateStatus(t, err, -1)
	require.ErrorIs(t, err, syscall.EEXIST)
	require.Equal(t, fwstate.DefaultStashSize, xerror.Unwrap(object.StashLayout(0)).StashSize)
	require.Less(t, agent.BlockAllocatorFreeSize(), freeBefore)

	require.NoError(t, object.Free())
	require.Equal(t, freeBefore, agent.BlockAllocatorFreeSize())
}

// Test_ModuleConfigMemory_FreedWithConfig verifies that destroying a config
// returns every allocated byte, including its worker cursors.
//
// A live config owns one allocated read position per dataplane worker.
func Test_ModuleConfigMemory_FreedWithConfig(t *testing.T) {
	_, agent := newHarnessAgent(t)

	before := agent.BlockAllocatorFreeSize()
	config, err := modulefwstate.NewModuleConfig(agent, "cycle", nil, nil, "", "")
	require.NoError(t, err)
	require.Equal(t, uint64(2), config.StashPositionCount())
	require.NoError(t, config.Free())
	require.Equal(t, before, agent.BlockAllocatorFreeSize())
}

// Test_MapObjectMemory_FailedStashCreateUnwinds verifies that partial stash
// allocation failure releases every completed allocation.
//
// The object retains no stash, and a later smaller create succeeds.
func Test_MapObjectMemory_FailedStashCreateUnwinds(t *testing.T) {
	_, agent := newHarnessAgent(t)
	object, err := fwstate.NewMapObjectConfig(agent, "stash_fail_v4", fwstate.KindV4)
	require.NoError(t, err)
	freeBefore := agent.BlockAllocatorFreeSize()

	// 65535 workers with a default buffer each need far more than the arena.
	err = object.CreateMap(fwstate.MapConfig{WorkerCount: 65535, IndexSize: 1024, ExtraBucketCount: 64})
	requireCreateStatus(t, err, -1)
	require.ErrorIs(t, err, syscall.ENOMEM)
	require.Equal(t, freeBefore, agent.BlockAllocatorFreeSize())
	require.Zero(t, xerror.Unwrap(object.StashLayout(0)).StashSize)

	require.NoError(t, object.CreateMap(fwstate.MapConfig{WorkerCount: 2, IndexSize: 1024, ExtraBucketCount: 64}))
	require.Equal(t, fwstate.DefaultStashSize, xerror.Unwrap(object.StashLayout(0)).StashSize)

	require.NoError(t, object.Free())
}

// Test_MapObjectMemory_FailedLayerCreateFreesStash verifies that first-layer
// allocation failure releases the already allocated stash.
//
// The map stays uncreated and can subsequently be created with a smaller layer.
func Test_MapObjectMemory_FailedLayerCreateFreesStash(t *testing.T) {
	_, agent := newHarnessAgent(t)
	object, err := fwstate.NewMapObjectConfig(agent, "layer_fail_v4", fwstate.KindV4)
	require.NoError(t, err)
	freeBefore := agent.BlockAllocatorFreeSize()

	// An index far beyond the arena cannot be allocated.
	err = object.CreateMap(fwstate.MapConfig{WorkerCount: 2, IndexSize: 1 << 30, ExtraBucketCount: 64})
	requireCreateStatus(t, err, -1)
	require.Error(t, err)
	require.Equal(t, freeBefore, agent.BlockAllocatorFreeSize())
	require.Zero(t, xerror.Unwrap(object.StashLayout(0)).StashSize)

	require.NoError(t, object.CreateMap(fwstate.MapConfig{WorkerCount: 2, IndexSize: 1024, ExtraBucketCount: 64}))

	require.NoError(t, object.Free())
}

// memoryNode selects exact context accounting from the production agent view.
func memoryNode(t *testing.T, harness *dataplaneut.Harness, name string) ffi.AgentMemoryNode {
	t.Helper()
	for _, agent := range harness.SharedMemory().DPConfig(0).Agents() {
		if agent.Name != "fwstate-test" {
			continue
		}
		require.Len(t, agent.Instances, 1)
		nodes := agent.Instances[0].MemoryTree
		require.NotEmpty(t, nodes)
		if name == "" {
			return nodes[0]
		}
		for _, node := range nodes[1:] {
			if node.Name == name && node.ParentIdx == 0 {
				return node
			}
		}
	}
	t.Fatalf("memory context %q not found", name)
	return ffi.AgentMemoryNode{}
}

// requireCreateStatus checks the native status without losing errno matching.
func requireCreateStatus(t *testing.T, err error, status int) {
	t.Helper()
	var failure *fwstate.CreateMapError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, status, failure.Status)
}
