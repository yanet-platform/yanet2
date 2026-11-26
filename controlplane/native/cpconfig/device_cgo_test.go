//go:build linux && (amd64 || arm64) && cgo

package cpconfig_test

import (
	"fmt"
	"regexp"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// updateDevices publishes the devices natively on the fixture.
func updateDevices(t *testing.T, memory *publishedFixture, devices ...*layout.CpDevice) error {
	t.Helper()
	return publishNatively(t, memory, func(options ...cpconfig.UpdateOption) error {
		return cpconfig.UpdateDevices(memory.Instance(), devices, options...)
	})
}

// newPlainDevice returns a plain device of the agent whose input runs the
// pipeline drop at that weight and whose output runs dummy.
func newPlainDevice(t *testing.T, owner *agent.Agent, name string, weight uint64) *layout.CpDevice {
	t.Helper()
	device, err := cpconfig.NewPlainDevice(owner, cpconfig.DeviceConfig{
		Name:   name,
		Input:  []cpconfig.DevicePipelineConfig{{Name: "drop", Weight: weight}},
		Output: []cpconfig.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	})
	require.NoError(t, err)
	return device
}

// destroyDevice finalises a device and returns its block to its agent,
// as the typed free does past its destroy gate.
func destroyDevice(device *layout.CpDevice) {
	owner := device.Entity.Agent.Get()
	cpconfig.FiniDevice(device)
	registry.FreeNode(&owner.MemoryContext, device, 0)
}

// deviceContext returns the context of the device of a name in the first
// worker's tree of the published generation.
func deviceContext(t *testing.T, memory *publishedFixture, name string) *layout.DeviceEctx {
	t.Helper()
	published := memory.Instance().Cp.CpConfigGen.Get()
	root := unsafe.Slice(published.ConfigGenEctxs.Get(), published.ConfigGenEctxCount)[0].Get()
	for device := range registry.Pointees(registry.Slice(root.DevicePtrs.Get(), root.DeviceCount)) {
		if shm.String(device.CpDevice.Get().Name[:]) == name {
			return device
		}
	}
	t.Fatalf("the published generation runs no device %q", name)
	return nil
}

// Test_UpdateDevices_DataplaneRunsGoDevice verifies that the dataplane
// runs packets through a plain device Go published in place of the one C
// published.
//
// The device's entries hold their weighted pipelines, the input entry's
// context spreads packets over the forty weights of its pipeline, and the
// device's counters continue from the replaced device's.
func Test_UpdateDevices_DataplaneRunsGoDevice(t *testing.T) {
	memory := newPublishedFixture(t)
	_, err := memory.Harness.HandlePackets(icmpPackets(t, 3)...)
	require.NoError(t, err)
	deviceTags := []zone.CounterTag{{Key: "device", Value: "port0"}, {Key: "kind", Value: "device"}}
	before := counterTotal(t, memory, "input_rx", deviceTags...)
	require.Equal(t, uint64(3), before)
	device := newPlainDevice(t, memory.owner, "port0", 40)

	require.NoError(t, updateDevices(t, memory, device))

	input := device.InputPipelines.Get()
	require.Equal(t, uint64(1), input.PipelineCount)
	pipelines := input.Pipelines(1)
	require.Equal(t, "drop", shm.String(pipelines[0].Name[:]))
	require.Equal(t, uint64(40), pipelines[0].Weight)
	require.Equal(t, uint64(1), device.OutputPipelines.Get().PipelineCount)
	require.Same(t, device, deviceContext(t, memory, "port0").CpDevice.Get())
	require.Equal(t, uint64(40), deviceContext(t, memory, "port0").InputPipelines.Get().PipelineMapSize)

	result, err := memory.Harness.HandlePackets(icmpPackets(t, 5)...)
	require.NoError(t, err)
	require.Len(t, result.Drop, 5, "the device's input pipeline drops every packet")
	require.Equal(t, before+5, counterTotal(t, memory, "input_rx", deviceTags...), "device counters continue")
}

// Test_UpdateDevices_ReplacementCarriesCounters verifies that a device
// replacing one of its name continues the counter the replaced device
// registered and it lacks, with the values counted before.
func Test_UpdateDevices_ReplacementCarriesCounters(t *testing.T) {
	memory := newPublishedFixture(t)
	first := newPlainDevice(t, memory.owner, "port0", 1)
	_, err := cpconfig.RegisterCounter(&first.CounterRegistry, "probe", 2)
	require.NoError(t, err)
	require.NoError(t, updateDevices(t, memory, first))
	countAll(memory)

	require.NoError(t, updateDevices(t, memory, newPlainDevice(t, memory.owner, "port0", 1)))

	deviceTags := []zone.CounterTag{{Key: "device", Value: "port0"}, {Key: "kind", Value: "device"}}
	require.Equal(t, uint64(2), counterTotal(t, memory, "probe", deviceTags...), "both workers' values carry over")
}

// Test_NewPlainDevice_AcceptsWeightBound verifies that a pipeline of the
// largest weight a device entry may sum to is accepted.
func Test_NewPlainDevice_AcceptsWeightBound(t *testing.T) {
	memory := newPublishedFixture(t)

	device := newPlainDevice(t, memory.owner, "port0", 65535)

	require.Equal(t, uint64(65535), device.InputPipelines.Get().Pipelines(1)[0].Weight)
}

// Test_UpdateDevices_RefusesMissingPipeline verifies that a device whose
// entry runs an unpublished pipeline is refused by the contexts' build,
// publishing nothing and taking nothing.
func Test_UpdateDevices_RefusesMissingPipeline(t *testing.T) {
	memory := newPublishedFixture(t)
	device, err := cpconfig.NewPlainDevice(memory.owner, cpconfig.DeviceConfig{
		Name:  "port0",
		Input: []cpconfig.DevicePipelineConfig{{Name: "absent", Weight: 1}},
	})
	require.NoError(t, err)
	before := sharedState(memory, device)

	err = updateDevices(t, memory, device)

	require.ErrorIs(t, err, cpconfig.ErrMissingPipeline)
	requireLeftAsFound(t, before, memory, device)
}

// Test_UpdateDevices_RefusesSupersededOwner verifies that a device of a
// superseded agent is refused, publishing nothing and taking nothing.
func Test_UpdateDevices_RefusesSupersededOwner(t *testing.T) {
	memory := newPublishedFixture(t)
	device := newPlainDevice(t, memory.owner, "port0", 1)
	// The published configurations keep the superseded agent alive.
	_, err := memory.Harness.SharedMemory().AgentAttach("owner", 0, 2*datasize.MB)
	require.NoError(t, err)
	before := sharedState(memory, device)

	err = updateDevices(t, memory, device)

	require.ErrorIs(t, err, cpconfig.ErrSupersededOwner)
	requireLeftAsFound(t, before, memory, device)
}

// Test_UpdateDevices_RefusesRepeatedDevice verifies that an update naming
// one device twice, of any types, is refused, publishing nothing and
// taking nothing.
func Test_UpdateDevices_RefusesRepeatedDevice(t *testing.T) {
	memory := newPublishedFixture(t)
	first := newPlainDevice(t, memory.owner, "twice", 1)
	second := newPlainDevice(t, memory.owner, "twice", 1)
	shm.CopyString(second.Type[:], "vlan")
	before := sharedState(memory, first, second)

	err := updateDevices(t, memory, first, second)

	require.ErrorIs(t, err, cpconfig.ErrRepeated)
	requireLeftAsFound(t, before, memory, first, second)
}

// Test_UpdateDevices_ExhaustedZoneFreesUpdate verifies that an update of a
// device the zone runs out of memory for part-way frees every block it
// took from the zone and drops every reference it took.
//
// The budgets together must run out in the copy and in both workers.
func Test_UpdateDevices_ExhaustedZoneFreesUpdate(t *testing.T) {
	failures := map[string]bool{}
	for _, budget := range exhaustionBudgets {
		t.Run(budget.name, func(t *testing.T) {
			memory := newPublishedFixture(t)
			device := newPlainDevice(t, memory.owner, "port0", 40)
			nativetest.LeaveOnly(t, &memory.Instance().Cp.BlockAllocator, requestsFor(budget.blocks)...)
			before := sharedState(memory, device)

			err := updateDevices(t, memory, device)

			require.ErrorIs(t, err, shmalloc.ErrNoMemory)
			requireLeftAsFound(t, before, memory, device)
			failures[failurePlace(t, err)] = true
		})
	}
	require.Equal(t, map[string]bool{"copy": true, "worker 0": true, "worker 1": true}, failures)
}

// Test_UpdateDevices_UpsertFailureFreesCopy verifies that an upsert unable
// to grow a full device registry fails the update and frees the copy.
//
// The published registry holds eight devices, its capacity, so a new one
// needs a larger slot array and index; the zone keeps the copy's blocks,
// then also those of the grown slots.
func Test_UpdateDevices_UpsertFailureFreesCopy(t *testing.T) {
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
			var idle []*layout.CpDevice
			for idx := range 7 {
				idle = append(idle, newPlainDevice(t, memory.owner, fmt.Sprintf("idle%d", idx), 1))
			}
			require.NoError(t, updateDevices(t, memory, idle...))
			published := memory.Instance().Cp.CpConfigGen.Get()
			requireFull(t, &published.DeviceRegistry.EntityRegistry.Registry)
			device := newPlainDevice(t, memory.owner, "ninth", 1)
			nativetest.LeaveOnly(t, &memory.Instance().Cp.BlockAllocator, append(copyRequests(published), tc.extra...)...)
			before := sharedState(memory, device)

			err := updateDevices(t, memory, device)

			require.ErrorIs(t, err, shmalloc.ErrNoMemory)
			require.ErrorContains(t, err, `upsert device "ninth"`)
			requireLeftAsFound(t, before, memory, device)
		})
	}
}

