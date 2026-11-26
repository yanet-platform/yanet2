//go:build linux && (amd64 || arm64)

package cpconfig_test

import (
	"net"
	"runtime"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// publishBound is how long a test waits for a publication to workers of
// their own before it fails.
//
// Such a publication waits for every worker without a timeout of its own,
// so a worker that never acknowledges would hang the test.
const publishBound = 10 * time.Second

// updateModules publishes the modules, delivering the contexts to the
// workers.
func updateModules(t testing.TB, memory *publishedFixture, modules ...*layout.CpModule) error {
	t.Helper()
	return cpconfig.UpdateModules(memory.Instance(), modules, memory.deliver())
}

// icmpPackets returns the frames of that many echo requests.
func icmpPackets(t *testing.T, count int) [][]byte {
	t.Helper()
	packets := make([][]byte, 0, count)
	for idx := range count {
		eth := layers.Ethernet{
			SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
			DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
			EthernetType: layers.EthernetTypeIPv4,
		}
		ip := layers.IPv4{
			Version: 4, TTL: 64, Protocol: layers.IPProtocolICMPv4,
			SrcIP: net.IPv4(10, 0, 0, byte(idx+1)), DstIP: net.IPv4(192, 168, 1, 1),
		}
		icmp := layers.ICMPv4{TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0)}
		packets = append(packets, xpacket.LayersToPacket(t, &eth, &ip, &icmp).Data())
	}
	return packets
}

// handlePackets runs the frames through port0 on the first worker and
// returns the frames dropped.
func handlePackets(t *testing.T, memory *publishedFixture, frames [][]byte) [][]byte {
	t.Helper()
	result, err := memory.Round(0, 0, frames...)
	require.NoError(t, err)
	return result.Drop
}

// moduleDrops returns the drop count of blackhole a, summed over the
// workers.
func moduleDrops(t *testing.T, memory *publishedFixture) uint64 {
	t.Helper()
	return counterTotal(t, memory, "drop", zone.CounterTag{Key: "module_name", Value: "a"}, zone.CounterTag{Key: "kind", Value: "module"})
}

// requireWorkersRunPublished fails unless every worker runs the contexts
// the published generation built for it.
func requireWorkersRunPublished(t *testing.T, memory *publishedFixture) {
	t.Helper()
	instance := memory.Instance()
	published := instance.Cp.CpConfigGen.Get()
	roots := unsafe.Slice(published.ConfigGenEctxs.Get(), published.ConfigGenEctxCount)
	workers := unsafe.Slice(instance.Dp.Workers.Get(), instance.Dp.WorkerCount)
	for idx := range workers {
		require.Same(t, roots[idx].Get(), workers[idx].Get().ConfigGenEctx.Get(), "worker %d runs the published contexts", idx)
	}
}

// publishedModules returns the module configurations the published
// generation lists.
func publishedModules(t *testing.T, memory *publishedFixture) []zone.Module {
	t.Helper()
	pinned, err := memory.Instance().Pin()
	require.NoError(t, err)
	defer pinned.Release()
	return pinned.Modules()
}

// Test_UpdateModules_DataplaneRunsGoGeneration verifies that the
// dataplane runs a module configuration Go published on contexts Go built.
//
// The workers take the new generation, the module drops its packets, and
// the module's counters continue from the generation it replaced.
func Test_UpdateModules_DataplaneRunsGoGeneration(t *testing.T) {
	memory := newPublishedFixture(t)
	require.Len(t, handlePackets(t, memory, icmpPackets(t, 3)), 3, "the first generation drops through blackhole a")
	before := moduleDrops(t, memory)
	generation := memory.Instance().Cp.CpConfigGen.Get().Gen

	require.NoError(t, updateModules(t, memory, newBlackhole(t, memory, "a")))

	require.Equal(t, generation+1, memory.Instance().Cp.CpConfigGen.Get().Gen)
	requireWorkersRunPublished(t, memory)
	require.Len(t, handlePackets(t, memory, icmpPackets(t, 5)), 5, "the new generation drops through blackhole a")
	require.Equal(t, before+5, moduleDrops(t, memory), "module counters continue across the publication")
}

