//go:build linux && (amd64 || arm64)

package cpconfig_test

import (
	"strings"
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

// invalidName is a value a C character field cannot hold whole.
type invalidName struct {
	name  string
	value string
}

// invalidNames returns the values a C character field of that size cannot
// hold whole: one without room for its terminator and one with an inner
// NUL.
func invalidNames(size int) []invalidName {
	return []invalidName{
		{"value without room for its terminator", strings.Repeat("n", size)},
		{"value with an inner NUL", "probe\x00other"},
	}
}

// identity is an entity's type and name, as a test case names them.
type identity struct {
	name       string
	entityType string
	entityName string
}

// invalidIdentities returns the types and names fields of those sizes cannot
// hold whole, each paired with a valid other half.
func invalidIdentities(typeSize, nameSize int, validType, validName string) []identity {
	var cases []identity
	for _, invalid := range invalidNames(typeSize) {
		cases = append(cases, identity{"type: " + invalid.name, invalid.value, validName})
	}
	for _, invalid := range invalidNames(nameSize) {
		cases = append(cases, identity{"name: " + invalid.name, validType, invalid.value})
	}
	return cases
}

// Test_NewBlackhole_RefusesNameItsFieldCannotHold verifies that a name C
// would cut short is refused before the agent is touched.
//
// The owner has no agent, so any allocation from it would fault.
func Test_NewBlackhole_RefusesNameItsFieldCannotHold(t *testing.T) {
	for _, tc := range invalidNames(len(layout.CpModule{}.Name)) {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cpconfig.NewBlackhole(&agent.Agent{}, tc.value)
			require.ErrorIs(t, err, cpconfig.ErrInvalidName)
		})
	}
}

// Test_LinkDevice_RefusesNameItsFieldCannotHold verifies that a device
// name C would cut short is refused before the module is touched.
//
// The module has no memory context, so any allocation from it would
// fault.
func Test_LinkDevice_RefusesNameItsFieldCannotHold(t *testing.T) {
	for _, tc := range invalidNames(len(layout.CpModuleDevice{}.Name)) {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cpconfig.LinkDevice(&layout.CpModule{}, tc.value)
			require.ErrorIs(t, err, cpconfig.ErrInvalidName)
		})
	}
}

// moduleMemory is what the memory of a test's module holds before its
// arena.
type moduleMemory struct {
	Allocator layout.BlockAllocator
	Context   layout.MemoryContext
	Module    layout.CpModule
}

// newModuleMemory returns a module header whose memory context allocates
// from a 64 KiB arena, all in one block of Go memory that the relative
// pointers stay within.
func newModuleMemory() *moduleMemory {
	backing := make([]byte, 64<<10)
	memory := (*moduleMemory)(unsafe.Pointer(&backing[0]))
	arena := unsafe.Sizeof(*memory)
	shmalloc.Init(&memory.Allocator)
	shmalloc.PutArena(&memory.Allocator, unsafe.Pointer(&backing[arena]), uintptr(len(backing))-arena)
	shmalloc.ContextInit(&memory.Context, "agent", &memory.Allocator)
	shm.CopyString(memory.Module.Type[:], "blackhole")
	shm.CopyString(memory.Module.Name[:], "a")
	shmalloc.ContextInitFrom(&memory.Module.Entity.MemoryContext, &memory.Context, "a")
	return memory
}

// registryTags returns the tags of a module's runtime counter registries,
// in order.
func registryTags(module *layout.CpModule) []string {
	var tags []string
	entries := registry.Slice(module.RuntimeCounterRegistries.Get(), module.RuntimeCounterRegistryCount)
	for idx := range entries {
		tags = append(tags, shm.String(entries[idx].Get().Tag[:]))
	}
	return tags
}

// Test_ModuleCounterRegistry_AddsAndFindsTags verifies that a new tag
// adds an empty registry after the others and a known tag returns its
// registry and index, the registries keeping their counters as more are
// added.
func Test_ModuleCounterRegistry_AddsAndFindsTags(t *testing.T) {
	memory := newModuleMemory()
	module := &memory.Module

	rules, index, err := cpconfig.ModuleCounterRegistry(module, "rules")
	require.NoError(t, err)
	require.Zero(t, index)
	require.Zero(t, rules.Count, "a new registry is empty")
	_, err = cpconfig.RegisterCounter(rules, "hits", 4)
	require.NoError(t, err)
	routes, index, err := cpconfig.ModuleCounterRegistry(module, "routes")
	require.NoError(t, err)
	require.Equal(t, uint64(1), index)
	again, index, err := cpconfig.ModuleCounterRegistry(module, "rules")
	require.NoError(t, err)

	require.Zero(t, index)
	require.Same(t, rules, again, "a known tag returns its registry")
	require.NotSame(t, rules, routes)
	require.Equal(t, []string{"rules", "routes"}, registryTags(module))
	require.Equal(t, uint64(1), again.Count, "the registry keeps its counter")
}

// Test_ModuleCounterRegistry_ExhaustedLeavesRegistries verifies that a
// registry the module's memory has no room for is refused as exhausted,
// leaving the module's registries and the memory's free bytes as they
// were.
func Test_ModuleCounterRegistry_ExhaustedLeavesRegistries(t *testing.T) {
	cases := []struct {
		name string
		// kept is the requests the memory serves.
		kept []uintptr
	}{
		{"no room for the registry", nil},
		{"no room for the grown pointer array", []uintptr{unsafe.Sizeof(layout.CpModuleCounterRegistry{})}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := newModuleMemory()
			module := &memory.Module
			_, _, err := cpconfig.ModuleCounterRegistry(module, "rules")
			require.NoError(t, err)
			pointers := module.RuntimeCounterRegistries.Get()
			nativetest.LeaveOnly(t, &memory.Allocator, tc.kept...)
			free := shmalloc.FreeSize(&memory.Allocator)

			_, _, err = cpconfig.ModuleCounterRegistry(module, "routes")

			require.ErrorIs(t, err, shmalloc.ErrNoMemory)
			require.Same(t, pointers, module.RuntimeCounterRegistries.Get(), "the pointer array stays")
			require.Equal(t, []string{"rules"}, registryTags(module))
			require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the memory's free bytes")
		})
	}
}

// Test_ModuleCounterRegistry_FreedWithModule verifies that finalising a
// module frees its runtime registries with their counters, giving its
// memory every byte back.
func Test_ModuleCounterRegistry_FreedWithModule(t *testing.T) {
	memory := newModuleMemory()
	free := shmalloc.FreeSize(&memory.Allocator)
	for _, tag := range []string{"rules", "routes", "states"} {
		counters, _, err := cpconfig.ModuleCounterRegistry(&memory.Module, tag)
		require.NoError(t, err)
		_, err = cpconfig.RegisterCounter(counters, "hits", 2)
		require.NoError(t, err)
	}

	cpconfig.FiniModule(&memory.Module)

	require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator))
}

// Test_ModuleCounterRegistry_RefusesTagItsFieldCannotHold verifies that a
// tag C would cut short is refused before the module is touched.
//
// The module has no memory context, so any allocation from it would
// fault.
func Test_ModuleCounterRegistry_RefusesTagItsFieldCannotHold(t *testing.T) {
	for _, tc := range invalidNames(len(layout.CpModuleCounterRegistry{}.Tag)) {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := cpconfig.ModuleCounterRegistry(&layout.CpModule{}, tc.value)
			require.ErrorIs(t, err, cpconfig.ErrInvalidName)
		})
	}
}
