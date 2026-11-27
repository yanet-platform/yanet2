//go:build linux && (amd64 || arm64)

package zone_test

import (
	"slices"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// Test_Instance_Pin_RefusesGenerationOutsideInstance verifies that a
// published generation lying outside its instance is refused before its
// reference count is touched.
func Test_Instance_Pin_RefusesGenerationOutsideInstance(t *testing.T) {
	data, dp := craftedStorage(unsafe.Sizeof(layout.CpConfigGen{}))
	outside := (*layout.CpConfigGen)(unsafe.Pointer(&data[dp.StorageSize]))
	dp.CpConfig.Get().CpConfigGen.Set(outside)
	storage, err := zone.FromMemory(data)
	require.NoError(t, err)

	_, err = storage.Instances[0].Pin()
	require.ErrorContains(t, err, "published generation lies outside the instance")
	require.Zero(t, outside.Refcnt)
}

// Test_Generation_Release_LeavesUnpinnedGenerationAlone verifies that
// releasing a generation no pin produced keeps its references.
func Test_Generation_Release_LeavesUnpinnedGenerationAlone(t *testing.T) {
	gen := &layout.CpConfigGen{Refcnt: 1}
	generation := zone.Generation{Gen: gen}

	generation.Release()

	require.Equal(t, uint64(1), atomic.LoadUint64(&gen.Refcnt))
	require.Nil(t, generation.Gen)
}

// newGenerationDataplane returns a dataplane of two workers that loads
// the blackhole module.
func newGenerationDataplane(t *testing.T) *nativetest.Dataplane {
	t.Helper()
	return nativetest.NewDataplane(t, nativetest.Config{Workers: 2, Modules: []string{"blackhole"}})
}

// superseded returns how many earlier agents of the name still hold
// memory.
func superseded(t *testing.T, instance *zone.Instance, name string) int {
	t.Helper()
	for _, entry := range instance.Agents() {
		if entry.Name == name {
			return entry.Superseded
		}
	}
	require.Fail(t, "no agent of that name", name)
	return 0
}

// restartAndRepublish publishes a blackhole configuration from an agent,
// attaches the agent again, as a restart does, and publishes the
// configuration anew; when asked to pin, it returns a pin taken before
// the restart, which holds the first generation, and nil otherwise.
func restartAndRepublish(t *testing.T, dataplane *nativetest.Dataplane, pin bool) *zone.Generation {
	t.Helper()
	instance := dataplane.Instance()
	publish := func() {
		owner, err := agent.Attach(instance, "restarting", uint64(2*datasize.MB))
		require.NoError(t, err)
		module, err := cpconfig.NewBlackhole(owner, "held")
		require.NoError(t, err)
		require.NoError(t, cpconfig.UpdateModules(instance, []*layout.CpModule{module}, cpconfig.WithDelivery(dataplane.Deliver)))
	}
	publish()
	var pinned *zone.Generation
	if pin {
		var err error
		pinned, err = instance.Pin()
		require.NoError(t, err)
	}
	publish()
	return pinned
}

// zoneFree returns the bytes the free lists of the instance's
// control-plane zone hold.
func zoneFree(instance *zone.Instance) uintptr {
	return shmalloc.FreeSize(&instance.Cp.BlockAllocator)
}

// Test_Generation_Release_FreesGenerationRetiredWhilePinned verifies that
// releasing the last pin of a retired generation frees it and reclaims
// the superseded agent only that generation held.
//
// The zone then holds the free bytes it holds after the same
// publications made without a pin, which free both at once.
func Test_Generation_Release_FreesGenerationRetiredWhilePinned(t *testing.T) {
	dataplane := newGenerationDataplane(t)
	instance := dataplane.Instance()
	unpinned := newGenerationDataplane(t)
	require.Nil(t, restartAndRepublish(t, unpinned, false))
	pinned := restartAndRepublish(t, dataplane, true)
	retired := pinned.Gen
	require.NotSame(t, retired, instance.Cp.CpConfigGen.Get())
	require.Equal(t, uint64(1), atomic.LoadUint64(&retired.Refcnt))
	require.Equal(t, 1, superseded(t, instance, "restarting"))

	pinned.Release()

	require.Nil(t, pinned.Gen)
	require.Zero(t, superseded(t, instance, "restarting"), "the release reclaims the agent only the retired generation held")
	require.Equal(t, zoneFree(unpinned.Instance()), zoneFree(instance), "the zone's free bytes")
}

// publishDropPipeline publishes a function whose chain runs a blackhole
// configuration and a pipeline of it, replacing both if published.
func publishDropPipeline(t *testing.T, dataplane *nativetest.Dataplane) {
	t.Helper()
	deliver := cpconfig.WithDelivery(dataplane.Deliver)
	require.NoError(t, cpconfig.UpdateFunctions(dataplane.Instance(), []cpconfig.FunctionConfig{{
		Name: "drop",
		Chains: []cpconfig.FunctionChainConfig{{
			Weight: 1,
			Chain: cpconfig.ChainConfig{
				Name:    "drop_chain",
				Modules: []cpconfig.ChainModuleConfig{{Type: "blackhole", Name: "held"}},
			},
		}},
	}}, deliver))
	require.NoError(t, cpconfig.UpdatePipelines(dataplane.Instance(), []cpconfig.PipelineConfig{
		{Name: "drop", Functions: []string{"drop"}},
	}, deliver))
}

// republishPipeline publishes a blackhole configuration from an agent and
// the function and pipeline running it, then publishes them again, and
// returns the agent; when asked to pin, it also returns a pin taken in
// between, which holds the generation that published the first ones.
func republishPipeline(t *testing.T, dataplane *nativetest.Dataplane, pin bool) (*zone.Generation, *agent.Agent) {
	t.Helper()
	instance := dataplane.Instance()
	owner, err := agent.Attach(instance, "owner", uint64(2*datasize.MB))
	require.NoError(t, err)
	module, err := cpconfig.NewBlackhole(owner, "held")
	require.NoError(t, err)
	require.NoError(t, cpconfig.UpdateModules(instance, []*layout.CpModule{module}, cpconfig.WithDelivery(dataplane.Deliver)))
	publishDropPipeline(t, dataplane)
	var pinned *zone.Generation
	if pin {
		pinned, err = instance.Pin()
		require.NoError(t, err)
	}
	publishDropPipeline(t, dataplane)
	return pinned, owner
}

// Test_Generation_Release_FreesFunctionsAndPipelinesRetiredWhilePinned
// verifies that a last release frees the function and pipeline it held.
//
// Updates replace the function and the pipeline while a pin holds the
// generation that published them, so the pin keeps their last reference.
// The zone and the agent then hold the free bytes they hold after the
// same updates made without a pin.
func Test_Generation_Release_FreesFunctionsAndPipelinesRetiredWhilePinned(t *testing.T) {
	unpinned := newGenerationDataplane(t)
	_, unpinnedOwner := republishPipeline(t, unpinned, false)
	dataplane := newGenerationDataplane(t)
	pinned, owner := republishPipeline(t, dataplane, true)
	require.Equal(t, uint64(1), atomic.LoadUint64(&pinned.Gen.Refcnt))

	pinned.Release()

	require.Equal(t, zoneFree(unpinned.Instance()), zoneFree(dataplane.Instance()), "the zone's free bytes")
	require.Equal(
		t,
		shmalloc.FreeSize(&unpinnedOwner.Agent.BlockAllocator),
		shmalloc.FreeSize(&owner.Agent.BlockAllocator),
		"the agent's free bytes",
	)
}

// Test_Generation_Release_DropsThePin verifies that a pin holds one more
// reference on the published generation, that Release drops it, and that
// a second Release drops nothing more.
func Test_Generation_Release_DropsThePin(t *testing.T) {
	native := newPublished(t).Instance
	published := native.Cp.CpConfigGen.Load()
	require.NotNil(t, published)
	before := atomic.LoadUint64(&published.Refcnt)

	pinned, err := native.Pin()
	require.NoError(t, err)
	require.Same(t, published, pinned.Gen)
	require.Equal(t, before+1, atomic.LoadUint64(&published.Refcnt))

	pinned.Release()
	require.Nil(t, pinned.Gen)
	require.Equal(t, before, atomic.LoadUint64(&published.Refcnt))

	pinned.Release()
	require.Equal(t, before, atomic.LoadUint64(&published.Refcnt))
}

// ringSize is the size of a test ring object: its header and a raw body.
const ringSize = unsafe.Sizeof(layout.CpObject{}) + 64

// publishRing builds a ring object of the topology's agent and publishes
// it, replacing the one of its name.
func publishRing(t *testing.T, memory *published, name string) *layout.CpObject {
	t.Helper()
	object := (*layout.CpObject)(shmalloc.ContextAlloc(&memory.Owner.Agent.MemoryContext, ringSize))
	require.NotNil(t, object)
	require.NoError(t, cpconfig.InitObject(object, memory.Owner, "ring", name))
	require.NoError(t, cpconfig.UpdateObjects(memory.Instance, []*layout.CpObject{object}, memory.Deliver()))
	return object
}

// pinLookupTopology publishes the topology with ring objects "first" and
// "gone", deletes "gone" and the module "second", and pins the generation.
func pinLookupTopology(t *testing.T) *zone.Generation {
	t.Helper()
	memory := newPublished(t)
	publishRing(t, memory, "first")
	publishRing(t, memory, "gone")
	require.NoError(t, cpconfig.DeleteObject(memory.Instance, "ring", "gone", memory.Deliver()))
	require.NoError(t, cpconfig.DeleteModule(memory.Instance, "blackhole", "second", memory.Deliver()))
	generation, err := memory.Instance.Pin()
	require.NoError(t, err)
	t.Cleanup(generation.Release)
	return generation
}

// lookupCase is a type and name looked up in a pinned generation and
// whether it is found.
type lookupCase struct {
	name       string
	entityType string
	entityName string
	found      bool
}

// Test_Generation_LookupModule_FindsPublishedModuleByTypeAndName verifies
// that a module configuration is found only under its published type and name.
func Test_Generation_LookupModule_FindsPublishedModuleByTypeAndName(t *testing.T) {
	generation := pinLookupTopology(t)
	cases := []lookupCase{
		{"published module", "blackhole", "first", true},
		{"unpublished name", "blackhole", "third", false},
		{"deleted module", "blackhole", "second", false},
		{"name of an object of another type", "ring", "first", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			module := generation.LookupModule(tc.entityType, tc.entityName)
			if !tc.found {
				require.Nil(t, module)
				return
			}
			require.NotNil(t, module)
			require.Equal(t, tc.entityType, shm.String(module.Type[:]))
			require.Equal(t, tc.entityName, shm.String(module.Name[:]))
		})
	}
}