// Test_UpdateModules_ReplacesModule verifies that a module update
// publishes the configuration in place of the one of its name.
//
// The module is new, or replaces one a chain runs, or one no chain runs,
// on each fixture. The generation lists it, holds it once and releases a
// replaced one for its free; once that is freed, the zone and the agent
// hold the free bytes they held before the update's configuration was
// built.
func Test_UpdateModules_ReplacesModule(t *testing.T) {
	for _, fixture := range fixtures {
		for _, name := range []string{"fresh", "a", "b"} {
			t.Run(fixture.name+", module "+name, func(t *testing.T) {
				memory := fixture.newFunc(t)
				cp := memory.Instance().Cp
				listed := publishedModules(t, memory)
				replaces := slices.ContainsFunc(listed, func(module zone.Module) bool { return module.Name == name })
				var replaced *layout.CpModule
				if replaces {
					replaced = publishedModule(t, cp.CpConfigGen.Get(), name)
				} else {
					listed = append(listed, zone.Module{Type: "blackhole", Name: name, Agent: "owner"})
				}
				zoneFree := shmalloc.FreeSize(&cp.BlockAllocator)
				agentFree := shmalloc.FreeSize(&memory.owner.Agent.BlockAllocator)
				module := newBlackhole(t, memory, name)

				require.NoError(t, updateModules(t, memory, module))

				require.ElementsMatch(t, listed, publishedModules(t, memory))
				require.Same(t, module, publishedModule(t, cp.CpConfigGen.Get(), name))
				require.Equal(t, uint64(1), atomic.LoadUint64(&module.Entity.ConfigItem.Refcnt), "the generation holds the module")
				if replaces {
					require.NoError(t, cpconfig.FreeBlackhole(replaced))
					require.Equal(t, zoneFree, shmalloc.FreeSize(&cp.BlockAllocator), "the zone's free bytes")
					require.Equal(t, agentFree, shmalloc.FreeSize(&memory.owner.Agent.BlockAllocator), "the agent's free bytes")
				}
			})
		}
	}
}

// firstWorkerRoot returns the root context of the first worker of the
// published generation.
func firstWorkerRoot(memory *publishedFixture) *layout.ConfigGenEctx {
	published := memory.Instance().Cp.CpConfigGen.Get()
	return unsafe.Slice(published.ConfigGenEctxs.Get(), published.ConfigGenEctxCount)[0].Get()
}

// linkedModuleContext returns the first worker's context of blackhole
// linked of the rich fixture: the second module of chain heavy, which
// function drop runs on port0's input.
func linkedModuleContext(t *testing.T, memory *publishedFixture) *layout.ModuleEctx {
	t.Helper()
	input := deviceContext(t, memory, "port0").InputPipelines.Get()
	drop := unsafe.Slice(input.PipelinePtrs.Get(), input.PipelineCount)[0].Get()
	function := unsafe.Slice(drop.FunctionPtrs.Get(), drop.Length)[0].Get()
	heavy := unsafe.Slice(function.ChainPtrs.Get(), function.ChainCount)[0].Get()
	return unsafe.Slice(heavy.ModulePtrs.Get(), heavy.Length)[1].Get()
}

// Test_UpdateModules_LinksObjectsAndDevices verifies that a module
// context reaches the objects, runtime storages and devices it names.
//
// Weights and ports shape the device and function tables as well.
func Test_UpdateModules_LinksObjectsAndDevices(t *testing.T) {
	memory := newRichFixture(t)
	require.NoError(t, updateModules(t, memory, newBlackhole(t, memory, "a")))
	root := firstWorkerRoot(memory)

	devices := map[string]*layout.DeviceEctx{}
	pointers := unsafe.Slice(root.DevicePtrs.Get(), root.DeviceCount)
	egress := unsafe.Slice(root.EgressBacking.Get(), root.DeviceCount)
	ports := map[string]uint16{}
	for idx := range pointers {
		if device := pointers[idx].Get(); device != nil {
			name := shm.String(device.CpDevice.Get().Name[:])
			devices[name] = device
			ports[name] = egress[idx]
		}
	}
	require.Equal(t, map[string]uint16{"port0": 0, "port1": 1, "virt": 0xffff}, ports)

	input := devices["port0"].InputPipelines.Get()
	require.Equal(t, uint64(3), input.PipelineMapSize, "drop at weight 2 and dummy at weight 1")
	require.True(t, devices["port0"].OutputPipelines.Get().PipelinePtrs.IsNil(), "no output pipelines")
	drop := unsafe.Slice(input.PipelinePtrs.Get(), input.PipelineCount)[0].Get()
	function := unsafe.Slice(drop.FunctionPtrs.Get(), drop.Length)[0].Get()
	require.Equal(t, uint64(4), function.ChainMapSize, "heavy at weight 3 and light at weight 1")
	linked := linkedModuleContext(t, memory)
	require.Equal(t, "linked", shm.String(linked.CpModule.Get().Name[:]))

	require.Equal(t, uint64(1), linked.ObjectLinkCount)
	link := &unsafe.Slice(linked.ObjectLinks.Get(), linked.ObjectLinkCount)[0]
	object := link.ObjectEctx.Get()
	require.NotNil(t, object)
	require.Equal(t, "events", shm.String(object.CpObject.Get().Name[:]))
	require.NotNil(t, link.CounterStorage.Get())
	require.Equal(t, uint64(1), linked.RuntimeCounterStorageCount)
	require.NotNil(t, unsafe.Slice(linked.RuntimeCounterStorages.Get(), 1)[0].Get())

	targets := unsafe.Slice(linked.DeviceTargets.Get(), linked.DeviceTargetCount)
	require.Len(t, targets, 3, "the device named \"\", port1 and virt")
	require.Equal(t, uint16(0xffff), targets[0].DeviceId)
	require.Same(t, devices["port1"].InputPipelines.Get(), targets[1].InputEntry.Get())
	require.Same(t, devices["virt"].OutputPipelines.Get(), targets[2].OutputEntry.Get())
	require.Zero(t, linked.ModuleDeviceId, "port0 is not among the module's devices")
}

