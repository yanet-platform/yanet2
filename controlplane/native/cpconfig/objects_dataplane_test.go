//go:build linux && (amd64 || arm64)

package cpconfig_test

import (
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"unsafe"

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

// grownCounters are the counters of an object replacing one that counts
// "hits": a new name before the one it continues, so their slots move.
var grownCounters = []string{"misses", "hits"}

// objectRegistries returns an object's own counter registry and the one
// its module links spawn theirs from.
func objectRegistries(object *layout.CpObject) []*layout.CounterRegistry {
	return []*layout.CounterRegistry{&object.CounterRegistry, &object.LinkCounterRegistry}
}

// newObject builds a ring object of the test object size in the agent's
// memory, with two-value counters in both of its registries.
func newObject(t testing.TB, memory *publishedFixture, owner *layout.Agent, name string, counters ...string) *layout.CpObject {
	t.Helper()
	object := (*layout.CpObject)(shmalloc.ContextAlloc(&owner.MemoryContext, objectSize))
	require.NotNil(t, object)
	require.NoError(t, cpconfig.InitObject(object, &agent.Agent{Instance: memory.Instance(), Agent: owner}, "ring", name))
	for _, counter := range counters {
		for _, registry := range objectRegistries(object) {
			_, err := cpconfig.RegisterCounter(registry, counter, 2)
			require.NoError(t, err)
		}
	}
	return object
}

// publishObject builds a ring object of the fixture's agent counting
// "hits" and publishes it.
func publishObject(t testing.TB, memory *publishedFixture, name string) *layout.CpObject {
	t.Helper()
	object := newObject(t, memory, memory.owner.Agent, name, "hits")
	require.NoError(t, updateObjects(t, memory, object))
	return object
}

// updateObjects publishes the objects, delivering the contexts to the
// workers.
func updateObjects(t testing.TB, memory *publishedFixture, objects ...*layout.CpObject) error {
	t.Helper()
	return cpconfig.UpdateObjects(memory.Instance(), objects, memory.deliver())
}

// deleteObject deletes the ring object, delivering the contexts to the
// workers.
func deleteObject(t *testing.T, memory *publishedFixture, name string) error {
	t.Helper()
	return cpconfig.DeleteObject(memory.Instance(), "ring", name, memory.deliver())
}

// publishedObject returns the object of a name the published generation
// holds, or nil.
func publishedObject(memory *publishedFixture, name string) *layout.CpObject {
	gen := memory.Instance().Cp.CpConfigGen.Get()
	for _, item := range registry.Items(&gen.ObjectRegistry.EntityRegistry.Registry) {
		if object := (*layout.CpObject)(unsafe.Pointer(item)); shm.String(object.Name[:]) == name {
			return object
		}
	}
	return nil
}

// workerObjects returns the objects the contexts of each worker of the
// published generation hold.
func workerObjects(memory *publishedFixture) [][]*layout.CpObject {
	gen := memory.Instance().Cp.CpConfigGen.Get()
	var held [][]*layout.CpObject
	for root := range registry.Pointees(registry.Slice(gen.ConfigGenEctxs.Get(), gen.ConfigGenEctxCount)) {
		var objects []*layout.CpObject
		for object := range registry.Pointees(registry.Slice(root.Objects.Get(), root.ObjectCount)) {
			objects = append(objects, object.CpObject.Get())
		}
		held = append(held, objects)
	}
	return held
}

// freeObject frees a test object no generation holds, as a typed free
// does.
func freeObject(t testing.TB, object *layout.CpObject) {
	t.Helper()
	require.NoError(t, cpconfig.TryDestroyObject(object))
	owner := object.Entity.Agent.Get()
	cpconfig.FiniObject(object)
	shmalloc.ContextFree(&owner.MemoryContext, unsafe.Pointer(object), objectSize)
	cpconfig.DestroyReleased(owner)
}

// Test_UpdateObjects_ReplacesObjects verifies that objects built and
// published from Go replace those of their names in the generation and in
// every worker's contexts.
//
// A replacement continues the counters of the object it replaces, in its
// own storages and in those of the module links to it; the replaced
// object is released for its free, and a module linking it links its
// replacement.
func Test_UpdateObjects_ReplacesObjects(t *testing.T) {
	cases := []struct {
		name string
		// objects are the names of the objects the update publishes.
		objects []string
	}{
		{"fresh object", []string{"fresh"}},
		{"object replacing one that counts", []string{"state"}},
		{"object replacing one a module links", []string{"events"}},
		{"linked and fresh objects together", []string{"events", "fresh"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := newRichFixture(t)
			publishObject(t, memory, "state")
			countAll(memory)
			replaced := map[string]*layout.CpObject{}
			var objects []*layout.CpObject
			for _, name := range tc.objects {
				replaced[name] = publishedObject(memory, name)
				objects = append(objects, newObject(t, memory, memory.owner.Agent, name, grownCounters...))
			}
			linkTags := []zone.CounterTag{{Key: "object_name", Value: "events"}, {Key: "kind", Value: "module_object_link"}}
			var linkHits uint64
			if replaced["events"] != nil {
				linkHits = counterTotal(t, memory, "hits", linkTags...)
			}

			require.NoError(t, updateObjects(t, memory, objects...))

			for idx, name := range tc.objects {
				require.Same(t, objects[idx], publishedObject(memory, name))
				for _, held := range workerObjects(memory) {
					require.Contains(t, held, objects[idx], "every worker holds %q", name)
				}
				// Each of the two workers counted one hit before the update.
				hits := uint64(0)
				if replaced[name] != nil {
					hits = 2
					freeObject(t, replaced[name])
				}
				tags := []zone.CounterTag{{Key: "object_name", Value: name}, {Key: "kind", Value: "object"}}
				require.Equal(t, hits, counterTotal(t, memory, "hits", tags...), "%q continues the hits", name)
			}
			linked := linkedModuleContext(t, memory)
			link := &unsafe.Slice(linked.ObjectLinks.Get(), linked.ObjectLinkCount)[0]
			require.Same(t, publishedObject(memory, "events"), link.ObjectEctx.Get().CpObject.Get())
			if replaced["events"] != nil {
				require.NotZero(t, linkHits)
				require.Equal(t, linkHits, counterTotal(t, memory, "hits", linkTags...), "the module's link continues the hits")
			}
		})
	}
}

// Test_UpdateObjects_ReplacingLastObjectReclaimsSupersededAgent verifies
// that replacing the last published object of a superseded agent reclaims
// that agent in the same update.
func Test_UpdateObjects_ReplacingLastObjectReclaimsSupersededAgent(t *testing.T) {
	memory := newRichFixture(t)
	original := newObject(t, memory, attachRestarting(t, memory).Agent, "state", "hits")
	require.NoError(t, updateObjects(t, memory, original))
	successor := attachRestarting(t, memory).Agent
	require.Equal(t, 1, supersededCount(memory, "restarting"))

	require.NoError(t, updateObjects(t, memory, newObject(t, memory, successor, "state", grownCounters...)))

	require.Zero(t, supersededCount(memory, "restarting"))
}

// Test_UpdateObjects_RefusesSupersededOwner verifies that an update
// holding an object of a superseded agent is refused, publishing nothing
// and taking nothing.
//
// A current agent's object ahead of it gives back the hold its agent took.
func Test_UpdateObjects_RefusesSupersededOwner(t *testing.T) {
	cases := []struct {
		name string
		// current is whether an object of a current agent comes first.
		current bool
	}{
		{"object of a superseded agent", false},
		{"current object before one of a superseded agent", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := newRichFixture(t)
			current := attachRestarting(t, memory)
			supersedeOwner(t, memory)
			before := sharedState(memory)
			var objects []*layout.CpObject
			if tc.current {
				objects = append(objects, newObject(t, memory, current.Agent, "current", "hits"))
			}
			objects = append(objects, newObject(t, memory, memory.owner.Agent, "late", "hits"))

			err := updateObjects(t, memory, objects...)

			require.ErrorIs(t, err, cpconfig.ErrSupersededOwner)
			requireLeftAsFound(t, before, memory, objects...)
		})
	}
}

