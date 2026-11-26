//go:build linux && (amd64 || arm64) && cgo

package cpconfig_test

import (
	"net"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/coracle"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
	"github.com/yanet-platform/yanet2/modules/blackhole/bindings/go/cblackhole"
)

// publishBound is how long a test waits for a publication before it
// fails.
//
// A publication waits for every worker without a timeout of its own, so a
// worker that never acknowledges would hang the test.
const publishBound = 10 * time.Second

// updateModules publishes the modules natively, delivering the contexts
// to the in-process workers, within the publication bound.
func updateModules(t *testing.T, memory *publishedFixture, modules ...*layout.CpModule) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- cpconfig.UpdateModules(memory.Instance(), modules, cpconfig.WithDelivery(nativetest.Deliver))
	}()
	return awaitPublication(t, memory, done)
}

// awaitPublication returns the result of a publication running on the
// fixture, failing the test if it does not return within the bound.
//
// A publication that never returns still runs on the dataplane's memory,
// so the fixture keeps it.
func awaitPublication(t *testing.T, memory *publishedFixture, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(publishBound):
		memory.Abandon()
		t.Fatalf("the publication did not return within %s", publishBound)
		return nil
	}
}

// icmpPackets returns that many echo requests through port0.
func icmpPackets(t *testing.T, count int) []gopacket.Packet {
	t.Helper()
	packets := make([]gopacket.Packet, 0, count)
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
		packets = append(packets, xpacket.LayersToPacket(t, &eth, &ip, &icmp))
	}
	return packets
}

// moduleDrops returns the drop count of blackhole a in the wired chain,
// summed over the workers, as the cgo reader reads it.
func moduleDrops(t *testing.T, memory *publishedFixture) uint64 {
	t.Helper()
	counters := memory.Harness.SharedMemory().DPConfig(0).ModuleCounters(
		"port0", "drop", "drop", "drop_chain", "blackhole", "a", []string{"drop"},
	)
	require.Len(t, counters, 1)
	total := uint64(0)
	for _, values := range counters[0].Values {
		total += values[0]
	}
	return total
}

// Test_UpdateModules_DataplaneRunsGoGeneration verifies that the
// dataplane runs a module configuration Go published on contexts Go built.
//
// The workers take the Go generation, the cgo control plane lists the
// configuration, the module drops its packets, and the module's counters
// continue from the generation C published.
func Test_UpdateModules_DataplaneRunsGoGeneration(t *testing.T) {
	memory := newPublishedFixture(t)
	instance := memory.Instance()
	result, err := memory.Harness.HandlePackets(icmpPackets(t, 3)...)
	require.NoError(t, err)
	require.Len(t, result.Drop, 3, "the C generation drops through blackhole a")
	before := moduleDrops(t, memory)
	cGeneration := instance.Cp.CpConfigGen.Get().Gen

	module, err := cpconfig.NewBlackhole(memory.owner, "a")
	require.NoError(t, err)
	require.NoError(t, updateModules(t, memory, module))

	published := instance.Cp.CpConfigGen.Get()
	require.Equal(t, cGeneration+1, published.Gen)
	roots := unsafe.Slice(published.ConfigGenEctxs.Get(), published.ConfigGenEctxCount)
	workers := unsafe.Slice(instance.Dp.Workers.Get(), instance.Dp.WorkerCount)
	for idx := range workers {
		require.Same(t, roots[idx].Get(), workers[idx].Get().ConfigGenEctx.Get(), "worker %d runs the contexts Go built", idx)
	}
	var listed bool
	for _, config := range memory.Harness.SharedMemory().DPConfig(0).CPConfigs() {
		if config.Type == "blackhole" && config.Name == "a" {
			listed = true
		}
	}
	require.True(t, listed, "the cgo control plane lists the Go configuration")

	result, err = memory.Harness.HandlePackets(icmpPackets(t, 5)...)
	require.NoError(t, err)
	require.Len(t, result.Drop, 5, "the Go generation drops through blackhole a")
	require.Equal(t, before+5, moduleDrops(t, memory), "module counters continue across the Go publication")
}