// Test_InitDevice_RefusesUnknownDeviceType verifies that a device of a type
// the dataplane does not load is refused, leaving the agent's memory as
// it was.
func Test_InitDevice_RefusesUnknownDeviceType(t *testing.T) {
	memory := newPublishedFixture(t)
	free := shmalloc.FreeSize(&memory.owner.Agent.BlockAllocator)
	var device layout.CpDevice

	err := cpconfig.InitDevice(&device, memory.owner, "vlan", cpconfig.DeviceConfig{Name: "port0"})

	require.ErrorContains(t, err, `device type "vlan" not found`)
	require.Equal(t, free, shmalloc.FreeSize(&memory.owner.Agent.BlockAllocator), "the agent's free bytes")
}

// plainDeviceStep matches the step of a plain device's build that failed.
var plainDeviceStep = regexp.MustCompile(`allocate plain device|input pipelines|output pipelines|register`)

// Test_NewPlainDevice_ExhaustedAgentFreesEverything verifies that a plain
// device its agent runs out of memory for at any step gives the agent
// back every byte and leaves no memory context.
//
// The agent keeps each prefix of what the build asks for: the device,
// its two entries, then its counter names and their index as fourteen
// counters grow them; the index's last growth takes the block the grown
// names freed.
func Test_NewPlainDevice_ExhaustedAgentFreesEverything(t *testing.T) {
	entry := unsafe.Sizeof(layout.CpDeviceEntry{}) + unsafe.Sizeof(layout.CpDevicePipeline{})
	counter := unsafe.Sizeof(layout.Counter{})
	requests := []uintptr{unsafe.Sizeof(layout.CpDevice{}), entry, entry, 8 * counter, 32, 64, 128, 16 * counter}
	steps := map[string]bool{}
	for end := range len(requests) {
		t.Run(fmt.Sprintf("first %d of %d requests", end, len(requests)), func(t *testing.T) {
			memory := newPublishedFixture(t)
			owner := memory.owner
			nativetest.LeaveOnly(t, &owner.Agent.BlockAllocator, requests[:end]...)
			free := shmalloc.FreeSize(&owner.Agent.BlockAllocator)
			children := owner.Agent.MemoryContext.FirstChild.Get()

			_, err := cpconfig.NewPlainDevice(owner, cpconfig.DeviceConfig{
				Name:  "port0",
				Input: []cpconfig.DevicePipelineConfig{{Name: "drop", Weight: 1}},
			})

			require.ErrorIs(t, err, shmalloc.ErrNoMemory)
			require.Equal(t, free, shmalloc.FreeSize(&owner.Agent.BlockAllocator), "the agent's free bytes")
			require.Same(t, children, owner.Agent.MemoryContext.FirstChild.Get(), "no memory context is left")
			steps[plainDeviceStep.FindString(err.Error())] = true
		})
	}
	require.Equal(t, map[string]bool{
		"allocate plain device": true, "input pipelines": true, "output pipelines": true, "register": true,
	}, steps)
}