// Test_UpdateObjects_RefusesRepeatedObject verifies that an update naming
// one object twice is refused, publishing nothing and taking nothing.
func Test_UpdateObjects_RefusesRepeatedObject(t *testing.T) {
	memory := newRichFixture(t)
	before := sharedState(memory)
	first := newObject(t, memory, memory.owner.Agent, "twice")
	second := newObject(t, memory, memory.owner.Agent, "twice")

	err := updateObjects(t, memory, first, second)

	require.ErrorIs(t, err, cpconfig.ErrRepeated)
	requireLeftAsFound(t, before, memory, first, second)
}

// Test_UpdateObjects_ExhaustedZoneFreesUpdate verifies that an object
// update the zone runs out of memory for part-way frees every block it
// took from the zone and drops every reference it took.
//
// The object replaces one a module links, so the link contexts spawn from
// its link counters. The previous generation stays published and the
// exhaustion is reported; the budgets together must run out in the copy
// and in both workers, and a worker's tree of this fixture takes more
// than the shared budgets keep.
func Test_UpdateObjects_ExhaustedZoneFreesUpdate(t *testing.T) {
	budgets := append(slices.Clone(exhaustionBudgets), struct {
		name   string
		blocks []uintptr
	}{"two 256 KB blocks", []uintptr{256 * kilobyte, 256 * kilobyte}})
	failures := map[string]bool{}
	for _, budget := range budgets {
		t.Run(budget.name, func(t *testing.T) {
			memory := newRichFixture(t)
			nativetest.LeaveOnly(t, &memory.Instance().Cp.BlockAllocator, requestsFor(budget.blocks)...)
			before := sharedState(memory)
			object := newObject(t, memory, memory.owner.Agent, "events", grownCounters...)

			err := updateObjects(t, memory, object)

			require.ErrorIs(t, err, shmalloc.ErrNoMemory)
			requireLeftAsFound(t, before, memory, object)
			failures[failurePlace(t, err)] = true
		})
	}
	require.Equal(t, map[string]bool{"copy": true, "worker 0": true, "worker 1": true}, failures)
}