// Test_Generation_LookupObject_FindsPublishedObjectByTypeAndName verifies
// that an object is found only under its published type and name.
func Test_Generation_LookupObject_FindsPublishedObjectByTypeAndName(t *testing.T) {
	generation := pinLookupTopology(t)
	cases := []lookupCase{
		{"published object", "ring", "first", true},
		{"unpublished name", "ring", "third", false},
		{"deleted object", "ring", "gone", false},
		{"name of a module of another type", "blackhole", "first", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			object := generation.LookupObject(tc.entityType, tc.entityName)
			if !tc.found {
				require.Nil(t, object)
				return
			}
			require.NotNil(t, object)
			require.Equal(t, tc.entityType, shm.String(object.Type[:]))
			require.Equal(t, tc.entityName, shm.String(object.Name[:]))
		})
	}
}

// Test_Generation_Lookup_ReadsThePinnedGeneration verifies that lookups
// read the generation a pin holds, not a later one.
//
// A pin taken before a module and an object are republished still finds
// the replaced ones, and a pin taken after finds their replacements.
func Test_Generation_Lookup_ReadsThePinnedGeneration(t *testing.T) {
	memory := newPublished(t)
	oldObject := publishRing(t, memory, "first")
	pinned, err := memory.Instance.Pin()
	require.NoError(t, err)
	defer pinned.Release()
	oldModule := pinned.LookupModule("blackhole", "first")
	require.NotNil(t, oldModule)

	newModule, err := cpconfig.NewBlackhole(memory.Owner, "first")
	require.NoError(t, err)
	require.NoError(t, cpconfig.UpdateModules(memory.Instance, []*layout.CpModule{newModule}, memory.Deliver()))
	newObject := publishRing(t, memory, "first")
	fresh, err := memory.Instance.Pin()
	require.NoError(t, err)
	defer fresh.Release()

	require.Same(t, oldModule, pinned.LookupModule("blackhole", "first"))
	require.Same(t, oldObject, pinned.LookupObject("ring", "first"))
	require.Same(t, newModule, fresh.LookupModule("blackhole", "first"))
	require.Same(t, newObject, fresh.LookupObject("ring", "first"))
}

