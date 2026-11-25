//go:build linux && (amd64 || arm64) && cgo

package cpconfig_test

import (
	"bytes"
	"iter"
	"regexp"
	"strconv"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/coracle"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
	cplain "github.com/yanet-platform/yanet2/devices/plain/bindings/go/cplain"
	"github.com/yanet-platform/yanet2/modules/blackhole/bindings/go/cblackhole"
	"github.com/yanet-platform/yanet2/objects/ring/bindings/go/cring"
)

// publishedFixture is a two-worker dataplane whose generation the cgo
// control plane filled through the agent "owner".
//
// It publishes blackhole configurations a, b and c, a function chaining
// a, pipelines "drop" and "dummy" and the device port0 running them.
type publishedFixture struct {
	*nativetest.Fixture
	cAgent *ffi.Agent
	owner  *agent.Agent
}

// newPublishedFixture returns a published fixture freed when the test
// ends.
func newPublishedFixture(t testing.TB) *publishedFixture {
	t.Helper()
	memory := nativetest.NewFixture(t, dataplaneut.Config{
		WorkerCount:   2,
		Devices:       []string{"port0"},
		Modules:       []string{"blackhole"},
		DevicesToLoad: []string{"plain"},
	})
	cAgent, owner := attachOwner(t, memory, "owner")
	var modules []ffi.ModuleConfig
	for _, name := range []string{"a", "b", "c"} {
		config, err := cblackhole.NewModuleConfig(cAgent, name)
		require.NoError(t, err)
		modules = append(modules, config.AsFFIModule())
	}
	require.NoError(t, cAgent.UpdateModules(modules))
	require.NoError(t, cAgent.UpdateFunction(ffi.FunctionConfig{
		Name: "drop",
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name:    "drop_chain",
				Modules: []ffi.ChainModuleConfig{{Type: "blackhole", Name: "a"}},
			},
		}},
	}))
	require.NoError(t, cAgent.UpdatePipeline(ffi.PipelineConfig{Name: "drop", Functions: []string{"drop"}}))
	require.NoError(t, cAgent.UpdatePipeline(ffi.PipelineConfig{Name: "dummy"}))
	_, err := cplain.UpdateDevices(cAgent, []ffi.DeviceConfig{{
		Name:   "port0",
		Input:  []ffi.DevicePipelineConfig{{Name: "drop", Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}})
	require.NoError(t, err)
	return &publishedFixture{Fixture: memory, cAgent: cAgent, owner: owner}
}

// newRichFixture returns a two-worker dataplane whose generation the cgo
// control plane filled with every kind of context node.
//
// Its agent "owner" published them: blackhole a is plain; blackhole linked
// links the ring object "events", counts into a runtime registry "rules"
// and names the devices port1 and virt. The function drop runs chain heavy
// (a, then linked) at weight 3 and chain light (a) at weight 1. The device
// port0 takes input through drop at weight 2 and dummy at weight 1 and has
// no output; port1 and virt run drop and dummy. Only virt has no port.
func newRichFixture(t testing.TB) *publishedFixture {
	t.Helper()
	memory := nativetest.NewFixture(t, dataplaneut.Config{
		WorkerCount:   2,
		Devices:       []string{"port0", "port1"},
		Modules:       []string{"blackhole"},
		DevicesToLoad: []string{"plain"},
		// Ring loads second, so its type resolves to a nonzero index.
		ObjectsToLoad: []string{"fwstate_map_v4", "ring"},
	})
	cAgent, owner := attachOwner(t, memory, "owner")
	ring, err := cring.NewObject(cAgent, "events", 64, cring.DefaultPublishBatch)
	require.NoError(t, err)
	require.NoError(t, ring.Publish())

	plain, err := cblackhole.NewModuleConfig(cAgent, "a")
	require.NoError(t, err)
	linked, err := cblackhole.NewModuleConfig(cAgent, "linked")
	require.NoError(t, err)
	linkedModule := linked.AsFFIModule().AsRawPtr()
	_, err = coracle.ModuleLinkObject(linkedModule, "ring", "events")
	require.NoError(t, err)
	require.NoError(t, coracle.ModuleRegisterCounter(linkedModule, "rules", "hits", 4))
	for _, device := range []string{"port1", "virt"} {
		_, err = coracle.ModuleLinkDevice(linkedModule, device)
		require.NoError(t, err)
	}
	require.NoError(t, cAgent.UpdateModules([]ffi.ModuleConfig{plain.AsFFIModule(), linked.AsFFIModule()}))

	require.NoError(t, cAgent.UpdateFunction(ffi.FunctionConfig{
		Name: "drop",
		Chains: []ffi.FunctionChainConfig{
			{Weight: 3, Chain: ffi.ChainConfig{Name: "heavy", Modules: []ffi.ChainModuleConfig{
				{Type: "blackhole", Name: "a"}, {Type: "blackhole", Name: "linked"},
			}}},
			{Weight: 1, Chain: ffi.ChainConfig{Name: "light", Modules: []ffi.ChainModuleConfig{
				{Type: "blackhole", Name: "a"},
			}}},
		},
	}))
	require.NoError(t, cAgent.UpdatePipeline(ffi.PipelineConfig{Name: "drop", Functions: []string{"drop"}}))
	require.NoError(t, cAgent.UpdatePipeline(ffi.PipelineConfig{Name: "dummy"}))
	_, err = cplain.UpdateDevices(cAgent, []ffi.DeviceConfig{
		{
			Name:  "port0",
			Input: []ffi.DevicePipelineConfig{{Name: "drop", Weight: 2}, {Name: "dummy", Weight: 1}},
		},
		{
			Name:   "port1",
			Input:  []ffi.DevicePipelineConfig{{Name: "drop", Weight: 1}},
			Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
		},
		{
			Name:   "virt",
			Input:  []ffi.DevicePipelineConfig{{Name: "drop", Weight: 1}},
			Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
		},
	})
	require.NoError(t, err)
	return &publishedFixture{Fixture: memory, cAgent: cAgent, owner: owner}
}

// fixtures names the published dataplanes updates are compared on.
var fixtures = []struct {
	name    string
	newFunc func(t testing.TB) *publishedFixture
}{
	{"one device", newPublishedFixture},
	{"objects, runtime counters, linked devices and weights", newRichFixture},
}

// newWeightedFixture returns a one-worker dataplane whose device port0
// takes input through pipeline drop at weight 40.
//
// Its entry context is four times the size of an entry without weights.
// The pipeline runs function drop, whose one chain runs blackhole a; the
// agent "owner" published them through the cgo control plane.
func newWeightedFixture(t testing.TB) *publishedFixture {
	t.Helper()
	memory := nativetest.NewFixture(t, dataplaneut.Config{
		WorkerCount:   1,
		Devices:       []string{"port0"},
		Modules:       []string{"blackhole"},
		DevicesToLoad: []string{"plain"},
	})
	cAgent, owner := attachOwner(t, memory, "owner")
	config, err := cblackhole.NewModuleConfig(cAgent, "a")
	require.NoError(t, err)
	require.NoError(t, cAgent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}))
	require.NoError(t, cAgent.UpdateFunction(ffi.FunctionConfig{
		Name: "drop",
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain:  ffi.ChainConfig{Name: "drop_chain", Modules: []ffi.ChainModuleConfig{{Type: "blackhole", Name: "a"}}},
		}},
	}))
	require.NoError(t, cAgent.UpdatePipeline(ffi.PipelineConfig{Name: "drop", Functions: []string{"drop"}}))
	_, err = cplain.UpdateDevices(cAgent, []ffi.DeviceConfig{{
		Name:  "port0",
		Input: []ffi.DevicePipelineConfig{{Name: "drop", Weight: 40}},
	}})
	require.NoError(t, err)
	return &publishedFixture{Fixture: memory, cAgent: cAgent, owner: owner}
}