// Test_UpdateObjects_CounterLinkFailureFreesCopy verifies that an object
// whose counters cannot continue the replaced object's fails at the
// registry that cannot, publishing nothing and taking nothing.
//
// A counter redeclared at another size refuses the link, and an agent
// without memory fails the registry that must carry a name. The object
// keeps what its links grew until it is freed.
func Test_UpdateObjects_CounterLinkFailureFreesCopy(t *testing.T) {
	cases := []struct {
		name string
		// build returns the object replacing "state", which counts "hits"
		// in both registries.
		build func(t *testing.T, memory *publishedFixture) *layout.CpObject
		// exhausted marks an update its agent's memory runs out for: the
		// agent keeps no free block.
		exhausted bool
		// failing is the registry whose link fails.
		failing string
	}{
		{
			name: "own counter redeclared at another size",
			build: func(t *testing.T, memory *publishedFixture) *layout.CpObject {
				object := newObject(t, memory, memory.owner.Agent, "state")
				_, err := cpconfig.RegisterCounter(&object.CounterRegistry, "hits", 4)
				require.NoError(t, err)
				return object
			},
			failing: "link counter registry",
		},
		{
			name: "link counter redeclared at another size",
			build: func(t *testing.T, memory *publishedFixture) *layout.CpObject {
				object := newObject(t, memory, memory.owner.Agent, "state")
				_, err := cpconfig.RegisterCounter(&object.CounterRegistry, "hits", 2)
				require.NoError(t, err)
				_, err = cpconfig.RegisterCounter(&object.LinkCounterRegistry, "hits", 4)
				require.NoError(t, err)
				return object
			},
			failing: "link the link counter registry",
		},
		{
			name: "agent memory exhausted carrying an own counter",
			build: func(t *testing.T, memory *publishedFixture) *layout.CpObject {
				return newObject(t, memory, memory.owner.Agent, "state")
			},
			exhausted: true,
			failing:   "link counter registry",
		},
		{
			name: "agent memory exhausted carrying a link counter",
			build: func(t *testing.T, memory *publishedFixture) *layout.CpObject {
				object := newObject(t, memory, memory.owner.Agent, "state")
				_, err := cpconfig.RegisterCounter(&object.CounterRegistry, "hits", 2)
				require.NoError(t, err)
				return object
			},
			exhausted: true,
			failing:   "link the link counter registry",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := newRichFixture(t)
			publishObject(t, memory, "state")
			before := sharedState(memory)
			object := tc.build(t, memory)
			restore := func() {}
			if tc.exhausted {
				restore = nativetest.LeaveOnly(t, &memory.owner.Agent.BlockAllocator)
			}

			err := updateObjects(t, memory, object)
			restore()

			require.ErrorContains(t, err, "upsert object ring:state: "+tc.failing+": ")
			require.Equal(t, tc.exhausted, errors.Is(err, shmalloc.ErrNoMemory))
			requireLeftAsFound(t, before, memory, object)
		})
	}
}