// Test_UpdateModules_RefusesSupersededOwner verifies that a module of a
// superseded agent is refused, publishing nothing and taking nothing.
func Test_UpdateModules_RefusesSupersededOwner(t *testing.T) {
	memory := newPublishedFixture(t)
	supersedeOwner(t, memory)
	before := sharedState(memory)
	module := newBlackhole(t, memory, "late")

	err := updateModules(t, memory, module)

	require.ErrorIs(t, err, cpconfig.ErrSupersededOwner)
	requireLeftAsFound(t, before, memory, module)
}

// Test_UpdateModules_RefusesRepeatedModule verifies that an update naming
// one module twice is refused, publishing nothing and taking nothing.
func Test_UpdateModules_RefusesRepeatedModule(t *testing.T) {
	memory := newPublishedFixture(t)
	before := sharedState(memory)
	first := newBlackhole(t, memory, "twice")
	second := newBlackhole(t, memory, "twice")

	err := updateModules(t, memory, first, second)

	require.ErrorIs(t, err, cpconfig.ErrRepeated)
	requireLeftAsFound(t, before, memory, first, second)
}

// Test_UpdateModules_ExhaustedZoneFreesUpdate verifies that an update the
// zone runs out of memory for part-way frees every block it took from the
// zone and drops every reference it took.
//
// The previous generation stays published and the exhaustion is reported,
// for a configuration of a new name, for a fresh configuration replacing
// a published one, whose counters it links inside the build, for one
// whose link carries names and so grows on its agent, for one with a
// tagged runtime registry and for the published configuration itself.
// The budgets of each kind together must run out in the copy and in both
// workers.
func Test_UpdateModules_ExhaustedZoneFreesUpdate(t *testing.T) {
	modules := []struct {
		name string
		// carried is whether a published configuration "carried" holds
		// counters the update's configuration lacks.
		carried bool
		// module returns the configuration the update publishes.
		module func(t *testing.T, memory *publishedFixture) *layout.CpModule
	}{
		{"configuration of a new name", false, func(t *testing.T, memory *publishedFixture) *layout.CpModule {
			return newBlackhole(t, memory, "fresh")
		}},
		{"fresh configuration", false, func(t *testing.T, memory *publishedFixture) *layout.CpModule {
			return newBlackhole(t, memory, "a")
		}},
		{"configuration carrying names", true, func(t *testing.T, memory *publishedFixture) *layout.CpModule {
			return newBlackhole(t, memory, "carried")
		}},
		{"configuration with a runtime registry", false, func(t *testing.T, memory *publishedFixture) *layout.CpModule {
			module := newBlackhole(t, memory, "a")
			registerRuntimeCounter(t, module)
			return module
		}},
		{"published configuration", false, func(t *testing.T, memory *publishedFixture) *layout.CpModule {
			return publishedModule(t, memory.Instance().Cp.CpConfigGen.Get(), "a")
		}},
	}
	for _, kind := range modules {
		t.Run(kind.name, func(t *testing.T) {
			failures := map[string]bool{}
			for _, budget := range exhaustionBudgets {
				t.Run(budget.name, func(t *testing.T) {
					memory := newPublishedFixture(t)
					if kind.carried {
						publishCarried(t, memory)
					}
					nativetest.LeaveOnly(t, &memory.Instance().Cp.BlockAllocator, requestsFor(budget.blocks)...)
					before := sharedState(memory)
					module := kind.module(t, memory)

					err := updateModules(t, memory, module)

					require.ErrorIs(t, err, shmalloc.ErrNoMemory)
					requireLeftAsFound(t, before, memory, module)
					failures[failurePlace(t, err)] = true
				})
			}
			require.Equal(t, map[string]bool{"copy": true, "worker 0": true, "worker 1": true}, failures)
		})
	}
}

