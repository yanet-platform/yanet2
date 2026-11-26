//go:build linux && (amd64 || arm64)

package agent_test

import (
	"strings"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// Test_Attach_RefusesBadName verifies that a name the registry cannot hold
// as given is refused before the instance is touched.
//
// The instance has no zones, so any access to them would fault.
func Test_Attach_RefusesBadName(t *testing.T) {
	cases := []struct {
		name  string
		agent string
	}{
		{"name without room for its terminator", strings.Repeat("n", agent.NameSize)},
		{"name with an inner NUL", "probe\x00other"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := agent.Attach(&zone.Instance{}, tc.agent, uint64(8*datasize.MB))
			require.Error(t, err)
		})
	}
}

// Test_Attach_RefusesUnreadyInstance verifies that an instance not marked
// ready is refused as not ready before its control-plane zone is touched.
//
// The instance has no control-plane zone, so any access to it would
// fault.
func Test_Attach_RefusesUnreadyInstance(t *testing.T) {
	instance := &zone.Instance{Dp: &layout.DpConfig{}}

	_, err := agent.Attach(instance, "probe", uint64(8*datasize.MB))

	require.ErrorIs(t, err, zone.ErrNotReady)
}

// newDataplane returns a dataplane of one worker that loads the
// blackhole module, with a control-plane zone of the size given, or of
// the default size for zero.
func newDataplane(t *testing.T, cpMemory datasize.ByteSize) *nativetest.Dataplane {
	t.Helper()
	return nativetest.NewDataplane(t, nativetest.Config{CPMemory: uint64(cpMemory), Modules: []string{"blackhole"}})
}

// attach returns an agent of the name and limit, failing the test when
// the attach fails.
func attach(t *testing.T, instance *zone.Instance, name string, limit datasize.ByteSize) *agent.Agent {
	t.Helper()
	attached, err := agent.Attach(instance, name, uint64(limit))
	require.NoError(t, err)
	return attached
}

// supersededChain attaches two agents of one name and the limit, the
// older marked as holding a configuration, and returns both, oldest
// first, with the zone bytes the oldest holds.
//
// The mark keeps the newer attach from sweeping the older. The holds are
// only counts written into the shared agents: no configuration refers to
// either agent, so a test may drop a count to make its agent reclaimable
// without leaving a configuration dangling. The newer attach takes from
// the zone what the oldest holds, as it registers in the slot the oldest
// keeps.
func supersededChain(t *testing.T, instance *zone.Instance, name string, limit datasize.ByteSize) (*layout.Agent, *layout.Agent, uintptr) {
	t.Helper()
	oldest := attach(t, instance, name, limit).Agent
	atomic.StoreUint64(&oldest.LoadedEntityCount, 1)
	free := zoneFree(instance)
	newer := attach(t, instance, name, limit).Agent
	require.Same(t, oldest, newer.Prev.Get())
	return oldest, newer, free - zoneFree(instance)
}

// registered returns the agent the instance's registry holds under that
// name, or nil.
func registered(instance *zone.Instance, name string) *layout.Agent {
	agents := instance.Cp.AgentRegistry.Get()
	for found := range registry.Pointees(agents.Agents(int(agents.Count))) {
		if shm.String(found.Name[:]) == name {
			return found
		}
	}
	return nil
}

// registryCount returns how many names the instance's registry holds.
func registryCount(instance *zone.Instance) uint64 {
	return instance.Cp.AgentRegistry.Get().Count
}

// zoneFree returns the bytes the free lists of the instance's
// control-plane zone hold.
func zoneFree(instance *zone.Instance) uintptr {
	return shmalloc.FreeSize(&instance.Cp.BlockAllocator)
}

