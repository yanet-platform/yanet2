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

// Test_AllocatorHold_Give_FillsTheBlocks verifies that a block given back
// holds the fill byte where a request served from it reads, and that the
// probe tells a block of another fill apart.
func Test_AllocatorHold_Give_FillsTheBlocks(t *testing.T) {
	const size = 300
	pool := dataplaneut.AllocatorPoolOf(size)
	for _, tc := range []struct {
		name   string
		fill   byte
		differ bool
	}{
		{name: "block of the probed fill", fill: 0xa5, differ: false},
		{name: "block of another fill", fill: 0, differ: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent := heldAgent(t)
			hold, err := dataplaneut.HoldAllocator(agent)
			require.NoError(t, err)
			t.Cleanup(hold.Release)
			require.NoError(t, hold.Give(pool, 1, tc.fill))

			differ, err := dataplaneut.ProbeFill(agent, size, 0xa5)

			require.NoError(t, err)
			require.Equal(t, tc.differ, differ != 0, "%d bytes differ", differ)
		})
	}
}

// Test_AllocatorPoolOf_RefusedSizes_HaveNoPool verifies that a size the
// allocator refuses, empty or beyond its largest request, maps to no pool.
func Test_AllocatorPoolOf_RefusedSizes_HaveNoPool(t *testing.T) {
	for _, tc := range []struct {
		name string
		size uint64
	}{
		{name: "empty request", size: 0},
		{name: "beyond the largest block", size: dataplaneut.AllocatorPoolSize(dataplaneut.AllocatorPools-1) + 1},
		{name: "near the largest integer", size: ^uint64(0) - 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, dataplaneut.AllocatorPools, dataplaneut.AllocatorPoolOf(tc.size))
		})
	}
}
