//go:build linux && (amd64 || arm64)

package zone_test

import (
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// Test_Instance_Pin_RefusesGenerationOutsideInstance verifies that a
// published generation lying outside its instance is refused before its
// reference count is touched.
func Test_Instance_Pin_RefusesGenerationOutsideInstance(t *testing.T) {
	data, dp := craftedStorage(unsafe.Sizeof(layout.CpConfigGen{}))
	outside := (*layout.CpConfigGen)(unsafe.Pointer(&data[dp.StorageSize]))
	dp.CpConfig.Get().CpConfigGen.Set(outside)
	storage, err := zone.FromMemory(data)
	require.NoError(t, err)

	_, err = storage.Instances[0].Pin()
	require.ErrorContains(t, err, "published generation lies outside the instance")
	require.Zero(t, outside.Refcnt)
}

// Test_Generation_Release_LeavesUnpinnedGenerationAlone verifies that
// releasing a generation no pin produced keeps its references.
func Test_Generation_Release_LeavesUnpinnedGenerationAlone(t *testing.T) {
	gen := &layout.CpConfigGen{Refcnt: 1}
	generation := zone.Generation{Gen: gen}

	generation.Release()

	require.Equal(t, uint64(1), atomic.LoadUint64(&gen.Refcnt))
	require.Nil(t, generation.Gen)
}