// Test_Attach_RegistersOverPredecessor verifies that an attach registers
// the new agent, stamped with the published generation, over the
// predecessor it must keep, and drops its generation pin.
//
// A predecessor that holds a configuration stays chained behind the new
// agent; one that holds nothing is swept by the attach itself, also from
// below a predecessor that stays, and its memory is back in the zone.
func Test_Attach_RegistersOverPredecessor(t *testing.T) {
	const limit = 8 * datasize.MB
	cases := []struct {
		name  string
		agent string
		// setup prepares the storage and returns the predecessor the new
		// agent must keep, or nil.
		setup func(t *testing.T, dataplane *nativetest.Dataplane) *layout.Agent
		// added is how many names the attach adds to the registry.
		added uint64
		// sweeps marks an attach that sweeps an agent of its own limit
		// registered under its name, so the zone ends as it started.
		sweeps bool
	}{
		{
			name:  "new agent",
			agent: "probe",
			setup: func(*testing.T, *nativetest.Dataplane) *layout.Agent { return nil },
			added: 1,
		},
		{
			name:  "new agent with the longest name",
			agent: strings.Repeat("n", agent.NameSize-1),
			setup: func(*testing.T, *nativetest.Dataplane) *layout.Agent { return nil },
			added: 1,
		},
		{
			name:  "superseding an agent that holds a published configuration",
			agent: "probe",
			setup: func(t *testing.T, dataplane *nativetest.Dataplane) *layout.Agent {
				previous := attach(t, dataplane.Instance(), "probe", 2*datasize.MB)
				module, err := cpconfig.NewBlackhole(previous, "held")
				require.NoError(t, err)
				require.NoError(t, cpconfig.UpdateModules(
					dataplane.Instance(), []*layout.CpModule{module}, cpconfig.WithDelivery(dataplane.Deliver),
				))
				return previous.Agent
			},
		},
		{
			name:  "sweeping a superseded agent that holds nothing",
			agent: "probe",
			setup: func(t *testing.T, dataplane *nativetest.Dataplane) *layout.Agent {
				attach(t, dataplane.Instance(), "probe", limit)
				return nil
			},
			sweeps: true,
		},
		{
			name:  "sweeping a reclaimable agent below one that holds a configuration",
			agent: "probe",
			setup: func(t *testing.T, dataplane *nativetest.Dataplane) *layout.Agent {
				oldest, newer, _ := supersededChain(t, dataplane.Instance(), "probe", limit)
				atomic.StoreUint64(&newer.LoadedEntityCount, 1)
				atomic.StoreUint64(&oldest.LoadedEntityCount, 0)
				return newer
			},
			sweeps: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dataplane := newDataplane(t, 0)
			instance := dataplane.Instance()
			previous := tc.setup(t, dataplane)
			published := instance.Cp.CpConfigGen.Get()
			references := atomic.LoadUint64(&published.Refcnt)
			names := registryCount(instance)
			free := zoneFree(instance)

			attached := attach(t, instance, tc.agent, limit)

			require.Same(t, attached.Agent, registered(instance, tc.agent))
			require.Same(t, previous, attached.Agent.Prev.Get())
			if previous != nil {
				require.Nil(t, previous.Prev.Get(), "nothing reclaimable stays chained below the predecessor")
			}
			require.Equal(t, names+tc.added, registryCount(instance))
			require.Equal(t, published.Gen, attached.Agent.Gen, "the stamp is the published generation")
			require.Equal(t, references, atomic.LoadUint64(&published.Refcnt), "the generation pin is dropped")
			if tc.sweeps {
				require.Equal(t, free, zoneFree(instance), "the swept agent's bytes are back in the zone")
			}
		})
	}
}

// Test_Attach_FailureReturnsWhatItTook verifies that a failed attach
// registers nothing, returns what it took to the zone and drops its
// generation pin, reporting exhaustion if the zone ran out.
//
// A failed attach still sweeps the superseded agents, whose memory is
// back in the zone too.
func Test_Attach_FailureReturnsWhatItTook(t *testing.T) {
	cases := []struct {
		name     string
		cpMemory datasize.ByteSize
		limit    datasize.ByteSize
		// setup prepares the storage and returns the zone bytes the
		// attach sweeps.
		setup func(t *testing.T, instance *zone.Instance) uintptr
		// exhausted marks an attach the zone ran out of memory for.
		exhausted bool
	}{
		{
			name:  "zero memory limit",
			limit: 0,
			setup: func(*testing.T, *zone.Instance) uintptr { return 0 },
		},
		{
			// The zone holds one block of the largest size, so the first
			// arena fits and the second does not.
			name:      "zone exhausted partway through the arenas",
			cpMemory:  96 * datasize.MB,
			limit:     2 * shmalloc.MaxSize,
			setup:     func(*testing.T, *zone.Instance) uintptr { return 0 },
			exhausted: true,
		},
		{
			name:  "zone exhausted by the registry growth after the arenas fit",
			limit: datasize.MB,
			setup: func(t *testing.T, instance *zone.Instance) uintptr {
				nativetest.LeaveOnly(
					t, &instance.Cp.BlockAllocator,
					unsafe.Sizeof(layout.Agent{}), unsafe.Sizeof(layout.AgentArena{}), uintptr(datasize.MB),
				)
				return 0
			},
			exhausted: true,
		},
		{
			name:  "failed attach sweeping a reclaimable agent",
			limit: 0,
			setup: func(t *testing.T, instance *zone.Instance) uintptr {
				oldest, _, held := supersededChain(t, instance, "chain", 2*datasize.MB)
				atomic.StoreUint64(&oldest.LoadedEntityCount, 0)
				return held
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			instance := newDataplane(t, tc.cpMemory).Instance()
			swept := tc.setup(t, instance)
			published := instance.Cp.CpConfigGen.Get()
			references := atomic.LoadUint64(&published.Refcnt)
			names := registryCount(instance)
			free := zoneFree(instance)

			_, err := agent.Attach(instance, "probe", uint64(tc.limit))

			if tc.exhausted {
				require.ErrorIs(t, err, shmalloc.ErrNoMemory)
			} else {
				require.Error(t, err)
				require.NotErrorIs(t, err, shmalloc.ErrNoMemory)
			}
			require.Nil(t, registered(instance, "probe"))
			require.Equal(t, names, registryCount(instance))
			require.Equal(t, free+swept, zoneFree(instance), "the zone's free bytes")
			require.Equal(t, references, atomic.LoadUint64(&published.Refcnt), "the generation pin is dropped")
		})
	}
}
