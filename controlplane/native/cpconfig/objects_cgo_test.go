//go:build linux && (amd64 || arm64) && cgo

package cpconfig_test

import (
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/coracle"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
	"github.com/yanet-platform/yanet2/modules/blackhole/bindings/go/cblackhole"
)

// grownCounters are the counters of an object replacing one that counts
// "hits": a new name before the one it continues, so their slots move.
var grownCounters = []string{"misses", "hits"}

// objectRegistries returns an object's own counter registry and the one
// its module links spawn theirs from.
func objectRegistries(object *layout.CpObject) []*layout.CounterRegistry {
	return []*layout.CounterRegistry{&object.CounterRegistry, &object.LinkCounterRegistry}
}

// cNewObject builds through C a ring object of the test object size in
// the agent's memory, with two-value counters in both of its registries.
func cNewObject(t testing.TB, owner *layout.Agent, name string, counters ...string) *layout.CpObject {
	t.Helper()
	raw, err := coracle.ObjectNew(unsafe.Pointer(owner), "ring", name, objectSize)
	require.NoError(t, err)
	object := (*layout.CpObject)(raw)
	for _, counter := range counters {
		for _, registry := range objectRegistries(object) {
			require.NoError(t, coracle.RegisterCounter(unsafe.Pointer(registry), counter, 2))
		}
	}
	return object
}

// goNewObject builds natively a ring object of the test object size in
// the agent's memory, with two-value counters in both of its registries.
func goNewObject(t testing.TB, memory *publishedFixture, owner *layout.Agent, name string, counters ...string) *layout.CpObject {
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

// cUpdateObjects publishes the objects through the cgo control plane.
func cUpdateObjects(memory *publishedFixture, objects ...*layout.CpObject) error {
	configs := make([]ffi.ObjectConfig, len(objects))
	for idx, object := range objects {
		configs[idx] = ffi.NewObjectConfig(unsafe.Pointer(object))
	}
	return memory.cAgent.UpdateObjects(configs)
}

// cPublishObject builds through C a ring object of the fixture's agent
// counting "hits" and publishes it through C.
func cPublishObject(t testing.TB, memory *publishedFixture, name string) *layout.CpObject {
	t.Helper()
	object := cNewObject(t, memory.owner.Agent, name, "hits")
	require.NoError(t, cUpdateObjects(memory, object))
	return object
}

// updateObjects publishes the objects natively, delivering the contexts
// to the in-process workers, within the publication bound.
func updateObjects(t *testing.T, memory *publishedFixture, objects ...*layout.CpObject) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- cpconfig.UpdateObjects(memory.Instance(), objects, cpconfig.WithDelivery(nativetest.Deliver))
	}()
	return awaitPublication(t, memory, done)
}

// deleteObject deletes the ring object natively, delivering the contexts
// to the in-process workers, within the publication bound.
func deleteObject(t *testing.T, memory *publishedFixture, name string) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- cpconfig.DeleteObject(memory.Instance(), "ring", name, cpconfig.WithDelivery(nativetest.Deliver))
	}()
	return awaitPublication(t, memory, done)
}

// freeObject frees natively a test object no generation holds, as a
// typed free does.
func freeObject(t *testing.T, object *layout.CpObject) {
	t.Helper()
	require.NoError(t, cpconfig.TryDestroyObject(object))
	owner := object.Entity.Agent.Get()
	cpconfig.FiniObject(object)
	shmalloc.ContextFree(&owner.MemoryContext, unsafe.Pointer(object), objectSize)
	cpconfig.DestroyReleased(owner)
}

