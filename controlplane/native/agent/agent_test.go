//go:build linux && amd64 && !asan

package agent_test

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/nativetest"
)

// newFixture returns a one-worker dataplane harness with blackhole.
func newFixture(t *testing.T) *nativetest.Fixture {
	t.Helper()
	return nativetest.NewFixture(t, dataplaneut.Config{WorkerCount: 1, Modules: []string{"blackhole"}})
}

// Test_Attach_MatchesC verifies that attaching an agent from Go leaves
// the storage byte for byte as attaching it from C does, for a new agent
// and for one superseding an agent of the same name.
func Test_Attach_MatchesC(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, memory *nativetest.Fixture)
	}{
		{"new agent", func(*testing.T, *nativetest.Fixture) {}},
		{"superseding agent", func(t *testing.T, memory *nativetest.Fixture) {
			_, err := memory.Harness.SharedMemory().AgentAttach("probe", 0, 2*datasize.MB)
			require.NoError(t, err)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := newFixture(t)
			tc.setup(t, memory)
			memory.CompareWithC(t,
				func() {
					_, err := memory.Harness.SharedMemory().AgentAttach("probe", 0, 8*datasize.MB)
					require.NoError(t, err)
				},
				func() {
					_, err := agent.Attach(memory.Instance(), "probe", uint64(8*datasize.MB))
					require.NoError(t, err)
				},
			)
		})
	}
}

// Test_Attach_SharesRegistryWithC verifies that an agent Go attached is
// listed by the cgo control plane with its name, process and memory.
func Test_Attach_SharesRegistryWithC(t *testing.T) {
	memory := newFixture(t)
	attached, err := agent.Attach(memory.Instance(), "native", uint64(8*datasize.MB))
	require.NoError(t, err)
	var found bool
	for _, info := range memory.Harness.SharedMemory().DPConfig(0).Agents() {
		if info.Name != "native" {
			continue
		}
		found = true
		require.Equal(t, uint32(attached.Agent.Pid), info.Instances[0].PID)
		require.Equal(t, uint64(8*datasize.MB), info.Instances[0].MemoryLimit)
	}
	require.True(t, found, "the cgo control plane does not list the native agent")
}
