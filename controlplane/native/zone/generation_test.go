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

// Test_Generation_Release_DropsThePin verifies that a pin holds one more
// reference on the published generation, that Release drops it, and that
// a second Release drops nothing more.
func Test_Generation_Release_DropsThePin(t *testing.T) {
	harness, _ := newPublishedHarness(t)
	native := nativeStorage(t, harness).Instances[0]
	published := native.Cp.CpConfigGen.Load()
	require.NotNil(t, published)
	before := atomic.LoadUint64(&published.Refcnt)

	pinned, err := native.Pin()
	require.NoError(t, err)
	require.Same(t, published, pinned.Gen)
	require.Equal(t, before+1, atomic.LoadUint64(&published.Refcnt))

	pinned.Release()
	require.Nil(t, pinned.Gen)
	require.Equal(t, before, atomic.LoadUint64(&published.Refcnt))

	pinned.Release()
	require.Equal(t, before, atomic.LoadUint64(&published.Refcnt))
}

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
