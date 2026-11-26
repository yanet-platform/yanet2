//go:build linux && (amd64 || arm64)

package cpconfig_test

import (
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

const (
	// objectSize is the size of a test object: its header and a raw body.
	objectSize = unsafe.Sizeof(layout.CpObject{}) + 64
	// bodySize is the size of the block a test object holds in its own
	// memory.
	bodySize = 512
	// restartingMemory is the memory limit of every attach of the agent
	// "restarting", small enough for the zone to hold several of them in
	// sanitizer builds.
	restartingMemory = 2 * datasize.MB
)

// attachRestarting attaches the agent "restarting", superseding any
// earlier one, as a restarted process does, and returns it.
func attachRestarting(t *testing.T, memory *publishedFixture) *agent.Agent {
	t.Helper()
	attached, err := agent.Attach(memory.Instance(), "restarting", uint64(restartingMemory))
	require.NoError(t, err)
	return attached
}

// supersededCount returns how many earlier agents of that name still hold
// memory.
func supersededCount(memory *publishedFixture, name string) int {
	for _, entry := range memory.Instance().Agents() {
		if entry.Name == name {
			return entry.Superseded
		}
	}
	return 0
}

// Test_TryDestroy_RefusesHeldConfiguration verifies that the free of a
// configuration the published generation holds is refused as still
// referenced, leaving its references and its agent as found.
func Test_TryDestroy_RefusesHeldConfiguration(t *testing.T) {
	cases := []struct {
		name string
		// held publishes a configuration and returns its entity and its
		// destroy gate.
		held func(t *testing.T, memory *publishedFixture) (*layout.CpEntity, func() error)
	}{
		{
			name: "published module",
			held: func(t *testing.T, memory *publishedFixture) (*layout.CpEntity, func() error) {
				module := publishedModule(t, memory.Instance().Cp.CpConfigGen.Get(), "a")
				return &module.Entity, func() error { return cpconfig.TryDestroyModule(module) }
			},
		},
		{
			name: "published object",
			held: func(t *testing.T, memory *publishedFixture) (*layout.CpEntity, func() error) {
				object := newObject(t, memory, memory.owner.Agent, "events", "hits")
				require.NoError(t, updateObjects(t, memory, object))
				return &object.Entity, func() error { return cpconfig.TryDestroyObject(object) }
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := newObjectsFixture(t, 0)
			owner := memory.owner.Agent
			entity, tryDestroy := tc.held(t, memory)
			references := atomic.LoadUint64(&entity.ConfigItem.Refcnt)
			loaded := atomic.LoadUint64(&owner.LoadedEntityCount)
			free := shmalloc.FreeSize(&owner.BlockAllocator)

			err := tryDestroy()

			require.ErrorIs(t, err, cpconfig.ErrStillReferenced)
			require.Equal(t, references, atomic.LoadUint64(&entity.ConfigItem.Refcnt), "the configuration's references")
			require.Equal(t, loaded, atomic.LoadUint64(&owner.LoadedEntityCount), "the agent's loaded count")
			require.Equal(t, free, shmalloc.FreeSize(&owner.BlockAllocator), "the agent's free bytes")
		})
	}
}

// Test_TryDestroyObject_TypedFreeGivesAgentBack verifies that an object's
// typed free composed from Go returns every byte its build took from its
// agent.
//
// The free passes the destroy gate, frees the body, finalises the header,
// returns the block and then the hold on the agent. An attach superseding
// the agent during the free leaves the agent to the free, whose release
// reclaims it.
func Test_TryDestroyObject_TypedFreeGivesAgentBack(t *testing.T) {
	cases := []struct {
		name string
		// supersede is whether a restart supersedes the agent mid-free.
		supersede bool
	}{
		{"object of a current agent", false},
		{"last object of an agent superseded during the free", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := newObjectsFixture(t, 0)
			owner := attachRestarting(t, memory).Agent
			free := shmalloc.FreeSize(&owner.BlockAllocator)
			object := newObject(t, memory, owner, "state", "hits")
			context := &object.Entity.MemoryContext
			body := shmalloc.ContextAlloc(context, bodySize)
			require.NotNil(t, body)

			require.NoError(t, cpconfig.TryDestroyObject(object))
			if tc.supersede {
				attachRestarting(t, memory)
			}
			shmalloc.ContextFree(context, body, bodySize)
			cpconfig.FiniObject(object)
			shmalloc.ContextFree(&owner.MemoryContext, unsafe.Pointer(object), objectSize)
			require.Equal(t, free, shmalloc.FreeSize(&owner.BlockAllocator), "the agent's free bytes")
			cpconfig.DestroyReleased(owner)

			require.Zero(t, supersededCount(memory, "restarting"))
		})
	}
}

// Test_TryDestroyObject_ReleaseThenFree verifies that an object only a
// pinned retired generation holds is refused until the pin is released,
// and then freed.
//
// An attach superseding the object's agent during the free leaves the
// agent to the free, whose release reclaims it.
func Test_TryDestroyObject_ReleaseThenFree(t *testing.T) {
	memory := newObjectsFixture(t, 0)
	instance := memory.Instance()
	object := newObject(t, memory, attachRestarting(t, memory).Agent, "state", "hits")
	require.NoError(t, updateObjects(t, memory, object))
	pinned, err := instance.Pin()
	require.NoError(t, err)
	require.NoError(t, updateObjects(t, memory, newObject(t, memory, memory.owner.Agent, "state", "hits")))
	require.ErrorIs(t, cpconfig.TryDestroyObject(object), cpconfig.ErrStillReferenced)
	owner := object.Entity.Agent.Get()

	pinned.Release()
	require.NoError(t, cpconfig.TryDestroyObject(object))
	attachRestarting(t, memory)
	cpconfig.FiniObject(object)
	shmalloc.ContextFree(&owner.MemoryContext, unsafe.Pointer(object), objectSize)
	cpconfig.DestroyReleased(owner)

	require.Zero(t, supersededCount(memory, "restarting"))
}
