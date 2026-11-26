//go:build linux && (amd64 || arm64)

package cpconfig_test

import (
	"errors"
	"strconv"
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
)

// Test_NewBlackhole_BuildsHeaders verifies that blackhole configurations
// built from Go each hold their own header in the agent's memory.
//
// The header names the blackhole module type and the configuration, is
// owned by the agent, holds a memory context under the agent's, an
// indexed counter registry and the link to the device named "".
func Test_NewBlackhole_BuildsHeaders(t *testing.T) {
	for _, count := range []int{1, 20} {
		t.Run(strconv.Itoa(count)+" configurations", func(t *testing.T) {
			memory := newFixture(t, nativetest.Config{Modules: []string{"blackhole"}})

			for idx := range count {
				name := "blackhole" + strconv.Itoa(idx)
				module, err := cpconfig.NewBlackhole(memory.owner, name)
				require.NoError(t, err)

				require.Equal(t, "blackhole", shm.String(module.Type[:]))
				require.Equal(t, name, shm.String(module.Name[:]))
				require.Zero(t, module.DpModuleIdx, "the dataplane's only module type")
				require.Same(t, memory.owner.Agent, module.Entity.Agent.Get())
				require.Same(t, &memory.owner.Agent.MemoryContext, module.Entity.MemoryContext.Parent.Get())
				requireIndexed(t, &module.CounterRegistry)
				devices := registry.Slice(module.Devices.Get(), module.DeviceCount)
				require.Len(t, devices, 1)
				require.Empty(t, shm.String(devices[0].Name[:]))
			}
		})
	}
}

// Test_NewBlackhole_FailureFreesEverything verifies that a blackhole
// configuration that cannot be built gives its agent back every byte.
//
// The header and whatever it took are returned and its memory context is
// unlinked from the agent's.
func Test_NewBlackhole_FailureFreesEverything(t *testing.T) {
	cases := []struct {
		name    string
		modules []string
		// setup leaves the agent's allocator as the case needs it.
		setup func(t *testing.T, owner *agent.Agent)
		// exhausted marks a build the agent's memory ran out for.
		exhausted bool
	}{
		{
			name:    "dataplane without the blackhole module",
			modules: []string{"forward"},
			setup:   func(*testing.T, *agent.Agent) {},
		},
		{
			name:    "agent memory exhausted after the header",
			modules: []string{"blackhole"},
			setup: func(t *testing.T, owner *agent.Agent) {
				nativetest.LeaveOnly(t, &owner.Agent.BlockAllocator, unsafe.Sizeof(layout.CpModule{}))
			},
			exhausted: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			owner := newFixture(t, nativetest.Config{Modules: tc.modules}).owner
			tc.setup(t, owner)
			free := shmalloc.FreeSize(&owner.Agent.BlockAllocator)

			_, err := cpconfig.NewBlackhole(owner, "probe")

			require.Error(t, err)
			require.Equal(t, tc.exhausted, errors.Is(err, shmalloc.ErrNoMemory))
			require.Equal(t, free, shmalloc.FreeSize(&owner.Agent.BlockAllocator), "the agent's free bytes")
			require.True(t, owner.Agent.MemoryContext.FirstChild.IsNil(), "a failed configuration leaves no memory context")
		})
	}
}