// Test_LinkObject_IndexesLinks verifies that a module linking objects
// appends each new object to its links and returns its index, and that
// an update publishes it with the links resolved.
//
// Linking an object a second time returns its first index.
func Test_LinkObject_IndexesLinks(t *testing.T) {
	memory := newRichFixture(t)
	publishObject(t, memory, "state")
	module := newBlackhole(t, memory, "linking")
	var indexes []uint64

	for _, name := range []string{"state", "events", "state"} {
		index, err := cpconfig.LinkObject(module, "ring", name)
		require.NoError(t, err)
		indexes = append(indexes, index)
	}
	require.NoError(t, updateModules(t, memory, module))

	require.Equal(t, []uint64{0, 1, 0}, indexes)
	var names []string
	for _, link := range registry.Slice(module.Objects.Get(), module.ObjectCount) {
		names = append(names, shm.String(link.Type[:])+":"+shm.String(link.Name[:]))
	}
	require.Equal(t, []string{"ring:state", "ring:events"}, names)
	require.Same(t, module, publishedModule(t, memory.Instance().Cp.CpConfigGen.Get(), "linking"))
}

// Test_LinkObject_AgentMemoryFailureKeepsLinks verifies that a link the
// agent has no memory for fails, leaving the module's links and the
// agent's memory as they were.
func Test_LinkObject_AgentMemoryFailureKeepsLinks(t *testing.T) {
	memory := newPublishedFixture(t)
	module, err := cpconfig.NewBlackhole(memory.owner, "linking")
	require.NoError(t, err)
	_, err = cpconfig.LinkObject(module, "ring", "state")
	require.NoError(t, err)
	links := module.Objects.Get()
	nativetest.LeaveOnly(t, &memory.owner.Agent.BlockAllocator)
	free := shmalloc.FreeSize(&memory.owner.Agent.BlockAllocator)

	_, err = cpconfig.LinkObject(module, "ring", "other")

	require.ErrorIs(t, err, shmalloc.ErrNoMemory)
	require.Same(t, links, module.Objects.Get(), "the module keeps its links")
	require.Equal(t, uint64(1), module.ObjectCount)
	require.Equal(t, "state", shm.String(links.Name[:]))
	require.Equal(t, free, shmalloc.FreeSize(&memory.owner.Agent.BlockAllocator), "the agent's free bytes")
}