// newObjectsFixture returns a one-worker dataplane publishing that many
// ring objects, blackhole a and the device port0 without pipelines.
//
// The agent "owner" published them through the cgo control plane.
func newObjectsFixture(t testing.TB, count int) *publishedFixture {
	t.Helper()
	memory := nativetest.NewFixture(t, dataplaneut.Config{
		WorkerCount:   1,
		Devices:       []string{"port0"},
		Modules:       []string{"blackhole"},
		DevicesToLoad: []string{"plain"},
		ObjectsToLoad: []string{"ring"},
	})
	cAgent, owner := attachOwner(t, memory, "owner")
	for idx := range count {
		ring, err := cring.NewObject(cAgent, "events"+strconv.Itoa(idx), 64, cring.DefaultPublishBatch)
		require.NoError(t, err)
		require.NoError(t, ring.Publish())
	}
	config, err := cblackhole.NewModuleConfig(cAgent, "a")
	require.NoError(t, err)
	require.NoError(t, cAgent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}))
	_, err = cplain.UpdateDevices(cAgent, []ffi.DeviceConfig{{Name: "port0"}})
	require.NoError(t, err)
	return &publishedFixture{Fixture: memory, cAgent: cAgent, owner: owner}
}

// shared is what an update takes from or counts on that outlives it.
//
// It is the free bytes of the zone, those of every agent once the
// update's configurations are destroyed, the published generation with
// its header, items and worker roots, and every reference count that
// generation and those configurations reach.
type shared struct {
	ZoneFree  uintptr
	AgentFree map[*layout.Agent]uintptr
	Published *layout.CpConfigGen
	Header    []byte
	Items     [][]*layout.RegistryItem
	Roots     []*layout.ConfigGenEctx
	Counts    map[*uint64]uint64
}