// Test_FreeBlackhole_GivesAgentBackEveryByte verifies that freeing an
// unpublished blackhole configuration returns every byte its build took
// from the agent and unlinks its memory context.
//
// A configuration that links objects and devices and counts into a
// runtime registry gives back every block of them.
func Test_FreeBlackhole_GivesAgentBackEveryByte(t *testing.T) {
	cases := []struct {
		name string
		// links is whether the configuration links an object, a device
		// and a runtime registry.
		links bool
	}{
		{"header only", false},
		{"linked objects, devices and runtime counters", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := newPublishedFixture(t)
			owner := memory.owner.Agent
			free := shmalloc.FreeSize(&owner.BlockAllocator)
			children := owner.MemoryContext.FirstChild.Get()
			module := newBlackhole(t, memory, "r")
			if tc.links {
				_, err := cpconfig.LinkObject(module, "ring", "state")
				require.NoError(t, err)
				_, err = cpconfig.LinkDevice(module, "port0")
				require.NoError(t, err)
				registerRuntimeCounter(t, module)
			}

			require.NoError(t, cpconfig.FreeBlackhole(module))

			require.Equal(t, free, shmalloc.FreeSize(&owner.BlockAllocator), "the agent's free bytes")
			require.Same(t, children, owner.MemoryContext.FirstChild.Get(), "the agent's memory contexts")
		})
	}
}

// Test_FreeBlackhole_ReleaseThenFree verifies that a blackhole
// configuration only a pinned retired generation holds is refused until
// the pin is released, and then gives its agent back what its build took.
func Test_FreeBlackhole_ReleaseThenFree(t *testing.T) {
	memory := newPublishedFixture(t)
	instance := memory.Instance()
	owner := memory.owner.Agent
	free := shmalloc.FreeSize(&owner.BlockAllocator)
	module := newBlackhole(t, memory, "r")
	taken := free - shmalloc.FreeSize(&owner.BlockAllocator)
	require.NoError(t, updateModules(t, memory, module))
	pinned, err := instance.Pin()
	require.NoError(t, err)
	require.NoError(t, updateModules(t, memory, newBlackhole(t, memory, "r")))
	require.ErrorIs(t, cpconfig.FreeBlackhole(module), cpconfig.ErrStillReferenced)
	held := shmalloc.FreeSize(&owner.BlockAllocator)

	pinned.Release()
	require.NoError(t, cpconfig.FreeBlackhole(module))

	require.Equal(t, held+taken, shmalloc.FreeSize(&owner.BlockAllocator), "the agent's free bytes")
}

// Test_LinkDevice_IndexesDevices verifies that linking devices to a module
// appends each new name to its devices and returns its index.
//
// The device named "" every module links comes first, and linking a
// device a second time returns its first index.
func Test_LinkDevice_IndexesDevices(t *testing.T) {
	memory := newFixture(t, nativetest.Config{Modules: []string{"blackhole"}})
	module := newBlackhole(t, memory, "linking")
	var indexes []uint64

	for _, device := range []string{"port0", "virt", "port0", ""} {
		index, err := cpconfig.LinkDevice(module, device)
		require.NoError(t, err)
		indexes = append(indexes, index)
	}

	require.Equal(t, []uint64{1, 2, 1, 0}, indexes)
	var names []string
	for _, device := range registry.Slice(module.Devices.Get(), module.DeviceCount) {
		names = append(names, shm.String(device.Name[:]))
	}
	require.Equal(t, []string{"", "port0", "virt"}, names)
}

// Test_LinkDevice_AgentMemoryFailureKeepsLinks verifies that a link the
// agent has no memory for fails, leaving the module's devices and the
// agent's memory as they were.
func Test_LinkDevice_AgentMemoryFailureKeepsLinks(t *testing.T) {
	owner := newFixture(t, nativetest.Config{Modules: []string{"blackhole"}}).owner
	module, err := cpconfig.NewBlackhole(owner, "linking")
	require.NoError(t, err)
	devices := module.Devices.Get()
	nativetest.LeaveOnly(t, &owner.Agent.BlockAllocator)
	free := shmalloc.FreeSize(&owner.Agent.BlockAllocator)

	_, err = cpconfig.LinkDevice(module, "port0")

	require.ErrorIs(t, err, shmalloc.ErrNoMemory)
	require.Same(t, devices, module.Devices.Get(), "the module keeps its devices")
	require.Equal(t, uint64(1), module.DeviceCount)
	require.Equal(t, free, shmalloc.FreeSize(&owner.Agent.BlockAllocator), "the agent's free bytes")
}