// Test_Generation_Lookup_ReleasedGenerationFindsNothing verifies that a
// released pin no longer reads the generation it held.
func Test_Generation_Lookup_ReleasedGenerationFindsNothing(t *testing.T) {
	memory := newPublished(t)
	publishRing(t, memory, "first")
	generation, err := memory.Instance.Pin()
	require.NoError(t, err)

	generation.Release()

	require.Nil(t, generation.LookupModule("blackhole", "first"))
	require.Nil(t, generation.LookupObject("ring", "first"))
}

// publishTwoChainTopology republishes the topology so its device runs one
// function of two chains on its input and its output.
//
// The function "drop" has chain "a" running blackhole configurations first
// and second and chain "b" running second; pipeline "dummy" runs it.
func publishTwoChainTopology(t *testing.T, memory *published) {
	t.Helper()
	blackholes := func(names ...string) []cpconfig.ChainModuleConfig {
		var modules []cpconfig.ChainModuleConfig
		for _, name := range names {
			modules = append(modules, cpconfig.ChainModuleConfig{Type: "blackhole", Name: name})
		}
		return modules
	}
	require.NoError(t, cpconfig.UpdateFunctions(memory.Instance, []cpconfig.FunctionConfig{{
		Name: "drop",
		Chains: []cpconfig.FunctionChainConfig{
			{Weight: 1, Chain: cpconfig.ChainConfig{Name: "a", Modules: blackholes("first", "second")}},
			{Weight: 1, Chain: cpconfig.ChainConfig{Name: "b", Modules: blackholes("second")}},
		},
	}}, memory.Deliver()))
	require.NoError(t, cpconfig.UpdatePipelines(memory.Instance, []cpconfig.PipelineConfig{
		{Name: "dummy", Functions: []string{"drop"}},
	}, memory.Deliver()))
}

