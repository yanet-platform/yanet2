package pdump_test

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	pdump "github.com/yanet-platform/yanet2/modules/pdump/controlplane"
	pdumppb "github.com/yanet-platform/yanet2/modules/pdump/controlplane/pdumppb/v1"
	"github.com/yanet-platform/yanet2/modules/pdump/internal/testutils"
)

// Test_ModuleConfig_SetRate_RejectsBeforeDereference verifies that invalid
// rates are rejected by C before it accesses even an uninitialized config.
func Test_ModuleConfig_SetRate_RejectsBeforeDereference(t *testing.T) {
	config := &pdump.ModuleConfig{}
	for _, rate := range []uint64{pdumppb.MaxRatePPS + 1, ^uint64(0)} {
		require.ErrorIs(t, config.SetRate(rate), syscall.EINVAL)
	}
}

// Test_ModuleConfig_SetRate_ReusesDisabledAllocation verifies that disabling
// retains storage, reenabling reuses it, and destruction releases it.
func Test_ModuleConfig_SetRate_ReusesDisabledAllocation(t *testing.T) {
	_, agent := newTestBackend(t)
	before := agent.BlockAllocatorFreeSize()
	config, err := pdump.NewModuleConfig(agent, "rate-lifecycle")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, config.Free()) })

	beforeEnable := agent.BlockAllocatorFreeSize()
	testutils.SetWorkerTime(agent, 123_456_789)
	require.NoError(t, config.SetRate(32001))
	allocated := agent.BlockAllocatorFreeSize()
	require.Less(t, allocated, beforeEnable)

	testutils.SetWorkerTime(agent, 0)
	require.NoError(t, config.SetRate(0))
	require.Equal(t, allocated, agent.BlockAllocatorFreeSize())

	testutils.SetWorkerTime(agent, 987_654_321)
	require.NoError(t, config.SetRate(pdumppb.MaxRatePPS))
	require.Equal(t, allocated, agent.BlockAllocatorFreeSize())
	require.NoError(t, config.SetRate(0))
	require.NoError(t, config.Free())
	require.Equal(t, before, agent.BlockAllocatorFreeSize())
}