// configuration is what an update publishes: module configurations or
// objects.
type configuration interface {
	*layout.CpModule | *layout.CpObject
}

// partsOf returns a configuration's entity, its counter registries and
// its free through C.
//
// Every test object is of the test object size.
func partsOf[C configuration](config C) (*layout.CpEntity, []*layout.CounterRegistry, func()) {
	switch config := any(config).(type) {
	case *layout.CpModule:
		return &config.Entity, counterRegistries(config), func() { coracle.ModuleDestroy(unsafe.Pointer(config)) }
	case *layout.CpObject:
		return &config.Entity, objectRegistries(config), func() { coracle.ObjectDestroy(unsafe.Pointer(config), objectSize) }
	}
	panic("a configuration is a module or an object")
}

// sharedState reads the shared state of the fixture's instance and of
// the configurations an update publishes.
func sharedState[C configuration](memory *publishedFixture, configs ...C) shared {
	cp := memory.Instance().Cp
	gen := cp.CpConfigGen.Get()
	state := shared{
		ZoneFree:  shmalloc.FreeSize(&cp.BlockAllocator),
		AgentFree: agentFreeWithout(memory, configs...),
		Published: gen,
		Header:    bytes.Clone(bytesOf(gen)),
		Counts:    map[*uint64]uint64{},
	}
	count := func(counter *uint64) { state.Counts[counter] = atomic.LoadUint64(counter) }
	for agent := range agentsOf(cp) {
		count(&agent.LoadedEntityCount)
	}
	for _, config := range configs {
		entity, _, _ := partsOf(config)
		count(&entity.ConfigItem.Refcnt)
	}
	registries := []*layout.Registry{
		&gen.ModuleRegistry.EntityRegistry.Registry,
		&gen.FunctionRegistry.Registry,
		&gen.PipelineRegistry.Registry,
		&gen.DeviceRegistry.EntityRegistry.Registry,
		&gen.ObjectRegistry.EntityRegistry.Registry,
	}
	for _, items := range registries {
		var held []*layout.RegistryItem
		for idx := range items.Capacity {
			item := registry.Item(items, idx)
			held = append(held, item)
			if item != nil {
				count(&item.Refcnt)
			}
		}
		state.Items = append(state.Items, held)
	}
	roots := registry.Slice(gen.ConfigGenEctxs.Get(), gen.ConfigGenEctxCount)
	for worker := range roots {
		root := roots[worker].Get()
		state.Roots = append(state.Roots, root)
		storages := root.CounterStorageRegistry.Get()
		items := registry.Slice(storages.Items.Get(), storages.Count)
		for idx := range items {
			storage := items[idx].Storage.Get()
			count(&storage.Refcnt)
			for pool := range storage.Pools {
				blocks := registry.Slice(storage.Pools[pool].Blocks.Get(), storage.Pools[pool].BlockCount)
				for block := range blocks {
					count(&blocks[block].Get().Refcnt)
				}
			}
		}
	}
	return state
}

// agentsOf yields every agent of the instance, superseded ones included.
func agentsOf(cp *layout.CpConfig) iter.Seq[*layout.Agent] {
	return func(yield func(*layout.Agent) bool) {
		agents := cp.AgentRegistry.Get()
		slots := agents.Agents(int(agents.Count))
		for idx := range slots {
			for agent := slots[idx].Get(); agent != nil; agent = agent.Prev.Get() {
				if !yield(agent) {
					return
				}
			}
		}
	}
}

