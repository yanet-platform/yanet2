//go:build linux && (amd64 || arm64) && cgo

package agent_test

import (
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
	"github.com/yanet-platform/yanet2/modules/blackhole/bindings/go/cblackhole"
)

// newFixture returns a one-worker dataplane with blackhole and the
// default zones.
func newFixture(t *testing.T) *nativetest.Fixture {
	t.Helper()
	return nativetest.NewFixture(t, dataplaneut.Config{WorkerCount: 1, Modules: []string{"blackhole"}})
}

// attachC attaches an agent through the cgo control plane.
func attachC(t *testing.T, memory *nativetest.Fixture, name string, limit datasize.ByteSize) *ffi.Agent {
	t.Helper()
	attached, err := memory.Harness.SharedMemory().AgentAttach(name, 0, limit)
	require.NoError(t, err)
	return attached
}

// agentOf returns the shared-memory agent behind a cgo handle.
func agentOf(attached *ffi.Agent) *layout.Agent {
	return (*layout.Agent)(attached.AsRawPtr())
}

// supersededChain attaches two agents of one name through C, the older
// marked as holding a configuration, and returns both, oldest first.
//
// The mark keeps the newer attach from sweeping the older. The holds are
// only counts written into the shared agents: no configuration refers to
// either agent, so a test may drop a count to make its agent reclaimable
// without leaving a configuration dangling.
func supersededChain(t *testing.T, memory *nativetest.Fixture, name string) (*layout.Agent, *layout.Agent) {
	t.Helper()
	oldest := agentOf(attachC(t, memory, name, 2*datasize.MB))
	atomic.StoreUint64(&oldest.LoadedEntityCount, 1)
	newer := agentOf(attachC(t, memory, name, 2*datasize.MB))
	require.Same(t, oldest, newer.Prev.Get())
	return oldest, newer
}

// registered returns the agent the instance's registry holds under that
// name, or nil.
func registered(instance *zone.Instance, name string) *layout.Agent {
	registry := instance.Cp.AgentRegistry.Get()
	slots := registry.Agents(int(registry.Count))
	for idx := range slots {
		if found := slots[idx].Get(); shm.String(found.Name[:]) == name {
			return found
		}
	}
	return nil
}

// Test_Attach_MatchesC verifies that a Go attach leaves the storage as a
// C attach does and registers the new agent over the predecessor C keeps.
//
// A predecessor that holds a configuration stays chained behind the new
// agent; one that holds nothing is swept by the attach itself, also from
// below a predecessor that stays.
func Test_Attach_MatchesC(t *testing.T) {
	cases := []struct {
		name  string
		agent string
		// setup prepares the storage and returns the predecessor the new
		// agent must keep, or nil.
		setup func(t *testing.T, memory *nativetest.Fixture) *layout.Agent
	}{
		{
			name:  "new agent",
			agent: "probe",
			setup: func(*testing.T, *nativetest.Fixture) *layout.Agent { return nil },
		},
		{
			name:  "new agent with the longest name",
			agent: strings.Repeat("n", agent.NameSize-1),
			setup: func(*testing.T, *nativetest.Fixture) *layout.Agent { return nil },
		},
		{
			name:  "superseding an agent that holds a published configuration",
			agent: "probe",
			setup: func(t *testing.T, memory *nativetest.Fixture) *layout.Agent {
				previous := attachC(t, memory, "probe", 2*datasize.MB)
				config, err := cblackhole.NewModuleConfig(previous, "held")
				require.NoError(t, err)
				require.NoError(t, previous.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}))
				return agentOf(previous)
			},
		},
		{
			name:  "sweeping a superseded agent that holds nothing",
			agent: "probe",
			setup: func(t *testing.T, memory *nativetest.Fixture) *layout.Agent {
				attachC(t, memory, "probe", 2*datasize.MB)
				return nil
			},
		},
		{
			name:  "sweeping a reclaimable agent below one that holds a configuration",
			agent: "probe",
			setup: func(t *testing.T, memory *nativetest.Fixture) *layout.Agent {
				oldest, newer := supersededChain(t, memory, "probe")
				atomic.StoreUint64(&newer.LoadedEntityCount, 1)
				atomic.StoreUint64(&oldest.LoadedEntityCount, 0)
				return newer
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := newFixture(t)
			previous := tc.setup(t, memory)
			var attached *agent.Agent
			memory.CompareWithC(
				t,
				func() { attachC(t, memory, tc.agent, 8*datasize.MB) },
				func() {
					var err error
					attached, err = agent.Attach(memory.Instance(), tc.agent, uint64(8*datasize.MB))
					require.NoError(t, err)
				},
			)
			require.Same(t, attached.Agent, registered(memory.Instance(), tc.agent))
			require.Same(t, previous, attached.Agent.Prev.Get())
		})
	}
}

