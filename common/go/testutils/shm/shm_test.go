package testshm_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	testshm "github.com/yanet-platform/yanet2/common/go/testutils/shm"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// Test_Storage_L3BInventory verifies that production loaders admit exactly one
// module and two inert object types without workers or activated configuration.
func Test_Storage_L3BInventory(t *testing.T) {
	path := testshm.NewStorage(t)
	memory, err := ffi.AttachSharedMemory(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, memory.Detach()) })
	config := memory.DPConfig(0)
	modules := config.Modules()
	require.Len(t, modules, 1)
	require.Equal(t, "l3b", modules[0].Name())
	require.ElementsMatch(t, []string{"l3b_virtual_service", "l3b_session_table"}, testshm.ObjectTypes(memory))
	require.Zero(t, config.WorkerCount())
	require.Empty(t, config.CPConfigs())
	require.Empty(t, config.Functions())
	require.Empty(t, config.Pipelines())
}

// Test_Storage_MissingTypePreservesInventory verifies that unresolved dynamic
// symbols fail without adding, removing or replacing existing type entries.
func Test_Storage_MissingTypePreservesInventory(t *testing.T) {
	for _, tc := range []struct {
		name   string
		object bool
	}{
		{name: "missing module"},
		{name: "missing object", object: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := testshm.NewStorage(t)
			memory, err := ffi.AttachSharedMemory(path)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, memory.Detach()) })
			modules := memory.DPConfig(0).Modules()
			objects := testshm.ObjectTypes(memory)
			require.ErrorContains(t, testshm.LoadType(memory, "missing_l3b_admission", tc.object), "missing_l3b_admission")
			require.Equal(t, modules, memory.DPConfig(0).Modules())
			require.Equal(t, objects, testshm.ObjectTypes(memory))
		})
	}
}