// Test_UpdateModules_ReleasedSlotStartsFromZero verifies that a counter
// placed in a slot released by a counted name starts from zero in every
// worker of the generation published with it.
func Test_UpdateModules_ReleasedSlotStartsFromZero(t *testing.T) {
	memory := newPublishedFixture(t)
	require.NoError(t, updateModules(t, memory, newBlackhole(t, memory, "a", "dropped")))
	require.NoError(t, updateModules(t, memory, newBlackhole(t, memory, "a")))
	countAll(memory)

	require.NoError(t, updateModules(t, memory, newBlackhole(t, memory, "a", "novel")))

	workers := 0
	for counter, values := range publishedValues(memory.Instance().Cp.CpConfigGen.Get()) {
		if shm.String(counter.Name[:]) == "novel" {
			require.Equal(t, make([]uint64, len(values)), values)
			workers++
		}
	}
	require.Equal(t, 2, workers, "both workers count novel")
}

// Test_UpdateModules_CopyFailureFreesCopy verifies that a copy failing at
// any registry reports that registry and frees the generation it copied.
//
// The zone keeps the blocks of the generation and of every registry
// copied before the failing one, and the slots of the failing one, whose
// index then finds no block.
func Test_UpdateModules_CopyFailureFreesCopy(t *testing.T) {
	for failing, name := range copiedRegistries {
		t.Run(name+" registry", func(t *testing.T) {
			memory := newRichFixture(t)
			instance := memory.Instance()
			kept := copyRequests(instance.Cp.CpConfigGen.Get())[:2+2*failing]
			nativetest.LeaveOnly(t, &instance.Cp.BlockAllocator, kept...)
			before := sharedState(memory)
			module := newBlackhole(t, memory, "a")

			err := updateModules(t, memory, module)

			require.ErrorIs(t, err, shmalloc.ErrNoMemory)
			require.ErrorContains(t, err, "copy "+name+" registry")
			requireLeftAsFound(t, before, memory, module)
		})
	}
}

// Test_UpdateModules_FreesFailedArraysAtTheirSize verifies that a
// context whose pointer array fails goes back at its allocated size.
//
// Its first array goes back too. The zone keeps one block for each
// request the update makes before the failing array, in the update's
// order, so that array finds none; the free bytes and counts must be back
// where they were.
func Test_UpdateModules_FreesFailedArraysAtTheirSize(t *testing.T) {
	pointer := shm.PtrSize
	// beforeEntryArrays is what the worker's tree asks for up to the
	// pointer arrays of port0's input entry, sized by its 40 weights.
	beforeEntryArrays := func(devices, objects uintptr) []uintptr {
		return append(
			treeRequests(devices, objects),
			unsafe.Sizeof(layout.DeviceEctx{}),
			unsafe.Sizeof(layout.DeviceEntryEctx{})+pointer*40,
		)
	}
	cases := []struct {
		name string
		// kept is the requests the zone serves after the copy and before
		// the failing one.
		kept func(devices, objects uintptr) []uintptr
		// failsAt is the allocation the update reports failing.
		failsAt string
	}{
		{"pipeline array of a weighted entry", beforeEntryArrays, "allocate pipelines of device"},
		{
			"pipeline address array of a weighted entry",
			func(devices, objects uintptr) []uintptr {
				return append(beforeEntryArrays(devices, objects), pointer)
			},
			"allocate pipeline addresses of device",
		},
		{
			"chain address array of a function",
			func(devices, objects uintptr) []uintptr {
				return append(
					beforeEntryArrays(devices, objects),
					pointer, pointer,
					unsafe.Sizeof(layout.PipelineEctx{})+pointer, pointer,
					unsafe.Sizeof(layout.FunctionEctx{})+pointer, pointer,
				)
			},
			"allocate chain addresses of function",
		},
		{
			"module array of a chain",
			func(devices, objects uintptr) []uintptr {
				return append(
					beforeEntryArrays(devices, objects),
					pointer, pointer,
					unsafe.Sizeof(layout.PipelineEctx{})+pointer, pointer,
					unsafe.Sizeof(layout.FunctionEctx{})+pointer, pointer, pointer,
					unsafe.Sizeof(layout.ChainEctx{})+pointer,
				)
			},
			"allocate modules of chain",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := newWeightedFixture(t)
			instance := memory.Instance()
			published := instance.Cp.CpConfigGen.Get()
			// The published configuration replaces itself: its upsert and
			// link take no block.
			module := publishedModule(t, published, "a")
			devices := uintptr(published.DeviceRegistry.EntityRegistry.Registry.Capacity)
			objects := uintptr(published.ObjectRegistry.EntityRegistry.Registry.Capacity)
			kept := append(copyRequests(published), tc.kept(devices, objects)...)
			nativetest.LeaveOnly(t, &instance.Cp.BlockAllocator, kept...)
			before := sharedState(memory)

			err := updateModules(t, memory, module)

			require.ErrorIs(t, err, shmalloc.ErrNoMemory)
			require.ErrorContains(t, err, tc.failsAt)
			requireLeftAsFound(t, before, memory, module)
		})
	}
}