// Test_UpdateObjects_MatchesC verifies that objects built and published
// from Go leave the storage as C's object init and update do.
//
// The update covers the objects' headers and counters, their registry
// slots, counters continued from a replaced object, the link contexts of
// a module linking a replaced object and every worker's contexts.
func Test_UpdateObjects_MatchesC(t *testing.T) {
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
			cPublishObject(t, memory, "state")

			memory.CompareWithC(
				t,
				func() {
					var objects []*layout.CpObject
					for _, name := range tc.objects {
						objects = append(objects, cNewObject(t, memory.owner.Agent, name, grownCounters...))
					}
					require.NoError(t, cUpdateObjects(memory, objects...))
				},
				func() {
					var objects []*layout.CpObject
					for _, name := range tc.objects {
						objects = append(objects, goNewObject(t, memory, memory.owner.Agent, name, grownCounters...))
					}
					require.NoError(t, updateObjects(t, memory, objects...))
				},
			)
		})
	}
}

// Test_UpdateObjects_ReplacingLastObjectReclaimsSupersededAgentMatchesC
// verifies that replacing the last published object of a superseded
// agent reclaims that agent in the same update, as C does.
func Test_UpdateObjects_ReplacingLastObjectReclaimsSupersededAgentMatchesC(t *testing.T) {
	memory := newRichFixture(t)
	original := cNewObject(t, attachRestarting(t, memory), "state", "hits")
	require.NoError(t, cUpdateObjects(memory, original))
	successor := attachRestarting(t, memory)
	require.Equal(t, 1, supersededCount(memory, "restarting"))

	memory.CompareWithC(
		t,
		func() {
			require.NoError(t, cUpdateObjects(memory, cNewObject(t, successor, "state", grownCounters...)))
			require.Zero(t, supersededCount(memory, "restarting"))
		},
		func() {
			require.NoError(t, updateObjects(t, memory, goNewObject(t, memory, successor, "state", grownCounters...)))
			require.Zero(t, supersededCount(memory, "restarting"))
		},
	)
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
			stale := cNewObject(t, memory.owner.Agent, "late", "hits")
			var objects []*layout.CpObject
			if tc.current {
				objects = append(objects, goNewObject(t, memory, attachRestarting(t, memory), "current", "hits"))
			}
			objects = append(objects, stale)
			// The published configurations keep the superseded agent
			// alive.
			_, err := memory.Harness.SharedMemory().AgentAttach("owner", 0, 2*datasize.MB)
			require.NoError(t, err)
			before := sharedState(memory, objects...)

			err = updateObjects(t, memory, objects...)

			require.ErrorIs(t, err, cpconfig.ErrSupersededOwner)
			requireLeftAsFound(t, before, memory, objects...)
		})
	}
}

// Test_UpdateObjects_RefusesRepeatedObject verifies that an update naming
// one object twice is refused, publishing nothing and taking nothing.
func Test_UpdateObjects_RefusesRepeatedObject(t *testing.T) {
	memory := newRichFixture(t)
	first := goNewObject(t, memory, memory.owner.Agent, "twice")
	second := goNewObject(t, memory, memory.owner.Agent, "twice")
	before := sharedState(memory, first, second)

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
			instance := memory.Instance()
			object := goNewObject(t, memory, memory.owner.Agent, "events", grownCounters...)
			nativetest.LeaveOnly(t, &instance.Cp.BlockAllocator, requestsFor(budget.blocks)...)
			before := sharedState(memory, object)

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
// keeps what its links grew until it is destroyed.
func Test_UpdateObjects_CounterLinkFailureFreesCopy(t *testing.T) {
	cases := []struct {
		name string
		// build returns the object replacing "state", which counts "hits"
		// in both registries.
		build func(t *testing.T, memory *publishedFixture) *layout.CpObject
		// exhausted marks an update its agent's memory runs out for.
		exhausted bool
		// failing is the registry whose link fails.
		failing string
	}{
		{
			name: "own counter redeclared at another size",
			build: func(t *testing.T, memory *publishedFixture) *layout.CpObject {
				object := goNewObject(t, memory, memory.owner.Agent, "state")
				_, err := cpconfig.RegisterCounter(&object.CounterRegistry, "hits", 4)
				require.NoError(t, err)
				return object
			},
			failing: "link counter registry",
		},
		{
			name: "link counter redeclared at another size",
			build: func(t *testing.T, memory *publishedFixture) *layout.CpObject {
				object := goNewObject(t, memory, memory.owner.Agent, "state")
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
				object := goNewObject(t, memory, memory.owner.Agent, "state")
				nativetest.LeaveOnly(t, &memory.owner.Agent.BlockAllocator)
				return object
			},
			exhausted: true,
			failing:   "link counter registry",
		},
		{
			name: "agent memory exhausted carrying a link counter",
			build: func(t *testing.T, memory *publishedFixture) *layout.CpObject {
				object := goNewObject(t, memory, memory.owner.Agent, "state")
				_, err := cpconfig.RegisterCounter(&object.CounterRegistry, "hits", 2)
				require.NoError(t, err)
				nativetest.LeaveOnly(t, &memory.owner.Agent.BlockAllocator)
				return object
			},
			exhausted: true,
			failing:   "link the link counter registry",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := newRichFixture(t)
			cPublishObject(t, memory, "state")
			object := tc.build(t, memory)
			before := sharedState(memory, object)

			err := updateObjects(t, memory, object)

			require.ErrorContains(t, err, "upsert object ring:state: "+tc.failing+": ")
			require.Equal(t, tc.exhausted, errors.Is(err, shmalloc.ErrNoMemory))
			requireLeftAsFound(t, before, memory, object)
		})
	}
}

