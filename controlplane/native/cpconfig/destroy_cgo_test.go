//go:build linux && (amd64 || arm64) && cgo

package cpconfig_test

import (
	"testing"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/coracle"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
	"github.com/yanet-platform/yanet2/objects/ring/bindings/go/cring"
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

// attachRestarting attaches through C the agent "restarting", superseding
// any earlier one, as a restarted process does, and returns it.
func attachRestarting(t *testing.T, memory *publishedFixture) *layout.Agent {
	t.Helper()
	attached, err := memory.Harness.SharedMemory().AgentAttach("restarting", 0, restartingMemory)
	require.NoError(t, err)
	return (*layout.Agent)(attached.AsRawPtr())
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
// referenced and leaves the storage as found.
func Test_TryDestroy_RefusesHeldConfiguration(t *testing.T) {
	cases := []struct {
		name string
		// held publishes a configuration and returns its destroy gate.
		held func(t *testing.T, memory *publishedFixture) func() error
	}{
		{
			name: "published module",
			held: func(t *testing.T, memory *publishedFixture) func() error {
				module := publishedModule(t, memory.Instance().Cp.CpConfigGen.Get(), "a")
				return func() error { return cpconfig.TryDestroyModule(module) }
			},
		},
		{
			name: "published object",
			held: func(t *testing.T, memory *publishedFixture) func() error {
				ring, err := cring.NewObject(memory.cAgent, "events", 64, cring.DefaultPublishBatch)
				require.NoError(t, err)
				require.NoError(t, ring.Publish())
				object := (*layout.CpObject)(ring.AsRawPtr())
				return func() error { return cpconfig.TryDestroyObject(object) }
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := newObjectsFixture(t, 0)
			tryDestroy := tc.held(t, memory)
			before, _ := coracle.Snapshot(memory.Image)

			err := tryDestroy()

			require.ErrorIs(t, err, cpconfig.ErrStillReferenced)
			after, _ := coracle.Snapshot(memory.Image)
			nativetest.RequireSameBytes(t, before, after)
		})
	}
}

// Test_TryDestroyObject_TypedFreeMatchesC verifies that an object's typed
// free composed from Go leaves the storage as the same free in C does.
//
// The free passes the destroy gate, frees the body, finalises the header,
// returns the block and then the hold on the agent. An attach superseding
// the agent during the free leaves the agent to the free, whose release
// reclaims it.
func Test_TryDestroyObject_TypedFreeMatchesC(t *testing.T) {
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
			raw, err := coracle.ObjectNew(unsafe.Pointer(attachRestarting(t, memory)), "ring", "state", objectSize)
			require.NoError(t, err)
			object := (*layout.CpObject)(raw)
			for _, counters := range objectRegistries(object) {
				require.NoError(t, coracle.RegisterCounter(unsafe.Pointer(counters), "hits", 2))
			}
			context := &object.Entity.MemoryContext
			body := coracle.ContextAlloc(unsafe.Pointer(context), bodySize)
			require.NotNil(t, body)
			restart := func() {
				if tc.supersede {
					attachRestarting(t, memory)
				}
			}

			memory.CompareWithC(
				t,
				func() {
					require.NoError(t, coracle.ObjectTryDestroy(raw))
					owner := object.Entity.Agent.Get()
					restart()
					coracle.ContextFree(unsafe.Pointer(context), body, bodySize)
					coracle.ObjectDestroy(raw, objectSize)
					coracle.AgentDestroyReleased(unsafe.Pointer(owner))
				},
				func() {
					require.NoError(t, cpconfig.TryDestroyObject(object))
					owner := object.Entity.Agent.Get()
					restart()
					shmalloc.ContextFree(context, body, bodySize)
					cpconfig.FiniObject(object)
					shmalloc.ContextFree(&owner.MemoryContext, raw, objectSize)
					cpconfig.DestroyReleased(owner)
				},
			)
			require.Zero(t, supersededCount(memory, "restarting"))
		})
	}
}

// Test_TryDestroyObject_ReleaseThenFreeMatchesC verifies that an object
// only a pinned retired generation holds is refused until the pin is
// released, and then freed as C frees it.
//
// An attach superseding the object's agent during the free leaves the
// agent to the free, whose release reclaims it.
func Test_TryDestroyObject_ReleaseThenFreeMatchesC(t *testing.T) {
	memory := newObjectsFixture(t, 0)
	instance := memory.Instance()
	object := goNewObject(t, memory, attachRestarting(t, memory), "state", "hits")
	require.NoError(t, updateObjects(t, memory, object))
	pinned, err := instance.Pin()
	require.NoError(t, err)
	require.NoError(t, updateObjects(t, memory, goNewObject(t, memory, memory.owner.Agent, "state", "hits")))
	require.ErrorIs(t, cpconfig.TryDestroyObject(object), cpconfig.ErrStillReferenced)
	owner := object.Entity.Agent.Get()

	memory.CompareWithC(
		t,
		func() {
			coracle.GenRelease(unsafe.Pointer(instance.Cp), unsafe.Pointer(pinned.Gen))
			require.NoError(t, coracle.ObjectTryDestroy(unsafe.Pointer(object)))
			attachRestarting(t, memory)
			coracle.ObjectDestroy(unsafe.Pointer(object), objectSize)
			coracle.AgentDestroyReleased(unsafe.Pointer(owner))
		},
		func() {
			pinned.Release()
			require.NoError(t, cpconfig.TryDestroyObject(object))
			attachRestarting(t, memory)
			cpconfig.FiniObject(object)
			shmalloc.ContextFree(&owner.MemoryContext, unsafe.Pointer(object), objectSize)
			cpconfig.DestroyReleased(owner)
		},
	)
	require.Zero(t, supersededCount(memory, "restarting"))
}