// Test_Generation_ModulePositions_WalksTopology verifies that a module
// type's positions are yielded in topology order and only for that type.
//
// The order is the device's input and then output pipelines, their
// functions, chains and chain modules; other types and released pins yield
// none.
func Test_Generation_ModulePositions_WalksTopology(t *testing.T) {
	memory := newPublished(t)
	publishTwoChainTopology(t, memory)
	generation, err := memory.Instance.Pin()
	require.NoError(t, err)
	defer generation.Release()
	position := func(pipeline, chain, module string) zone.ModulePosition {
		return zone.ModulePosition{
			Device:     "port0",
			Pipeline:   pipeline,
			Function:   "drop",
			Chain:      chain,
			ModuleType: "blackhole",
			ModuleName: module,
		}
	}

	cases := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"every position in walk order", func(t *testing.T) {
			require.Equal(t, []zone.ModulePosition{
				position("drop", "a", "first"),
				position("drop", "a", "second"),
				position("drop", "b", "second"),
				position("dummy", "a", "first"),
				position("dummy", "a", "second"),
				position("dummy", "b", "second"),
			}, slices.Collect(generation.ModulePositions("blackhole")))
		}},
		{"type no chain runs", func(t *testing.T) {
			require.Empty(t, slices.Collect(generation.ModulePositions("ring")))
		}},
		{"walk stopped by its consumer", func(t *testing.T) {
			var seen []zone.ModulePosition
			for found := range generation.ModulePositions("blackhole") {
				seen = append(seen, found)
				break
			}
			require.Equal(t, []zone.ModulePosition{position("drop", "a", "first")}, seen)
		}},
		{"released pin", func(t *testing.T) {
			released, err := memory.Instance.Pin()
			require.NoError(t, err)
			released.Release()
			require.Empty(t, slices.Collect(released.ModulePositions("blackhole")))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.check)
	}
}