// agentFreeWithout returns the free bytes of every agent once the
// configurations are destroyed, and leaves the agents as they were.
//
// A configuration keeps what its links grew on its agent, so destroying
// it after a failed update returns as much as destroying it before. The
// destruction writes only to the agents and their arenas, which are kept
// and put back instead of the whole storage.
func agentFreeWithout[C configuration](memory *publishedFixture, configs ...C) map[*layout.Agent]uintptr {
	var regions [][]byte
	for agent := range agentsOf(memory.Instance().Cp) {
		regions = append(regions, bytesOf(agent))
		arenas := registry.Slice(agent.Arenas.Get(), agent.ArenaCount)
		for idx := range arenas {
			regions = append(regions, unsafe.Slice((*byte)(arenas[idx].Data.Get()), arenas[idx].Size))
		}
	}
	images := make([][]byte, len(regions))
	runs := make([][]coracle.Run, len(regions))
	for idx, region := range regions {
		images[idx], runs[idx] = coracle.Snapshot(region)
	}
	defer func() {
		for idx, region := range regions {
			coracle.Restore(region, images[idx], runs[idx])
		}
	}()
	for _, config := range configs {
		_, _, destroy := partsOf(config)
		destroy()
	}
	free := map[*layout.Agent]uintptr{}
	for agent := range agentsOf(memory.Instance().Cp) {
		free[agent] = shmalloc.FreeSize(&agent.BlockAllocator)
	}
	return free
}

// publishedValues yields every counter the storages of a generation's
// workers show, with its values.
func publishedValues(gen *layout.CpConfigGen) iter.Seq2[*layout.Counter, []uint64] {
	return func(yield func(*layout.Counter, []uint64) bool) {
		roots := registry.Slice(gen.ConfigGenEctxs.Get(), gen.ConfigGenEctxCount)
		for worker := range roots {
			storages := roots[worker].Get().CounterStorageRegistry.Get()
			items := registry.Slice(storages.Items.Get(), storages.Count)
			for idx := range items {
				storage := items[idx].Storage.Get()
				counters := storage.Registry.Get()
				handles := registry.Slice(storage.CounterValueHandles.Get(), counters.Count)
				names := registry.Slice(counters.Names.Get(), counters.Count)
				for slot := range names {
					values := unsafe.Slice((*uint64)(unsafe.Pointer(handles[slot].Get())), names[slot].Size)
					if !yield(&names[slot], values) {
						return
					}
				}
			}
		}
	}
}

// countAll counts one into every counter value the published
// generation's storages show.
func countAll(memory *publishedFixture) {
	for _, values := range publishedValues(memory.Instance().Cp.CpConfigGen.Get()) {
		for idx := range values {
			values[idx]++
		}
	}
}

// counterRegistries returns a module's own counter registry and its
// tagged runtime ones.
func counterRegistries(module *layout.CpModule) []*layout.CounterRegistry {
	all := []*layout.CounterRegistry{&module.CounterRegistry}
	entries := registry.Slice(module.RuntimeCounterRegistries.Get(), module.RuntimeCounterRegistryCount)
	for idx := range entries {
		all = append(all, &entries[idx].Get().Registry)
	}
	return all
}

// bytesOf views the bytes of a value in shared memory.
func bytesOf[T any](value *T) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(value)), unsafe.Sizeof(*value))
}

// requireIndexed fails unless a counter registry's index holds its names
// and nothing else, each found at its own place.
func requireIndexed(t testing.TB, counters *layout.CounterRegistry) {
	t.Helper()
	held := 0
	for _, value := range registry.Slice(counters.StrIndex.Values.Get(), uint64(counters.StrIndex.Capacity)) {
		if value != registry.Invalid {
			held++
		}
	}
	require.Equal(t, int(counters.Count), held, "the index holds the registry's names only")
	names := registry.Slice(counters.Names.Get(), counters.Capacity)
	read := func(value uint32) []byte { return shm.Bytes(names[value].Name[:]) }
	for idx := range counters.Count {
		name := shm.String(names[idx].Name[:])
		found := registry.StrLookup(&counters.StrIndex, name, len(names[idx].Name), read)
		require.Equal(t, uint32(idx), found, "counter %q is indexed at its place", name)
	}
}

// requireLeftAsFound fails unless a failed update left the shared state as it
// found it and the counter registries of its configurations usable.
//
// The configurations stay linked, and destroying them returns their
// agents' memory as it did before the update.
func requireLeftAsFound[C configuration](t testing.TB, before shared, memory *publishedFixture, configs ...C) {
	t.Helper()
	after := sharedState(memory, configs...)
	for _, config := range configs {
		_, registries, _ := partsOf(config)
		for _, counters := range registries {
			requireIndexed(t, counters)
		}
	}
	require.Same(t, before.Published, after.Published, "the previous generation stays published")
	require.Equal(t, before.Header, after.Header, "the published generation's header")
	require.Equal(t, before.Items, after.Items, "the published generation's items")
	require.Equal(t, before.Roots, after.Roots, "the published generation's worker roots")
	require.Equal(t, before.ZoneFree, after.ZoneFree, "the zone's free bytes")
	require.Equal(t, before.AgentFree, after.AgentFree, "the agents' free bytes once the configurations are destroyed")
	require.Equal(t, before.Counts, after.Counts, "the reference and loaded-entity counts")
}