// Test_FreePlainDevice_GivesAgentBackEveryByte verifies that freeing an
// unpublished plain device returns every byte its build took from the
// agent and leaves the agent's loaded count as it was.
func Test_FreePlainDevice_GivesAgentBackEveryByte(t *testing.T) {
	memory := newPublishedFixture(t)
	owner := memory.owner.Agent
	free := shmalloc.FreeSize(&owner.BlockAllocator)
	loaded := atomic.LoadUint64(&owner.LoadedEntityCount)
	device := newPlainDevice(t, memory.owner, "port0", 40)

	require.NoError(t, cpconfig.FreePlainDevice(device))

	require.Equal(t, free, shmalloc.FreeSize(&owner.BlockAllocator), "the agent's free bytes")
	require.Equal(t, loaded, atomic.LoadUint64(&owner.LoadedEntityCount), "the agent's loaded count")
}

// Test_FreePlainDevice_RefusesHeldDevice verifies that a device a pinned
// retired generation holds is refused as still referenced, untouched,
// and freed once the pin is released.
func Test_FreePlainDevice_RefusesHeldDevice(t *testing.T) {
	memory := newPublishedFixture(t)
	instance := memory.Instance()
	_, restarting := attachOwner(t, memory.Fixture, "restarting")
	free := shmalloc.FreeSize(&restarting.Agent.BlockAllocator)
	device := newPlainDevice(t, restarting, "port0", 1)
	require.NoError(t, updateDevices(t, memory, device))
	pinned, err := instance.Pin()
	require.NoError(t, err)
	require.NoError(t, updateDevices(t, memory, newPlainDevice(t, memory.owner, "port0", 1)))
	held := shmalloc.FreeSize(&restarting.Agent.BlockAllocator)
	references := atomic.LoadUint64(&device.Entity.ConfigItem.Refcnt)

	require.ErrorIs(t, cpconfig.FreePlainDevice(device), cpconfig.ErrStillReferenced)
	require.Equal(t, held, shmalloc.FreeSize(&restarting.Agent.BlockAllocator), "the agent's free bytes")
	require.Equal(t, references, atomic.LoadUint64(&device.Entity.ConfigItem.Refcnt), "the device's references")

	pinned.Release()
	require.NoError(t, cpconfig.FreePlainDevice(device))
	require.Equal(t, free, shmalloc.FreeSize(&restarting.Agent.BlockAllocator), "the agent's free bytes")
}

