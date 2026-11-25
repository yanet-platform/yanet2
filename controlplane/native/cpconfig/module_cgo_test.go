//go:build linux && (amd64 || arm64) && cgo

package cpconfig_test

import (
	"errors"
	"strconv"
	"testing"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/coracle"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
	"github.com/yanet-platform/yanet2/modules/blackhole/bindings/go/cblackhole"
)

// attachOwner attaches an agent of that name through the cgo control
// plane and returns its cgo handle and its native view.
func attachOwner(t testing.TB, memory *nativetest.Fixture, name string) (*ffi.Agent, *agent.Agent) {
	t.Helper()
	attached, err := memory.Harness.SharedMemory().AgentAttach(name, 0, 16*datasize.MB)
	require.NoError(t, err)
	return attached, &agent.Agent{Instance: memory.Instance(), Agent: (*layout.Agent)(attached.AsRawPtr())}
}

// Test_NewBlackhole_MatchesC verifies that blackhole configurations built
// from Go leave the storage as building them from C does.
//
// The build covers the header, the counter registry and its index, the
// memory context and the device link.
func Test_NewBlackhole_MatchesC(t *testing.T) {
	for _, count := range []int{1, 20} {
		t.Run(strconv.Itoa(count)+" configurations", func(t *testing.T) {
			memory := nativetest.NewFixture(t, dataplaneut.Config{WorkerCount: 1, Modules: []string{"blackhole"}})
			cAgent, owner := attachOwner(t, memory, "owner")

			memory.CompareWithC(
				t,
				func() {
					for idx := range count {
						_, err := cblackhole.NewModuleConfig(cAgent, "blackhole"+strconv.Itoa(idx))
						require.NoError(t, err)
					}
				},
				func() {
					for idx := range count {
						_, err := cpconfig.NewBlackhole(owner, "blackhole"+strconv.Itoa(idx))
						require.NoError(t, err)
					}
				},
			)
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
			memory := nativetest.NewFixture(t, dataplaneut.Config{WorkerCount: 1, Modules: tc.modules})
			_, owner := attachOwner(t, memory, "owner")
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

// Test_FreeBlackhole_MatchesC verifies that freeing an unpublished
// blackhole configuration from Go leaves the storage as C's typed free
// does.
//
// A configuration that links objects and devices and counts into a
// runtime registry gives back every block of them.
func Test_FreeBlackhole_MatchesC(t *testing.T) {
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
			config, err := cblackhole.NewModuleConfig(memory.cAgent, "r")
			require.NoError(t, err)
			module := (*layout.CpModule)(config.AsFFIModule().AsRawPtr())
			if tc.links {
				_, err = cpconfig.LinkObject(module, "ring", "state")
				require.NoError(t, err)
				_, err = cpconfig.LinkDevice(module, "port0")
				require.NoError(t, err)
				require.NoError(t, coracle.ModuleRegisterCounter(unsafe.Pointer(module), "rules", "hits", 4))
			}

			memory.CompareWithC(
				t,
				func() { require.NoError(t, config.Free()) },
				func() { require.NoError(t, cpconfig.FreeBlackhole(module)) },
			)
		})
	}
}

// Test_FreeBlackhole_ReleaseThenFreeMatchesC verifies that a blackhole
// configuration only a pinned retired generation holds is refused until
// the pin is released, and then freed as C frees it.
func Test_FreeBlackhole_ReleaseThenFreeMatchesC(t *testing.T) {
	memory := newPublishedFixture(t)
	instance := memory.Instance()
	config, err := cblackhole.NewModuleConfig(memory.cAgent, "r")
	require.NoError(t, err)
	require.NoError(t, memory.cAgent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}))
	pinned, err := instance.Pin()
	require.NoError(t, err)
	replacement, err := cblackhole.NewModuleConfig(memory.cAgent, "r")
	require.NoError(t, err)
	require.NoError(t, memory.cAgent.UpdateModules([]ffi.ModuleConfig{replacement.AsFFIModule()}))
	module := (*layout.CpModule)(config.AsFFIModule().AsRawPtr())
	require.ErrorIs(t, cpconfig.FreeBlackhole(module), cpconfig.ErrStillReferenced)

	memory.CompareWithC(
		t,
		func() {
			coracle.GenRelease(unsafe.Pointer(instance.Cp), unsafe.Pointer(pinned.Gen))
			require.NoError(t, config.Free())
		},
		func() {
			pinned.Release()
			require.NoError(t, cpconfig.FreeBlackhole(module))
		},
	)
}

// Test_LinkDevice_MatchesC verifies that linking devices to a module from
// Go leaves the storage and the device indexes as cp_module_link_device
// does.
//
// The device named "" every module links comes first, and linking a
// device a second time returns its first index.
func Test_LinkDevice_MatchesC(t *testing.T) {
	memory := nativetest.NewFixture(t, dataplaneut.Config{WorkerCount: 1, Modules: []string{"blackhole"}})
	cAgent, owner := attachOwner(t, memory, "owner")
	devices := []string{"port0", "virt", "port0", ""}
	var cIndexes, goIndexes []uint64

	memory.CompareWithC(
		t,
		func() {
			config, err := cblackhole.NewModuleConfig(cAgent, "linking")
			require.NoError(t, err)
			for _, device := range devices {
				index, err := coracle.ModuleLinkDevice(config.AsFFIModule().AsRawPtr(), device)
				require.NoError(t, err)
				cIndexes = append(cIndexes, index)
			}
		},
		func() {
			module, err := cpconfig.NewBlackhole(owner, "linking")
			require.NoError(t, err)
			for _, device := range devices {
				index, err := cpconfig.LinkDevice(module, device)
				require.NoError(t, err)
				goIndexes = append(goIndexes, index)
			}
		},
	)
	require.Equal(t, []uint64{1, 2, 1, 0}, cIndexes)
	require.Equal(t, cIndexes, goIndexes)
}

// Test_LinkDevice_AgentMemoryFailureKeepsLinks verifies that a link the
// agent has no memory for fails, leaving the module's devices and the
// agent's memory as they were.
func Test_LinkDevice_AgentMemoryFailureKeepsLinks(t *testing.T) {
	memory := nativetest.NewFixture(t, dataplaneut.Config{WorkerCount: 1, Modules: []string{"blackhole"}})
	_, owner := attachOwner(t, memory, "owner")
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
