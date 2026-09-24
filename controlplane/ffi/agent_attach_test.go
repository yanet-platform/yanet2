package ffi_test

import (
	"math"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	testshm "github.com/yanet-platform/yanet2/common/go/testutils/shm"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// Test_SharedMemory_AgentAttach_InstanceBounds verifies that the last real
// instance attaches and invalid indices fail before traversing unmapped storage.
func Test_SharedMemory_AgentAttach_InstanceBounds(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		instance uint32
		valid    bool
	}{
		{name: "last valid instance", instance: 0, valid: true},
		{name: "first index beyond the single instance", instance: 1},
		{name: "maximum index cannot traverse the mapping", instance: math.MaxUint32},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			path := testshm.NewStorage(t)
			memory, err := ffi.AttachSharedMemory(path)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, memory.Detach()) })
			agents := memory.DPConfig(0).Agents()
			agent, err := memory.AgentAttach("boundary", scenario.instance, 64*datasize.MB)
			if agent != nil {
				t.Cleanup(func() { require.NoError(t, agent.Close()) })
			}
			if scenario.valid {
				require.NoError(t, err)
				require.NotNil(t, agent)
				return
			}
			require.Nil(t, agent)
			require.ErrorContains(t, err, "out of range [0, 1)")
			require.Equal(t, agents, memory.DPConfig(0).Agents())
		})
	}
}