// Test_TryDestroyDevice_FreeReclaimsSupersededAgent verifies that a
// device's typed free whose agent an attach superseded during the free
// reclaims the agent when it returns its hold.
func Test_TryDestroyDevice_FreeReclaimsSupersededAgent(t *testing.T) {
	memory := newPublishedFixture(t)
	owner := attachRestarting(t, memory)
	device := newPlainDevice(t, &agent.Agent{Instance: memory.Instance(), Agent: owner}, "port0", 1)
	require.NoError(t, cpconfig.TryDestroyDevice(device))

	attachRestarting(t, memory)
	destroyDevice(device)
	cpconfig.DestroyReleased(owner)

	require.Zero(t, supersededCount(memory, "restarting"))
}

// Test_UpdateDevices_DataplaneRunsGoTopology verifies that a topology Go
// published alone, through an agent Go attached, carries packets through
// the dataplane.
//
// The device's input pipeline runs a function whose chain runs a
// blackhole with a runtime registry; the blackhole drops every packet,
// and the module's and the device's counters count them by their tags.
func Test_UpdateDevices_DataplaneRunsGoTopology(t *testing.T) {
	fixture := nativetest.NewFixture(t, dataplaneut.Config{
		WorkerCount:   2,
		Devices:       []string{"port0"},
		Modules:       []string{"blackhole"},
		DevicesToLoad: []string{"plain"},
	})
	owner, err := agent.Attach(fixture.Instance(), "native", uint64(8*datasize.MB))
	require.NoError(t, err)
	memory := &publishedFixture{Fixture: fixture, owner: owner}
	module, err := cpconfig.NewBlackhole(owner, "sink")
	require.NoError(t, err)
	rules, _, err := cpconfig.ModuleCounterRegistry(module, "rules")
	require.NoError(t, err)
	_, err = cpconfig.RegisterCounter(rules, "hits", 4)
	require.NoError(t, err)
	require.NoError(t, updateModules(t, memory, module))
	require.NoError(t, updateFunctions(t, memory, cpconfig.FunctionConfig{
		Name:   "drop",
		Chains: []cpconfig.FunctionChainConfig{chainOf("sink_chain", 1, "sink")},
	}))
	require.NoError(t, updatePipelines(t, memory,
		cpconfig.PipelineConfig{Name: "drop", Functions: []string{"drop"}},
		cpconfig.PipelineConfig{Name: "dummy"},
	))
	require.NoError(t, updateDevices(t, memory, newPlainDevice(t, owner, "port0", 40)))

	result, err := memory.Harness.HandlePackets(icmpPackets(t, 4)...)

	require.NoError(t, err)
	require.Len(t, result.Drop, 4, "the Go module drops every packet")
	moduleTags := []zone.CounterTag{{Key: "module_name", Value: "sink"}, {Key: "kind", Value: "module"}}
	require.Equal(t, uint64(4), counterTotal(t, memory, "drop", moduleTags...))
	deviceTags := []zone.CounterTag{{Key: "device", Value: "port0"}, {Key: "kind", Value: "device"}}
	require.Equal(t, uint64(4), counterTotal(t, memory, "input_rx", deviceTags...))
	runtimeTags := []zone.CounterTag{{Key: "module_name", Value: "sink"}, {Key: "kind", Value: "runtime"}, {Key: "config", Value: "rules"}}
	require.Zero(t, counterTotal(t, memory, "hits", runtimeTags...), "the runtime registry has a storage")
}