// Test_Attach_FailureMatchesC verifies that a failed attach leaves the
// storage as C's failed attach does, reporting exhaustion if the zone ran out.
//
// A failed attach returns what it took, drops its generation pin and
// still sweeps the superseded agents.
func Test_Attach_FailureMatchesC(t *testing.T) {
	cases := []struct {
		name     string
		cpMemory datasize.ByteSize
		limit    datasize.ByteSize
		setup    func(t *testing.T, memory *nativetest.Fixture)
		// exhausted marks an attach the zone ran out of memory for.
		exhausted bool
	}{
		{
			name:  "zero memory limit",
			limit: 0,
			setup: func(*testing.T, *nativetest.Fixture) {},
		},
		{
			// The zone holds one block of the largest size, so the first
			// arena fits and the second does not.
			name:      "zone exhausted partway through the arenas",
			cpMemory:  96 * datasize.MB,
			limit:     2 * shmalloc.MaxSize,
			setup:     func(*testing.T, *nativetest.Fixture) {},
			exhausted: true,
		},
		{
			name:  "zone exhausted by the registry growth after the arenas fit",
			limit: datasize.MB,
			setup: func(t *testing.T, memory *nativetest.Fixture) {
				nativetest.LeaveOnly(
					t, &memory.Instance().Cp.BlockAllocator,
					unsafe.Sizeof(layout.Agent{}), unsafe.Sizeof(layout.AgentArena{}), uintptr(datasize.MB),
				)
			},
			exhausted: true,
		},
		{
			name:  "failed attach sweeping a reclaimable agent",
			limit: 0,
			setup: func(t *testing.T, memory *nativetest.Fixture) {
				oldest, _ := supersededChain(t, memory, "chain")
				atomic.StoreUint64(&oldest.LoadedEntityCount, 0)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := nativetest.NewFixture(t, dataplaneut.Config{
				CPMemory:    uint64(tc.cpMemory),
				WorkerCount: 1,
				Modules:     []string{"blackhole"},
			})
			tc.setup(t, memory)
			memory.CompareWithC(
				t,
				func() {
					_, err := memory.Harness.SharedMemory().AgentAttach("probe", 0, tc.limit)
					require.Error(t, err)
				},
				func() {
					_, err := agent.Attach(memory.Instance(), "probe", uint64(tc.limit))
					if tc.exhausted {
						require.ErrorIs(t, err, shmalloc.ErrNoMemory)
						return
					}
					require.Error(t, err)
					require.NotErrorIs(t, err, shmalloc.ErrNoMemory)
				},
			)
			require.Nil(t, registered(memory.Instance(), "probe"))
		})
	}
}

// Test_Attach_SharesRegistryWithC verifies that an agent Go attached is
// listed by the cgo control plane with its name, process and memory limit.
func Test_Attach_SharesRegistryWithC(t *testing.T) {
	memory := newFixture(t)
	_, err := agent.Attach(memory.Instance(), "native", uint64(8*datasize.MB))
	require.NoError(t, err)
	var found bool
	for _, info := range memory.Harness.SharedMemory().DPConfig(0).Agents() {
		if info.Name != "native" {
			continue
		}
		found = true
		require.NotEmpty(t, info.Instances)
		require.Equal(t, uint32(os.Getpid()), info.Instances[0].PID)
		require.Equal(t, uint64(8*datasize.MB), info.Instances[0].MemoryLimit)
	}
	require.True(t, found, "the cgo control plane does not list the native agent")
}