// treeRequests returns what a one-worker update asks the zone for after
// the copy, in order, up to the worker's object contexts.
//
// The registries' capacities size the device and object tables, and the
// dataplane has one port.
func treeRequests(devices, objects uintptr) []uintptr {
	pointer := shm.PtrSize
	return []uintptr{
		pointer,
		unsafe.Sizeof(layout.ConfigGenEctx{}) + pointer*devices,
		pointer * devices,
		2 * devices,
		unsafe.Sizeof(layout.PacketList{}),
		unsafe.Sizeof(layout.CpConfigCounterStorageRegistry{}),
		8 * unsafe.Sizeof(layout.CpCounterStorage{}),
		8 * 4 * unsafe.Sizeof(layout.HashIndexEntry{}),
		pointer * objects,
	}
}

// moduleRequests returns what a one-worker update of the weighted fixture
// asks the zone for after the copy, in order, up to blackhole a's module
// context.
func moduleRequests(devices, objects uintptr) []uintptr {
	pointer := shm.PtrSize
	return append(
		treeRequests(devices, objects),
		unsafe.Sizeof(layout.DeviceEctx{}),
		unsafe.Sizeof(layout.DeviceEntryEctx{})+pointer*40,
		pointer, pointer,
		unsafe.Sizeof(layout.PipelineEctx{})+pointer, pointer,
		unsafe.Sizeof(layout.FunctionEctx{})+pointer, pointer, pointer,
		unsafe.Sizeof(layout.ChainEctx{})+pointer, pointer,
		unsafe.Sizeof(layout.ModuleEctx{}),
	)
}

// Test_UpdateModules_FreesFailedContexts verifies that a context or
// counter storage failing part-way frees it and what it took.
//
// The zone keeps one block for each request the update makes before the
// failing one, in the update's order. A fresh module spawns storages of
// its own, whose block, value handles or pages find no block; a worker's
// storage index finds none; the ninth object's registration grows the
// worker's storage registry, which finds none.
func Test_UpdateModules_FreesFailedContexts(t *testing.T) {
	pointer := shm.PtrSize
	cases := []struct {
		name string
		// fixture returns the published dataplane.
		fixture func(t testing.TB) *publishedFixture
		// module returns the configuration the update publishes.
		module func(t *testing.T, memory *publishedFixture) *layout.CpModule
		// kept is the requests the zone serves after the copy and before
		// the failing one.
		kept func(devices, objects uintptr) []uintptr
		// failsAt is the allocation the update reports failing.
		failsAt string
	}{
		{
			"module whose storage fails",
			newWeightedFixture,
			func(t *testing.T, memory *publishedFixture) *layout.CpModule {
				return newBlackhole(t, memory, "a")
			},
			moduleRequests,
			"allocate storage",
		},
		{
			"worker whose storage index fails",
			newWeightedFixture,
			func(t *testing.T, memory *publishedFixture) *layout.CpModule {
				return newBlackhole(t, memory, "a")
			},
			func(devices, objects uintptr) []uintptr {
				return treeRequests(devices, objects)[:7]
			},
			"allocate counter storage index",
		},
		{
			"module whose value handles fail",
			newWeightedFixture,
			func(t *testing.T, memory *publishedFixture) *layout.CpModule {
				return newBlackhole(t, memory, "a")
			},
			func(devices, objects uintptr) []uintptr {
				return append(moduleRequests(devices, objects), unsafe.Sizeof(layout.CounterStorage{}), pointer)
			},
			"allocate value handles",
		},
		{
			"runtime storage whose page fails",
			newWeightedFixture,
			func(t *testing.T, memory *publishedFixture) *layout.CpModule {
				module := newBlackhole(t, memory, "a")
				registerRuntimeCounter(t, module)
				return module
			},
			func(devices, objects uintptr) []uintptr {
				return append(
					moduleRequests(devices, objects),
					unsafe.Sizeof(layout.CounterStorage{}), pointer, 5*pointer, pointer, pointer,
					unsafe.Sizeof(layout.CounterStorage{}), pointer, unsafe.Sizeof(layout.CounterStorageBlock{}),
				)
			},
			"allocate page of pool 2",
		},
		{
			"object whose registration grows the storage registry",
			func(t testing.TB) *publishedFixture { return newObjectsFixture(t, 9) },
			func(t *testing.T, memory *publishedFixture) *layout.CpModule {
				return publishedModule(t, memory.Instance().Cp.CpConfigGen.Get(), "a")
			},
			func(devices, objects uintptr) []uintptr {
				requests := treeRequests(devices, objects)
				for range 9 {
					requests = append(requests, unsafe.Sizeof(layout.ObjectEctx{}))
				}
				return requests
			},
			"grow counter storage registry",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := tc.fixture(t)
			instance := memory.Instance()
			published := instance.Cp.CpConfigGen.Get()
			devices := uintptr(published.DeviceRegistry.EntityRegistry.Registry.Capacity)
			objects := uintptr(published.ObjectRegistry.EntityRegistry.Registry.Capacity)
			kept := append(copyRequests(published), tc.kept(devices, objects)...)
			nativetest.LeaveOnly(t, &instance.Cp.BlockAllocator, kept...)
			before := sharedState(memory)
			module := tc.module(t, memory)

			err := updateModules(t, memory, module)

			require.ErrorIs(t, err, shmalloc.ErrNoMemory)
			require.ErrorContains(t, err, tc.failsAt)
			requireLeftAsFound(t, before, memory, module)
		})
	}
}