// publishedModule returns the blackhole configuration of that name the
// generation publishes.
func publishedModule(t testing.TB, gen *layout.CpConfigGen, name string) *layout.CpModule {
	t.Helper()
	registry := &gen.ModuleRegistry.EntityRegistry.Registry
	slots := unsafe.Slice(registry.Items.Get(), registry.Capacity)
	for idx := range slots {
		item := slots[idx].Get()
		if item == nil {
			continue
		}
		// An empty slot links to another slot of the array.
		start := uintptr(unsafe.Pointer(&slots[0]))
		end := uintptr(unsafe.Pointer(&slots[len(slots)-1])) + unsafe.Sizeof(slots[0])
		if address := uintptr(unsafe.Pointer(item)); address >= start && address < end {
			continue
		}
		if module := (*layout.CpModule)(unsafe.Pointer(item)); shm.String(module.Name[:]) == name {
			return module
		}
	}
	t.Fatalf("generation %d publishes no module %q", gen.Gen, name)
	return nil
}

// copiedRegistries names a generation's registries in the order an
// update copies them.
var copiedRegistries = []string{"module", "function", "pipeline", "device", "object"}

// copyRequests returns the requests an update's copy of the generation
// makes, in its order.
//
// The generation comes first, then each registry's slots and index.
func copyRequests(gen *layout.CpConfigGen) []uintptr {
	registries := []*layout.Registry{
		&gen.ModuleRegistry.EntityRegistry.Registry,
		&gen.FunctionRegistry.Registry,
		&gen.PipelineRegistry.Registry,
		&gen.DeviceRegistry.EntityRegistry.Registry,
		&gen.ObjectRegistry.EntityRegistry.Registry,
	}
	requests := []uintptr{unsafe.Sizeof(layout.CpConfigGen{})}
	for _, registry := range registries {
		requests = append(
			requests,
			uintptr(registry.Capacity)*shm.PtrSize,
			uintptr(registry.Capacity)*4*unsafe.Sizeof(layout.HashIndexEntry{}),
		)
	}
	return requests
}

// requestsFor returns the requests whose kept blocks are of the sizes.
func requestsFor(blocks []uintptr) []uintptr {
	requests := make([]uintptr, len(blocks))
	for idx, size := range blocks {
		requests[idx] = nativetest.BlockRequest(size)
	}
	return requests
}

// kilobyte is the unit the exhaustion budgets are sized in.
const kilobyte = uintptr(datasize.KB)

// exhaustionBudgets are the free blocks the zone keeps in the failure
// tests, smallest first.
//
// Their failures move from the start of an update towards its end.
var exhaustionBudgets = []struct {
	name   string
	blocks []uintptr
}{
	{"one 512 B block", []uintptr{kilobyte / 2}},
	{"one 4 KB block", []uintptr{4 * kilobyte}},
	{"one 32 KB block", []uintptr{32 * kilobyte}},
	{"one 64 KB block", []uintptr{64 * kilobyte}},
	{"one 64 KB and one 4 KB block", []uintptr{64 * kilobyte, 4 * kilobyte}},
	{"two 32 KB blocks and one 2 KB block", []uintptr{32 * kilobyte, 32 * kilobyte, 2 * kilobyte}},
	{"two 32 KB blocks and one 4 KB block", []uintptr{32 * kilobyte, 32 * kilobyte, 4 * kilobyte}},
	{"two 32 KB blocks and one 8 KB block", []uintptr{32 * kilobyte, 32 * kilobyte, 8 * kilobyte}},
}

// workerPlace matches the worker a context build stopped in.
var workerPlace = regexp.MustCompile(`worker \d+`)

// failurePlace returns where a failed update stopped: the worker whose
// contexts it was building, or the copy.
func failurePlace(t *testing.T, err error) string {
	t.Helper()
	if place := workerPlace.FindString(err.Error()); place != "" {
		return place
	}
	require.Regexp(t, `allocate generation|copy \w+ registry`, err.Error())
	return "copy"
}