// Test_InitObject_UnknownTypeLeavesAgentAsFound verifies that an object
// of a type the dataplane does not load is refused, linking no memory
// context to the agent's and taking none of its memory.
func Test_InitObject_UnknownTypeLeavesAgentAsFound(t *testing.T) {
	memory := newPublishedFixture(t)
	owner := memory.owner.Agent
	object := (*layout.CpObject)(shmalloc.ContextAlloc(&owner.MemoryContext, objectSize))
	require.NotNil(t, object)
	firstChild := owner.MemoryContext.FirstChild.Get()
	free := shmalloc.FreeSize(&owner.BlockAllocator)

	err := cpconfig.InitObject(object, memory.owner, "ring", "state")

	require.ErrorContains(t, err, `object type "ring" not found`)
	require.Same(t, firstChild, owner.MemoryContext.FirstChild.Get(), "the agent's memory contexts")
	require.Equal(t, free, shmalloc.FreeSize(&owner.BlockAllocator), "the agent's free bytes")
}

// Test_DeleteObject_RemovesObject verifies that deleting a published
// object empties its registry slot, drops its owner's loaded count and
// rebuilds every worker's contexts without it.
func Test_DeleteObject_RemovesObject(t *testing.T) {
	memory := newRichFixture(t)
	object := publishObject(t, memory, "state")
	loaded := atomic.LoadUint64(&memory.owner.Agent.LoadedEntityCount)

	require.NoError(t, deleteObject(t, memory, "state"))

	require.Nil(t, publishedObject(memory, "state"))
	require.Equal(t, loaded-1, atomic.LoadUint64(&memory.owner.Agent.LoadedEntityCount))
	for _, held := range workerObjects(memory) {
		require.NotContains(t, held, object)
		require.Contains(t, held, publishedObject(memory, "events"))
	}
}

// Test_DeleteObject_RefusesAbsentOrLinkedObject verifies that deleting
// an object the published generation does not hold, or one a published
// module links, is refused, publishing nothing and taking nothing.
func Test_DeleteObject_RefusesAbsentOrLinkedObject(t *testing.T) {
	cases := []struct {
		name string
		// object is the name of the object the delete names.
		object string
		want   error
	}{
		{"object not published", "absent", cpconfig.ErrNotFound},
		{"object a published module links", "events", cpconfig.ErrObjectLinked},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := newRichFixture(t)
			before := sharedState(memory)

			err := deleteObject(t, memory, tc.object)

			require.ErrorIs(t, err, tc.want)
			requireLeftAsFound[*layout.CpObject](t, before, memory)
		})
	}
}

// Test_DeleteObject_FreeRepublishAndDeleteAgain verifies that an object
// deleted, freed, published again under its name and deleted again
// leaves nothing behind.
//
// Once both are freed the agent's memory and loaded count are back where
// they were, and a third delete finds no object.
func Test_DeleteObject_FreeRepublishAndDeleteAgain(t *testing.T) {
	memory := newRichFixture(t)
	owner := memory.owner.Agent
	free := shmalloc.FreeSize(&owner.BlockAllocator)
	loaded := atomic.LoadUint64(&owner.LoadedEntityCount)

	original := publishObject(t, memory, "state")
	require.NoError(t, deleteObject(t, memory, "state"))
	freeObject(t, original)
	replacement := newObject(t, memory, owner, "state", grownCounters...)
	require.NoError(t, updateObjects(t, memory, replacement))
	require.NoError(t, deleteObject(t, memory, "state"))
	freeObject(t, replacement)

	require.Equal(t, free, shmalloc.FreeSize(&owner.BlockAllocator), "the agent's free bytes")
	require.Equal(t, loaded, atomic.LoadUint64(&owner.LoadedEntityCount), "the agent's loaded count")
	require.ErrorIs(t, deleteObject(t, memory, "state"), cpconfig.ErrNotFound)
}