// Test_UpdateModules_UpsertFailureFreesCopy verifies that an upsert unable
// to grow a full module registry fails the update and frees the copy.
//
// The published registry holds eight modules, its capacity, so a new one
// needs a larger slot array and index. The zone keeps exactly the blocks
// of the copy, then also those of the grown slots, so the upsert fails at
// the slots or at the index.
func Test_UpdateModules_UpsertFailureFreesCopy(t *testing.T) {
	cases := []struct {
		name string
		// extra is the requests the zone keeps beyond the copy's blocks.
		extra []uintptr
	}{
		{"grown slots unavailable", nil},
		{"grown index unavailable", []uintptr{16 * shm.PtrSize}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := newPublishedFixture(t)
			cp := memory.Instance().Cp
			for _, name := range []string{"d", "e", "f", "g", "h"} {
				require.NoError(t, updateModules(t, memory, newBlackhole(t, memory, name)))
			}
			published := cp.CpConfigGen.Get()
			requireFull(t, &published.ModuleRegistry.EntityRegistry.Registry)
			nativetest.LeaveOnly(t, &cp.BlockAllocator, append(copyRequests(published), tc.extra...)...)
			before := sharedState(memory)
			module := newBlackhole(t, memory, "fresh")

			err := updateModules(t, memory, module)

			require.ErrorIs(t, err, shmalloc.ErrNoMemory)
			require.ErrorContains(t, err, "upsert module")
			requireLeftAsFound(t, before, memory, module)
		})
	}
}

// Test_UpdateModules_WaitsForEveryWorker verifies that a publication to
// workers of their own retires the replaced generation only at the end.
//
// The replaced generation is pinned, so the publication's reference shows
// in its count, which must stay until the last worker acknowledges.
func Test_UpdateModules_WaitsForEveryWorker(t *testing.T) {
	memory := newPublishedFixture(t)
	instance := memory.Instance()
	instance.Dp.ExternalWorkerRounds = false
	t.Cleanup(func() { instance.Dp.ExternalWorkerRounds = true })
	module := newBlackhole(t, memory, "a")
	pinned, err := instance.Pin()
	require.NoError(t, err)
	t.Cleanup(pinned.Release)
	old := pinned.Gen

	var publication errgroup.Group
	done := make(chan struct{})
	publication.Go(func() error {
		defer close(done)
		return cpconfig.UpdateModules(instance, []*layout.CpModule{module})
	})
	published := awaitGeneration(t, memory, old, done, &publication)

	workers := unsafe.Slice(instance.Dp.Workers.Get(), instance.Dp.WorkerCount)
	roots := unsafe.Slice(published.ConfigGenEctxs.Get(), published.ConfigGenEctxCount)
	for idx := range workers {
		require.Equal(t, uint64(2), atomic.LoadUint64(&old.Refcnt), "retired before worker %d acknowledged", idx)
		select {
		case <-done:
			t.Fatalf("the publication returned before worker %d acknowledged: %v", idx, publication.Wait())
		default:
		}
		worker := workers[idx].Get()
		worker.ConfigGenEctx.Store(roots[idx].Get())
		atomic.StoreUint64(&worker.Gen, published.Gen)
	}
	select {
	case <-done:
		require.NoError(t, publication.Wait())
	case <-time.After(publishBound):
		// The publication still runs on the dataplane's memory.
		memory.Abandon()
		t.Fatalf("the publication did not return within %s", publishBound)
	}
	require.Equal(t, uint64(1), atomic.LoadUint64(&old.Refcnt))
}