// Test_UpdateModules_MatchesC verifies that a whole module update from Go
// leaves the storage as cp_config_update_modules does.
//
// The update covers the generation copy, the upsert, every worker's
// contexts and their counter storages, the publication and the teardown
// of the replaced generation, on each fixture. The module is new, or
// replaces one a chain runs, or, on the one-device fixture, one no chain
// runs.
func Test_UpdateModules_MatchesC(t *testing.T) {
	for _, fixture := range fixtures {
		for _, name := range []string{"fresh", "a", "b"} {
			t.Run(fixture.name+", module "+name, func(t *testing.T) {
				memory := fixture.newFunc(t)
				memory.CompareWithC(
					t,
					func() {
						config, err := cblackhole.NewModuleConfig(memory.cAgent, name)
						require.NoError(t, err)
						require.NoError(t, memory.cAgent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}))
					},
					func() {
						module, err := cpconfig.NewBlackhole(memory.owner, name)
						require.NoError(t, err)
						require.NoError(t, updateModules(t, memory, module))
					},
				)
			})
		}
	}
}

// Test_UpdateModules_LinksObjectsAndDevices verifies that a module
// context reaches the objects, runtime storages and devices it names.
//
// Weights and ports shape the device and function tables as well.
func Test_UpdateModules_LinksObjectsAndDevices(t *testing.T) {
	memory := newRichFixture(t)
	module, err := cpconfig.NewBlackhole(memory.owner, "a")
	require.NoError(t, err)
	require.NoError(t, updateModules(t, memory, module))
	published := memory.Instance().Cp.CpConfigGen.Get()
	root := unsafe.Slice(published.ConfigGenEctxs.Get(), published.ConfigGenEctxCount)[0].Get()

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
	heavy := unsafe.Slice(function.ChainPtrs.Get(), function.ChainCount)[0].Get()
	linked := unsafe.Slice(heavy.ModulePtrs.Get(), heavy.Length)[1].Get()
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
	config, err := cblackhole.NewModuleConfig(memory.cAgent, "late")
	require.NoError(t, err)
	module := (*layout.CpModule)(config.AsFFIModule().AsRawPtr())
	// The published configurations keep the superseded agent alive.
	_, err = memory.Harness.SharedMemory().AgentAttach("owner", 0, 2*datasize.MB)
	require.NoError(t, err)
	before := sharedState(memory, module)

	err = updateModules(t, memory, module)

	require.ErrorIs(t, err, cpconfig.ErrSupersededOwner)
	requireLeftAsFound(t, before, memory, module)
}