// Test_LinkObject_MatchesC verifies that a module linking objects, built
// and published from Go, leaves the storage and the link indexes as C's
// link and module update do.
//
// Linking an object a second time returns its first index.
func Test_LinkObject_MatchesC(t *testing.T) {
	memory := newRichFixture(t)
	cPublishObject(t, memory, "state")
	links := []string{"state", "events", "state"}
	var cIndexes, goIndexes []uint64

	memory.CompareWithC(
		t,
		func() {
			config, err := cblackhole.NewModuleConfig(memory.cAgent, "linking")
			require.NoError(t, err)
			for _, name := range links {
				index, err := coracle.ModuleLinkObject(config.AsFFIModule().AsRawPtr(), "ring", name)
				require.NoError(t, err)
				cIndexes = append(cIndexes, index)
			}
			require.NoError(t, memory.cAgent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}))
		},
		func() {
			module, err := cpconfig.NewBlackhole(memory.owner, "linking")
			require.NoError(t, err)
			for _, name := range links {
				index, err := cpconfig.LinkObject(module, "ring", name)
				require.NoError(t, err)
				goIndexes = append(goIndexes, index)
			}
			require.NoError(t, updateModules(t, memory, module))
		},
	)
	require.Equal(t, []uint64{0, 1, 0}, cIndexes)
	require.Equal(t, cIndexes, goIndexes)
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

// Test_DeleteObject_MatchesC verifies that deleting a published object
// from Go leaves the storage as cp_config_delete_object does.
//
// Its registry slot is emptied, its owner's loaded count dropped and every
// worker's contexts rebuilt without it.
func Test_DeleteObject_MatchesC(t *testing.T) {
	memory := newRichFixture(t)
	cPublishObject(t, memory, "state")

	memory.CompareWithC(
		t,
		func() { require.NoError(t, memory.cAgent.DeleteObject("ring", "state")) },
		func() { require.NoError(t, deleteObject(t, memory, "state")) },
	)
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
			before := sharedState[*layout.CpObject](memory)

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

	original := cPublishObject(t, memory, "state")
	require.NoError(t, deleteObject(t, memory, "state"))
	freeObject(t, original)
	replacement := goNewObject(t, memory, owner, "state", grownCounters...)
	require.NoError(t, updateObjects(t, memory, replacement))
	require.NoError(t, deleteObject(t, memory, "state"))
	freeObject(t, replacement)

	require.Equal(t, free, shmalloc.FreeSize(&owner.BlockAllocator), "the agent's free bytes")
	require.Equal(t, loaded, atomic.LoadUint64(&owner.LoadedEntityCount), "the agent's loaded count")
	require.ErrorIs(t, deleteObject(t, memory, "state"), cpconfig.ErrNotFound)
}