// awaitGeneration returns the generation a running publication publishes
// in place of the replaced one, failing the test otherwise.
//
// The test fails if the publication, which closes done when it returns,
// returns first or publishes nothing within the bound.
func awaitGeneration(
	t *testing.T,
	memory *publishedFixture,
	old *layout.CpConfigGen,
	done <-chan struct{},
	publication *errgroup.Group,
) *layout.CpConfigGen {
	t.Helper()
	deadline := time.Now().Add(publishBound)
	for {
		select {
		case <-done:
			t.Fatalf("the publication returned before publishing: %v", publication.Wait())
		default:
		}
		if published := memory.Instance().Cp.CpConfigGen.Load(); published != old {
			return published
		}
		if time.Now().After(deadline) {
			memory.Abandon()
			t.Fatalf("the generation was not published within %s", publishBound)
		}
		runtime.Gosched()
	}
}

// Test_UpdateModules_RefusesMissingObject verifies that a module linking
// an unpublished object is refused after the copy, which is freed.
func Test_UpdateModules_RefusesMissingObject(t *testing.T) {
	memory := newPublishedFixture(t)
	before := sharedState(memory)
	module := newBlackhole(t, memory, "a")
	_, err := cpconfig.LinkObject(module, "ring", "absent")
	require.NoError(t, err)

	err = updateModules(t, memory, module)

	require.ErrorIs(t, err, cpconfig.ErrMissingObject)
	requireLeftAsFound(t, before, memory, module)
}

// publishCarried publishes a blackhole configuration "carried" whose
// extra counters a replacement lacks.
//
// The replacement carries their names and grows its counter registry on
// its agent.
func publishCarried(t *testing.T, memory *publishedFixture) {
	t.Helper()
	var extra []string
	for idx := range 12 {
		extra = append(extra, "extra"+strconv.Itoa(idx))
	}
	require.NoError(t, updateModules(t, memory, newBlackhole(t, memory, "carried", extra...)))
}

// Test_UpdateModules_AgentMemoryFailureFreesCopy verifies that an update
// whose counter link exhausts its agent's memory fails at the link.
//
// The configuration carries twelve names of the one it replaces, so its
// names and their index grow twice. The agent keeps no block, or enough
// for the first growths only, so the link fails at its first block or
// after it took some. The zone's blocks and every reference go back, the
// module keeps what its link grew until it is freed, and the exhaustion
// is reported.
func Test_UpdateModules_AgentMemoryFailureFreesCopy(t *testing.T) {
	budgets := []struct {
		name   string
		blocks []uintptr
	}{
		{"no block", nil},
		{"one 4 KB block", []uintptr{4 * kilobyte}},
		{"one 4 KB and one 512 B block", []uintptr{4 * kilobyte, kilobyte / 2}},
	}
	for _, budget := range budgets {
		t.Run(budget.name, func(t *testing.T) {
			memory := newPublishedFixture(t)
			publishCarried(t, memory)
			before := sharedState(memory)
			module := newBlackhole(t, memory, "carried")
			restore := nativetest.LeaveOnly(t, &memory.owner.Agent.BlockAllocator, requestsFor(budget.blocks)...)

			err := updateModules(t, memory, module)
			restore()

			require.ErrorIs(t, err, shmalloc.ErrNoMemory)
			require.ErrorContains(t, err, "link counter registry")
			requireLeftAsFound(t, before, memory, module)
		})
	}
}

// newBlackhole returns a blackhole configuration of the name with extra
// own counters of one value each.
func newBlackhole(t testing.TB, memory *publishedFixture, name string, extra ...string) *layout.CpModule {
	t.Helper()
	module, err := cpconfig.NewBlackhole(memory.owner, name)
	require.NoError(t, err)
	for _, counter := range extra {
		_, err = cpconfig.RegisterCounter(&module.CounterRegistry, counter, 1)
		require.NoError(t, err)
	}
	return module
}