// Test_UpdateModules_RefusesRepeatedModule verifies that an update naming
// one module twice is refused, publishing nothing and taking nothing.
func Test_UpdateModules_RefusesRepeatedModule(t *testing.T) {
	memory := newPublishedFixture(t)
	first, err := cpconfig.NewBlackhole(memory.owner, "twice")
	require.NoError(t, err)
	second, err := cpconfig.NewBlackhole(memory.owner, "twice")
	require.NoError(t, err)
	before := sharedState(memory, first, second)

	err = updateModules(t, memory, first, second)

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
		// module returns the configuration the update publishes.
		module func(t *testing.T, memory *publishedFixture) *layout.CpModule
	}{
		{"configuration of a new name", func(t *testing.T, memory *publishedFixture) *layout.CpModule {
			module, err := cpconfig.NewBlackhole(memory.owner, "fresh")
			require.NoError(t, err)
			return module
		}},
		{"fresh configuration", func(t *testing.T, memory *publishedFixture) *layout.CpModule {
			module, err := cpconfig.NewBlackhole(memory.owner, "a")
			require.NoError(t, err)
			return module
		}},
		{"configuration carrying names", func(t *testing.T, memory *publishedFixture) *layout.CpModule {
			publishCarried(t, memory)
			module, err := cpconfig.NewBlackhole(memory.owner, "carried")
			require.NoError(t, err)
			return module
		}},
		{"configuration with a runtime registry", func(t *testing.T, memory *publishedFixture) *layout.CpModule {
			module, err := cpconfig.NewBlackhole(memory.owner, "a")
			require.NoError(t, err)
			require.NoError(t, coracle.ModuleRegisterCounter(unsafe.Pointer(module), "rules", "hits", 4))
			return module
		}},
		{"published configuration", func(t *testing.T, memory *publishedFixture) *layout.CpModule {
			return publishedModule(t, memory.Instance().Cp.CpConfigGen.Get(), "a")
		}},
	}
	for _, kind := range modules {
		t.Run(kind.name, func(t *testing.T) {
			failures := map[string]bool{}
			for _, budget := range exhaustionBudgets {
				t.Run(budget.name, func(t *testing.T) {
					memory := newPublishedFixture(t)
					instance := memory.Instance()
					module := kind.module(t, memory)
					nativetest.LeaveOnly(t, &instance.Cp.BlockAllocator, requestsFor(budget.blocks)...)
					before := sharedState(memory, module)

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
			module, err := cpconfig.NewBlackhole(memory.owner, "a")
			require.NoError(t, err)
			kept := copyRequests(instance.Cp.CpConfigGen.Get())[:2+2*failing]
			nativetest.LeaveOnly(t, &instance.Cp.BlockAllocator, kept...)
			before := sharedState(memory, module)

			err = updateModules(t, memory, module)

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
			before := sharedState(memory, module)

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
				require.NoError(t, coracle.ModuleRegisterCounter(unsafe.Pointer(module), "rules", "hits", 4))
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
			module := tc.module(t, memory)
			published := instance.Cp.CpConfigGen.Get()
			devices := uintptr(published.DeviceRegistry.EntityRegistry.Registry.Capacity)
			objects := uintptr(published.ObjectRegistry.EntityRegistry.Registry.Capacity)
			kept := append(copyRequests(published), tc.kept(devices, objects)...)
			nativetest.LeaveOnly(t, &instance.Cp.BlockAllocator, kept...)
			before := sharedState(memory, module)

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
			instance := memory.Instance()
			cp := instance.Cp
			for _, name := range []string{"d", "e", "f", "g", "h"} {
				config, err := cblackhole.NewModuleConfig(memory.cAgent, name)
				require.NoError(t, err)
				require.NoError(t, memory.cAgent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}))
			}
			published := cp.CpConfigGen.Get()
			modules := &published.ModuleRegistry.EntityRegistry.Registry
			require.Equal(t, uint64(8), modules.Capacity)
			module, err := cpconfig.NewBlackhole(memory.owner, "fresh")
			require.NoError(t, err)
			nativetest.LeaveOnly(t, &cp.BlockAllocator, append(copyRequests(published), tc.extra...)...)
			before := sharedState(memory, module)

			err = updateModules(t, memory, module)

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
	module, err := cpconfig.NewBlackhole(memory.owner, "a")
	require.NoError(t, err)
	pinned, err := instance.Pin()
	require.NoError(t, err)
	t.Cleanup(pinned.Release)
	old := pinned.Gen

	done := make(chan error, 1)
	go func() { done <- cpconfig.UpdateModules(instance, []*layout.CpModule{module}) }()
	published := awaitGeneration(t, memory, old, done)

	workers := unsafe.Slice(instance.Dp.Workers.Get(), instance.Dp.WorkerCount)
	roots := unsafe.Slice(published.ConfigGenEctxs.Get(), published.ConfigGenEctxCount)
	for idx := range workers {
		require.Equal(t, uint64(2), atomic.LoadUint64(&old.Refcnt), "retired before worker %d acknowledged", idx)
		select {
		case err := <-done:
			t.Fatalf("the publication returned before worker %d acknowledged: %v", idx, err)
		default:
		}
		worker := workers[idx].Get()
		worker.ConfigGenEctx.Store(roots[idx].Get())
		atomic.StoreUint64(&worker.Gen, published.Gen)
	}
	require.NoError(t, awaitPublication(t, memory, done))
	require.Equal(t, uint64(1), atomic.LoadUint64(&old.Refcnt))
}

// awaitGeneration returns the generation a running publication publishes
// in place of the replaced one, failing the test otherwise.
//
// The test fails if the publication returns first or publishes nothing
// within the bound.
func awaitGeneration(t *testing.T, memory *publishedFixture, old *layout.CpConfigGen, done <-chan error) *layout.CpConfigGen {
	t.Helper()
	deadline := time.Now().Add(publishBound)
	for {
		select {
		case err := <-done:
			t.Fatalf("the publication returned before publishing: %v", err)
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
	module, err := cpconfig.NewBlackhole(memory.owner, "a")
	require.NoError(t, err)
	_, err = cpconfig.LinkObject(module, "ring", "absent")
	require.NoError(t, err)
	before := sharedState(memory, module)

	err = updateModules(t, memory, module)

	require.ErrorIs(t, err, cpconfig.ErrMissingObject)
	requireLeftAsFound(t, before, memory, module)
}

// publishCarried publishes through C a blackhole configuration "carried"
// whose extra counters a replacement lacks.
//
// The replacement carries their names and grows its counter registry on
// its agent.
func publishCarried(t *testing.T, memory *publishedFixture) {
	t.Helper()
	config, err := cblackhole.NewModuleConfig(memory.cAgent, "carried")
	require.NoError(t, err)
	for idx := range 12 {
		require.NoError(t, coracle.ModuleRegisterCounter(
			config.AsFFIModule().AsRawPtr(), "", "extra"+strconv.Itoa(idx), 1,
		))
	}
	require.NoError(t, memory.cAgent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}))
}

// Test_UpdateModules_AgentMemoryFailureFreesCopy verifies that an update
// whose counter link exhausts its agent's memory fails at the link.
//
// The configuration carries twelve names of the one it replaces, so its
// names and their index grow twice. The agent keeps no block, or enough
// for the first growths only, so the link fails at its first block or
// after it took some. The zone's blocks and every reference go back, the
// module keeps what its link grew until it is destroyed, and the
// exhaustion is reported.
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
			module, err := cpconfig.NewBlackhole(memory.owner, "carried")
			require.NoError(t, err)
			nativetest.LeaveOnly(t, &memory.owner.Agent.BlockAllocator, requestsFor(budget.blocks)...)
			before := sharedState(memory, module)

			err = updateModules(t, memory, module)

			require.ErrorIs(t, err, shmalloc.ErrNoMemory)
			require.ErrorContains(t, err, "link counter registry")
			requireLeftAsFound(t, before, memory, module)
		})
	}
}

