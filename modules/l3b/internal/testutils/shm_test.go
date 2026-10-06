package testutils_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	testshm "github.com/yanet-platform/yanet2/common/go/testutils/shm"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	l3btestutils "github.com/yanet-platform/yanet2/modules/l3b/internal/testutils"
)

// Test_Storage_L3BInventory verifies that production loaders admit exactly one
// module and two inert object types without workers or activated configuration.
func Test_Storage_L3BInventory(t *testing.T) {
	path := l3btestutils.NewStorage(t)
	memory, err := ffi.AttachSharedMemory(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, memory.Detach()) })
	config := memory.DPConfig(0)
	modules := config.Modules()
	require.Len(t, modules, 1)
	require.Equal(t, "l3b", modules[0].Name())
	require.ElementsMatch(t, []string{"l3b_virtual_service", "l3b_session_table"}, testshm.LoadedObjectTypes(memory))
	require.Zero(t, config.WorkerCount())
	require.Empty(t, config.CPConfigs())
	require.Empty(t, config.Functions())
	require.Empty(t, config.Pipelines())
}

// Test_Storage_LoadersRegisterExistingTypes verifies that the loader wrappers
// register valid module and object types in an empty fixture.
func Test_Storage_LoadersRegisterExistingTypes(t *testing.T) {
	_ = l3btestutils.NewStorage(t)
	path := testshm.NewStorage(t, testshm.StorageTypes{})
	memory, err := ffi.AttachSharedMemory(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, memory.Detach()) })

	require.NoError(t, testshm.LoadModule(memory, "l3b"))
	require.NoError(t, testshm.LoadObject(memory, "l3b_virtual_service"))
	require.NoError(t, testshm.LoadObject(memory, "l3b_session_table"))

	modules := memory.DPConfig(0).Modules()
	require.Len(t, modules, 1)
	require.Equal(t, "l3b", modules[0].Name())
	require.ElementsMatch(
		t,
		[]string{"l3b_virtual_service", "l3b_session_table"},
		testshm.LoadedObjectTypes(memory),
	)
}

// Test_Storage_MissingTypePreservesInventory verifies that unresolved dynamic
// symbols fail without adding, removing or replacing existing type entries.
func Test_Storage_MissingTypePreservesInventory(t *testing.T) {
	for _, tc := range []struct {
		name string
		load func(*ffi.SharedMemory) error
	}{
		{
			name: "missing module",
			load: func(memory *ffi.SharedMemory) error {
				return testshm.LoadModule(memory, "missing_l3b_admission")
			},
		},
		{
			name: "missing object",
			load: func(memory *ffi.SharedMemory) error {
				return testshm.LoadObject(memory, "missing_l3b_admission")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := l3btestutils.NewStorage(t)
			memory, err := ffi.AttachSharedMemory(path)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, memory.Detach()) })
			modules := memory.DPConfig(0).Modules()
			objects := testshm.LoadedObjectTypes(memory)
			require.ErrorContains(t, tc.load(memory), "missing_l3b_admission")
			require.Equal(t, modules, memory.DPConfig(0).Modules())
			require.Equal(t, objects, testshm.LoadedObjectTypes(memory))
		})
	}
}