// deleteModule deletes the blackhole configuration, delivering the
// contexts to the workers.
func deleteModule(t *testing.T, memory *publishedFixture, name string) error {
	t.Helper()
	return cpconfig.DeleteModule(memory.Instance(), "blackhole", name, memory.deliver())
}

// Test_DeleteModule_RemovesModule verifies that deleting a published
// module configuration no chain runs empties its registry slot, drops its
// owner's loaded count and releases it for its free, and that the workers
// run the generation published without it.
func Test_DeleteModule_RemovesModule(t *testing.T) {
	memory := newPublishedFixture(t)
	module := publishedModule(t, memory.Instance().Cp.CpConfigGen.Get(), "b")
	loaded := atomic.LoadUint64(&memory.owner.Agent.LoadedEntityCount)

	require.NoError(t, deleteModule(t, memory, "b"))

	require.ElementsMatch(t, []zone.Module{
		{Type: "blackhole", Name: "a", Agent: "owner"},
		{Type: "blackhole", Name: "c", Agent: "owner"},
	}, publishedModules(t, memory))
	require.Equal(t, loaded-1, atomic.LoadUint64(&memory.owner.Agent.LoadedEntityCount))
	require.Zero(t, atomic.LoadUint64(&module.Entity.ConfigItem.Refcnt))
	requireWorkersRunPublished(t, memory)
}

// Test_DeleteModule_FailureFreesCopy verifies that a delete of a module
// the generation does not publish, or of one a chain runs, frees the copy
// it built, publishing nothing and taking nothing.
func Test_DeleteModule_FailureFreesCopy(t *testing.T) {
	cases := []struct {
		name string
		// module is the name of the module the delete names.
		module string
		// check checks the error the delete reports.
		check func(t *testing.T, err error)
	}{
		{
			name:   "module not published",
			module: "absent",
			check: func(t *testing.T, err error) {
				require.ErrorIs(t, err, cpconfig.ErrNotFound)
			},
		},
		{
			name:   "module a chain runs",
			module: "a",
			check: func(t *testing.T, err error) {
				require.ErrorIs(t, err, cpconfig.ErrMissingModule)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := newPublishedFixture(t)
			before := sharedState(memory)

			err := deleteModule(t, memory, tc.module)

			tc.check(t, err)
			requireLeftAsFound[*layout.CpModule](t, before, memory)
		})
	}
}

// Test_DeleteModule_FreeRepublishAndDeleteAgain verifies that a module
// configuration deleted, freed, published again under its name and
// deleted again leaves nothing behind.
//
// Once both are freed the agent's memory and loaded count are back where
// they were, and a third delete finds no module.
func Test_DeleteModule_FreeRepublishAndDeleteAgain(t *testing.T) {
	memory := newPublishedFixture(t)
	owner := memory.owner.Agent
	original := publishedModule(t, memory.Instance().Cp.CpConfigGen.Get(), "b")
	require.NoError(t, deleteModule(t, memory, "b"))
	require.NoError(t, cpconfig.FreeBlackhole(original))
	free := shmalloc.FreeSize(&owner.BlockAllocator)
	loaded := atomic.LoadUint64(&owner.LoadedEntityCount)

	replacement := newBlackhole(t, memory, "b")
	require.NoError(t, updateModules(t, memory, replacement))
	require.NoError(t, deleteModule(t, memory, "b"))
	require.NoError(t, cpconfig.FreeBlackhole(replacement))

	require.Equal(t, free, shmalloc.FreeSize(&owner.BlockAllocator), "the agent's free bytes")
	require.Equal(t, loaded, atomic.LoadUint64(&owner.LoadedEntityCount), "the agent's loaded count")
	require.ErrorIs(t, deleteModule(t, memory, "b"), cpconfig.ErrNotFound)
}

// Benchmark_UpdateModules_Blackhole measures building a blackhole
// configuration and publishing it.
//
// The dataplane helper delivers the workers' contexts, and each iteration
// frees the configuration its publication replaced, so the agent's memory
// lasts the run.
func Benchmark_UpdateModules_Blackhole(b *testing.B) {
	b.ReportAllocs()
	memory := newPublishedFixture(b)
	replaced := publishedModule(b, memory.Instance().Cp.CpConfigGen.Get(), "a")
	for b.Loop() {
		module := newBlackhole(b, memory, "a")
		require.NoError(b, updateModules(b, memory, module))
		require.NoError(b, cpconfig.FreeBlackhole(replaced))
		replaced = module
	}
}
