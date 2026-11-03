package dataplaneut_test

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/plain/bindings/go/cplain"
)

// heldAgent returns an agent of a fresh harness, of 4 MB of memory.
func heldAgent(t *testing.T) *ffi.Agent {
	t.Helper()

	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(32 * datasize.MB),
		DPMemory:      uint64(datasize.MB),
		WorkerCount:   1,
		Devices:       []string{"port0"},
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)
	agent, err := harness.SharedMemory().AgentAttach("held", 0, 4*datasize.MB)
	require.NoError(t, err)
	return agent
}

// Test_AllocatorHold_GiveAndRelease_MoveExactBlocks verifies that a hold
// leaves the agent no free memory, that giving blocks back frees exactly their
// sizes, those cut from larger held blocks included, and that releasing the
// hold gives the agent back all it had.
func Test_AllocatorHold_GiveAndRelease_MoveExactBlocks(t *testing.T) {
	agent := heldAgent(t)
	baseline := agent.BlockAllocatorFreeSize()
	allocations := dataplaneut.PoolAllocations(agent)

	hold, err := dataplaneut.HoldAllocator(agent)
	require.NoError(t, err)
	require.Zero(t, agent.BlockAllocatorFreeSize())
	require.NoError(t, hold.Give(4, 3, 0))
	require.NoError(t, hold.Give(11, 1, 0))
	require.Equal(t, 3*dataplaneut.AllocatorPoolSize(4)+dataplaneut.AllocatorPoolSize(11), agent.BlockAllocatorFreeSize())
	hold.Release()

	require.Equal(t, baseline, agent.BlockAllocatorFreeSize())
	require.Equal(t, allocations, dataplaneut.PoolAllocations(agent))
}

// Test_AllocatorHold_GiveUnavailableBlocks_IsRefused verifies that invalid
// requests and blocks larger than any held block leave the allocator empty.
func Test_AllocatorHold_GiveUnavailableBlocks_IsRefused(t *testing.T) {
	agent := heldAgent(t)
	hold, err := dataplaneut.HoldAllocator(agent)
	require.NoError(t, err)
	t.Cleanup(hold.Release)

	for _, tc := range []struct {
		name  string
		pool  int
		count int
	}{
		{name: "block larger than held memory", pool: dataplaneut.AllocatorPools - 1, count: 1},
		{name: "negative pool", pool: -1, count: 1},
		{name: "pool past the last", pool: dataplaneut.AllocatorPools, count: 1},
		{name: "pool beyond the array", pool: dataplaneut.AllocatorPools + 1, count: 1},
		{name: "negative block count", pool: 4, count: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, hold.Give(tc.pool, tc.count, 0))
			require.Zero(t, agent.BlockAllocatorFreeSize())
		})
	}
}

// Test_AllocatorHold_EmptyHold_FailsTheAgentsRequests verifies that a request
// of the agent fails while every free block is held, and succeeds once the
// hold is released.
func Test_AllocatorHold_EmptyHold_FailsTheAgentsRequests(t *testing.T) {
	agent := heldAgent(t)
	hold, err := dataplaneut.HoldAllocator(agent)
	require.NoError(t, err)

	_, err = cplain.UpdateDevices(agent, []ffi.DeviceConfig{{Name: "port0"}})
	require.Error(t, err)
	hold.Release()

	devices, err := cplain.UpdateDevices(agent, []ffi.DeviceConfig{{Name: "port0"}})
	require.NoError(t, err)
	t.Cleanup(func() {
		for _, device := range devices {
			_ = device.Free()
		}
	})
}
