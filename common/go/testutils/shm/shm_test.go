package testshm_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	testshm "github.com/yanet-platform/yanet2/common/go/testutils/shm"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// Test_Storage_Empty verifies that the generic fixture publishes no module or
// object types, workers, or active configuration by default.
func Test_Storage_Empty(t *testing.T) {
	path := testshm.NewStorage(t, testshm.StorageTypes{})
	memory, err := ffi.AttachSharedMemory(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, memory.Detach()) })
	config := memory.DPConfig(0)
	require.Empty(t, config.Modules())
	require.Empty(t, testshm.LoadedObjectTypes(memory))
	require.Zero(t, config.WorkerCount())
	require.Empty(t, config.CPConfigs())
	require.Empty(t, config.Functions())
	require.Empty(t, config.Pipelines())
}

// Test_Storage_MarkInstanceReady_RejectsOutOfRange verifies that the fixture
// refuses to publish readiness for an unallocated instance.
func Test_Storage_MarkInstanceReady_RejectsOutOfRange(t *testing.T) {
	readyInstanceCount := uint32(0)
	path := testshm.NewStorage(t, testshm.StorageTypes{
		InstanceCount:      2,
		ReadyInstanceCount: &readyInstanceCount,
	})
	memory, err := ffi.AttachSharedMemory(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, memory.Detach()) })

	require.ErrorContains(
		t,
		testshm.MarkInstanceReady(memory, 2),
		"out of range [0, 2)",
	)
	require.False(t, memory.DataplaneReady(1))
}

// Test_Storage_LoadersRejectNULNames verifies that type names with embedded NUL
// bytes are rejected before they cross the C string boundary.
func Test_Storage_LoadersRejectNULNames(t *testing.T) {
	path := testshm.NewStorage(t, testshm.StorageTypes{})
	memory, err := ffi.AttachSharedMemory(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, memory.Detach()) })

	require.ErrorContains(t, testshm.LoadModule(memory, "module\x00suffix"), "NUL")
	require.ErrorContains(t, testshm.LoadObject(memory, "object\x00suffix"), "NUL")
}