// newBlackhole returns a blackhole configuration of the name with extra
// own counters of one value each.
func newBlackhole(t *testing.T, memory *publishedFixture, name string, extra ...string) *layout.CpModule {
	t.Helper()
	module, err := cpconfig.NewBlackhole(memory.owner, name)
	require.NoError(t, err)
	for _, counter := range extra {
		require.NoError(t, coracle.ModuleRegisterCounter(unsafe.Pointer(module), "", counter, 1))
	}
	return module
}

// deleteModule deletes the blackhole configuration natively, delivering
// the contexts to the in-process workers, within the publication bound.
func deleteModule(t *testing.T, memory *publishedFixture, name string) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- cpconfig.DeleteModule(memory.Instance(), "blackhole", name, cpconfig.WithDelivery(nativetest.Deliver))
	}()
	return awaitPublication(t, memory, done)
}

// Test_DeleteModule_MatchesC verifies that deleting a published module
// configuration no chain runs from Go leaves the storage as
// cp_config_delete_module does.
//
// Its registry slot is emptied, its owner's loaded count dropped and every
// worker's contexts rebuilt without it.
func Test_DeleteModule_MatchesC(t *testing.T) {
	memory := newPublishedFixture(t)

	memory.CompareWithC(
		t,
		func() { require.NoError(t, memory.cAgent.DeleteModuleConfig("blackhole", "b")) },
		func() { require.NoError(t, deleteModule(t, memory, "b")) },
	)
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
			before := sharedState[*layout.CpModule](memory)

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

	replacement, err := cpconfig.NewBlackhole(memory.owner, "b")
	require.NoError(t, err)
	require.NoError(t, updateModules(t, memory, replacement))
	require.NoError(t, deleteModule(t, memory, "b"))
	require.NoError(t, cpconfig.FreeBlackhole(replacement))

	require.Equal(t, free, shmalloc.FreeSize(&owner.BlockAllocator), "the agent's free bytes")
	require.Equal(t, loaded, atomic.LoadUint64(&owner.LoadedEntityCount), "the agent's loaded count")
	require.ErrorIs(t, deleteModule(t, memory, "b"), cpconfig.ErrNotFound)
}

// Benchmark_UpdateModules_Blackhole measures building a blackhole
// configuration and publishing it, through cgo and natively.
//
// Both deliver the workers' contexts on the publishing thread, and each
// iteration frees through C the configuration its publication replaced,
// so the agent's memory lasts the run.
func Benchmark_UpdateModules_Blackhole(b *testing.B) {
	b.Run("cgo", func(b *testing.B) {
		b.ReportAllocs()
		memory := newPublishedFixture(b)
		replaced := publishedModule(b, memory.Instance().Cp.CpConfigGen.Get(), "a")
		for b.Loop() {
			config, err := cblackhole.NewModuleConfig(memory.cAgent, "a")
			require.NoError(b, err)
			require.NoError(b, memory.cAgent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}))
			coracle.ModuleDestroy(unsafe.Pointer(replaced))
			replaced = (*layout.CpModule)(config.AsFFIModule().AsRawPtr())
		}
	})
	b.Run("native", func(b *testing.B) {
		b.ReportAllocs()
		memory := newPublishedFixture(b)
		replaced := publishedModule(b, memory.Instance().Cp.CpConfigGen.Get(), "a")
		deliver := cpconfig.WithDelivery(nativetest.Deliver)
		for b.Loop() {
			module, err := cpconfig.NewBlackhole(memory.owner, "a")
			require.NoError(b, err)
			require.NoError(b, cpconfig.UpdateModules(memory.Instance(), []*layout.CpModule{module}, deliver))
			coracle.ModuleDestroy(unsafe.Pointer(replaced))
			replaced = module
		}
	})
}
