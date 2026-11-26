//go:build linux && (amd64 || arm64)

package cpconfig_test

import (
	"bytes"
	"iter"
	"regexp"
	"slices"
	"strconv"
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
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest/helperproto"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// publishedFixture is a dataplane to which the agent "owner" published
// a topology.
type publishedFixture struct {
	*nativetest.Dataplane
	owner *agent.Agent
}

// newFixture returns a dataplane of the configuration with the agent
// "owner" attached and nothing published.
func newFixture(t testing.TB, config nativetest.Config) *publishedFixture {
	t.Helper()
	dataplane := nativetest.NewDataplane(t, config)
	owner, err := agent.Attach(dataplane.Instance(), "owner", uint64(16*datasize.MB))
	require.NoError(t, err)
	return &publishedFixture{Dataplane: dataplane, owner: owner}
}

// deliver returns the option that hands a publication to the dataplane's
// workers.
func (m *publishedFixture) deliver() cpconfig.UpdateOption {
	return cpconfig.WithDelivery(m.Deliver)
}

// newPublishedFixture returns a two-worker dataplane to which the agent
// "owner" published blackhole configurations a, b and c, a function
// chaining a, pipelines "drop" and "dummy" and the device port0 running
// them.
func newPublishedFixture(t testing.TB) *publishedFixture {
	t.Helper()
	memory := newFixture(t, nativetest.Config{
		Workers:     2,
		Devices:     []string{"port0"},
		Modules:     []string{"blackhole"},
		DeviceTypes: []string{"plain"},
	})
	modules := []*layout.CpModule{newBlackhole(t, memory, "a"), newBlackhole(t, memory, "b"), newBlackhole(t, memory, "c")}
	require.NoError(t, updateModules(t, memory, modules...))
	require.NoError(t, updateFunctions(t, memory, cpconfig.FunctionConfig{
		Name:   "drop",
		Chains: []cpconfig.FunctionChainConfig{chainOf("drop_chain", 1, "a")},
	}))
	require.NoError(t, updatePipelines(t, memory,
		cpconfig.PipelineConfig{Name: "drop", Functions: []string{"drop"}},
		cpconfig.PipelineConfig{Name: "dummy"},
	))
	require.NoError(t, updateDevices(t, memory, newPlainDevice(t, memory.owner, "port0", 1)))
	return memory
}

// newRichFixture returns a two-worker dataplane to which the agent
// "owner" published every kind of context node.
//
// Blackhole a is plain; blackhole linked links the ring object "events",
// counts into a runtime registry "rules" and names the devices port1 and
// virt. The function drop runs chain heavy (a, then linked) at weight 3
// and chain light (a) at weight 1. The device port0 takes input through
// drop at weight 2 and dummy at weight 1 and has no output; port1 and
// virt run drop and dummy. Only virt has no port.
func newRichFixture(t testing.TB) *publishedFixture {
	t.Helper()
	memory := newFixture(t, nativetest.Config{
		Workers:     2,
		Devices:     []string{"port0", "port1"},
		Modules:     []string{"blackhole"},
		DeviceTypes: []string{"plain"},
		// Ring loads second, so its type resolves to a nonzero index.
		ObjectTypes: []string{"fwstate_map_v4", "ring"},
	})
	require.NoError(t, updateObjects(t, memory, newObject(t, memory, memory.owner.Agent, "events", "hits")))

	plain := newBlackhole(t, memory, "a")
	linked := newBlackhole(t, memory, "linked")
	_, err := cpconfig.LinkObject(linked, "ring", "events")
	require.NoError(t, err)
	registerRuntimeCounter(t, linked)
	for _, device := range []string{"port1", "virt"} {
		_, err = cpconfig.LinkDevice(linked, device)
		require.NoError(t, err)
	}
	require.NoError(t, updateModules(t, memory, plain, linked))

	require.NoError(t, updateFunctions(t, memory, cpconfig.FunctionConfig{
		Name:   "drop",
		Chains: []cpconfig.FunctionChainConfig{chainOf("heavy", 3, "a", "linked"), chainOf("light", 1, "a")},
	}))
	require.NoError(t, updatePipelines(t, memory,
		cpconfig.PipelineConfig{Name: "drop", Functions: []string{"drop"}},
		cpconfig.PipelineConfig{Name: "dummy"},
	))
	port0, err := cpconfig.NewPlainDevice(memory.owner, cpconfig.DeviceConfig{
		Name:  "port0",
		Input: []cpconfig.DevicePipelineConfig{{Name: "drop", Weight: 2}, {Name: "dummy", Weight: 1}},
	})
	require.NoError(t, err)
	port1 := newPlainDevice(t, memory.owner, "port1", 1)
	virt := newPlainDevice(t, memory.owner, "virt", 1)
	require.NoError(t, updateDevices(t, memory, port0, port1, virt))
	return memory
}

// registerRuntimeCounter registers the counter "hits" of four values in
// the module's runtime registry "rules".
func registerRuntimeCounter(t testing.TB, module *layout.CpModule) {
	t.Helper()
	rules, _, err := cpconfig.ModuleCounterRegistry(module, "rules")
	require.NoError(t, err)
	_, err = cpconfig.RegisterCounter(rules, "hits", 4)
	require.NoError(t, err)
}

// fixtures names the published dataplanes updates are checked on.
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
// agent "owner" published them.
func newWeightedFixture(t testing.TB) *publishedFixture {
	t.Helper()
	memory := newFixture(t, nativetest.Config{
		Devices:     []string{"port0"},
		Modules:     []string{"blackhole"},
		DeviceTypes: []string{"plain"},
	})
	require.NoError(t, updateModules(t, memory, newBlackhole(t, memory, "a")))
	require.NoError(t, updateFunctions(t, memory, cpconfig.FunctionConfig{
		Name:   "drop",
		Chains: []cpconfig.FunctionChainConfig{chainOf("drop_chain", 1, "a")},
	}))
	require.NoError(t, updatePipelines(t, memory, cpconfig.PipelineConfig{Name: "drop", Functions: []string{"drop"}}))
	device, err := cpconfig.NewPlainDevice(memory.owner, cpconfig.DeviceConfig{
		Name:  "port0",
		Input: []cpconfig.DevicePipelineConfig{{Name: "drop", Weight: 40}},
	})
	require.NoError(t, err)
	require.NoError(t, updateDevices(t, memory, device))
	return memory
}

// newObjectsFixture returns a one-worker dataplane publishing that many
// ring objects, blackhole a and the device port0 without pipelines.
//
// The agent "owner" published them.
func newObjectsFixture(t testing.TB, count int) *publishedFixture {
	t.Helper()
	memory := newFixture(t, nativetest.Config{
		Devices:     []string{"port0"},
		Modules:     []string{"blackhole"},
		DeviceTypes: []string{"plain"},
		ObjectTypes: []string{"ring"},
	})
	for idx := range count {
		require.NoError(t, updateObjects(t, memory, newObject(t, memory, memory.owner.Agent, "events"+strconv.Itoa(idx))))
	}
	require.NoError(t, updateModules(t, memory, newBlackhole(t, memory, "a")))
	device, err := cpconfig.NewPlainDevice(memory.owner, cpconfig.DeviceConfig{Name: "port0"})
	require.NoError(t, err)
	require.NoError(t, updateDevices(t, memory, device))
	return memory
}

// shared is what an update takes from or counts on that outlives it.
//
// It is the free bytes of the zone and of every agent, the zone's
// AddressSanitizer marks, the published generation with its header,
// items and worker roots, and every reference count that generation
// reaches.
type shared struct {
	ZoneFree  uintptr
	AgentFree map[*layout.Agent]uintptr
	Marks     []helperproto.Range
	Published *layout.CpConfigGen
	Header    []byte
	Items     [][]*layout.RegistryItem
	Roots     []*layout.ConfigGenEctx
	Counts    map[*uint64]uint64
}

// configuration is what an update publishes: module configurations,
// objects or devices.
type configuration interface {
	*layout.CpModule | *layout.CpObject | *layout.CpDevice
}

// partsOf returns a configuration's entity, its counter registries and
// its typed free.
func partsOf[C configuration](t testing.TB, config C) (*layout.CpEntity, []*layout.CounterRegistry, func()) {
	switch config := any(config).(type) {
	case *layout.CpModule:
		return &config.Entity, counterRegistries(config), func() { require.NoError(t, cpconfig.FreeBlackhole(config)) }
	case *layout.CpObject:
		return &config.Entity, objectRegistries(config), func() { freeObject(t, config) }
	case *layout.CpDevice:
		return &config.Entity, []*layout.CounterRegistry{&config.CounterRegistry}, func() {
			require.NoError(t, cpconfig.FreePlainDevice(config))
		}
	}
	panic("a configuration is a module, an object or a device")
}

// sharedState reads the shared state of the fixture's instance.
func sharedState(memory *publishedFixture) shared {
	cp := memory.Instance().Cp
	gen := cp.CpConfigGen.Get()
	state := shared{
		ZoneFree:  shmalloc.FreeSize(&cp.BlockAllocator),
		AgentFree: map[*layout.Agent]uintptr{},
		Marks:     memory.Marks(),
		Published: gen,
		Header:    bytes.Clone(bytesOf(gen)),
		Counts:    map[*uint64]uint64{},
	}
	count := func(counter *uint64) { state.Counts[counter] = atomic.LoadUint64(counter) }
	for agent := range agentsOf(cp) {
		state.AgentFree[agent] = shmalloc.FreeSize(&agent.BlockAllocator)
		count(&agent.LoadedEntityCount)
	}
	registries := registriesOf(gen)
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

// requireLeftAsFound fails unless a failed update left the shared state
// as it was before its configurations were built, once those no
// generation holds are freed, and their counter registries usable.
//
// The configurations stay linked: freeing them returns what their links
// grew on their agents.
func requireLeftAsFound[C configuration](t testing.TB, before shared, memory *publishedFixture, configs ...C) {
	t.Helper()
	for _, config := range configs {
		entity, registries, free := partsOf(t, config)
		for _, counters := range registries {
			requireIndexed(t, counters)
		}
		if atomic.LoadUint64(&entity.ConfigItem.Refcnt) == 0 {
			free()
		}
	}
	after := sharedState(memory)
	require.Same(t, before.Published, after.Published, "the previous generation stays published")
	require.Equal(t, before.Header, after.Header, "the published generation's header")
	require.Equal(t, before.Items, after.Items, "the published generation's items")
	require.Equal(t, before.Roots, after.Roots, "the published generation's worker roots")
	require.Equal(t, before.ZoneFree, after.ZoneFree, "the zone's free bytes")
	require.Equal(t, before.AgentFree, after.AgentFree, "the agents' free bytes once the configurations are freed")
	// A free more than a red zone smaller than its block leaves the tail
	// addressable, so every run unaddressable before must stay inside one
	// after. More may be unaddressable after: a block taken before this
	// process marked the zone starts addressable whole, and a later request
	// that reuses it poisons up to its own size when freed.
	for _, run := range before.Marks {
		require.True(t, slices.ContainsFunc(after.Marks, func(kept helperproto.Range) bool {
			return kept.Offset <= run.Offset && run.Offset+run.Size <= kept.Offset+kept.Size
		}), "the zone's bytes %d long at offset %#x stay unaddressable", run.Size, run.Offset)
	}
	require.Equal(t, before.Counts, after.Counts, "the reference and loaded-entity counts")
}

// publishedModule returns the blackhole configuration of that name the
// generation publishes.
func publishedModule(t testing.TB, gen *layout.CpConfigGen, name string) *layout.CpModule {
	t.Helper()
	for _, item := range registry.Items(&gen.ModuleRegistry.EntityRegistry.Registry) {
		if module := (*layout.CpModule)(unsafe.Pointer(item)); shm.String(module.Name[:]) == name {
			return module
		}
	}
	t.Fatalf("generation %d publishes no module %q", gen.Gen, name)
	return nil
}

// registriesOf returns a generation's registries in the order an update
// copies them.
func registriesOf(gen *layout.CpConfigGen) []*layout.Registry {
	return []*layout.Registry{
		&gen.ModuleRegistry.EntityRegistry.Registry,
		&gen.FunctionRegistry.Registry,
		&gen.PipelineRegistry.Registry,
		&gen.DeviceRegistry.EntityRegistry.Registry,
		&gen.ObjectRegistry.EntityRegistry.Registry,
	}
}

// copiedRegistries names a generation's registries in the order an
// update copies them.
var copiedRegistries = []string{"module", "function", "pipeline", "device", "object"}

// copyRequests returns the requests an update's copy of the generation
// makes, in its order.
//
// The generation comes first, then each registry's slots and index.
func copyRequests(gen *layout.CpConfigGen) []uintptr {
	registries := registriesOf(gen)
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

// supersedeOwner attaches the agent "owner" again, as a restarted process
// does; the published configurations keep the superseded one alive.
func supersedeOwner(t testing.TB, memory *publishedFixture) {
	t.Helper()
	_, err := agent.Attach(memory.Instance(), "owner", uint64(2*datasize.MB))
	require.NoError(t, err)
}
