package builtin_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/builtin"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// Test_Inspect_AllocatorSnapshots verifies that shared and agent free
// capacity remain distinct and retain every size class through the FFI.
func Test_Inspect_AllocatorSnapshots(t *testing.T) {
	shm := newMemoryHarness(t).SharedMemory()
	attachModuleAgent(t, shm, "route")
	dpConfig := shm.DPConfig(0)
	shared := dpConfig.CPAllocatorStats()
	agents := dpConfig.Agents()

	response, err := builtin.NewInspect(0, shm).Inspect(t.Context(), &ynpb.InspectRequest{})
	require.NoError(t, err)
	info := response.GetInstanceInfo()
	require.NotNil(t, info)
	assertAllocatorInfo(t, shared, info.GetCpAllocator())
	require.NotZero(t, shared.FreeBytes)
	require.Len(t, shared.Pools, 24)
	for _, agent := range agents {
		if agent.Name != "route" {
			continue
		}
		for _, reported := range info.GetAgents() {
			if reported.GetName() == agent.Name {
				require.Len(t, reported.GetInstances(), len(agent.Instances))
				for idx, instance := range agent.Instances {
					actual := reported.GetInstances()[idx]
					assertAllocatorInfo(t, instance.Allocator, actual.GetAllocator())
					require.Equal(t, actual.GetFreeBytes(), actual.GetAllocator().GetFreeBytes())
				}
				return
			}
		}
	}
	require.FailNow(t, "attached agent was not reported")
}

// assertAllocatorInfo checks that a wire snapshot preserves both its
// summary and all raw size classes, including zero-valued classes.
func assertAllocatorInfo(t *testing.T, expected ffi.AllocatorStats, actual *ynpb.AllocatorInfo) {
	t.Helper()
	require.NotNil(t, actual)
	require.Equal(t, expected.FreeBytes, actual.GetFreeBytes())
	require.Equal(t, expected.LargestFreeBlockBytes, actual.GetLargestFreeBlockBytes())
	require.Len(t, actual.GetPools(), len(expected.Pools))
	var total uint64
	var largest uint64
	for idx, pool := range expected.Pools {
		require.Equal(t, pool.BlockSizeBytes, actual.GetPools()[idx].GetBlockSizeBytes())
		require.Equal(t, pool.FreeBlocks, actual.GetPools()[idx].GetFreeBlocks())
		total += pool.BlockSizeBytes * pool.FreeBlocks
		if pool.FreeBlocks != 0 {
			largest = pool.BlockSizeBytes
		}
	}
	require.Equal(t, total, actual.GetFreeBytes())
	require.Equal(t, largest, actual.GetLargestFreeBlockBytes())
}
