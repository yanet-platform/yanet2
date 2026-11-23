//go:build linux && (amd64 || arm64)

package zone_test

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
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

	pinned := native.Pin()
	require.Same(t, published, pinned.Gen)
	require.Equal(t, before+1, atomic.LoadUint64(&published.Refcnt))

	pinned.Release()
	require.Nil(t, pinned.Gen)
	require.Equal(t, before, atomic.LoadUint64(&published.Refcnt))

	pinned.Release()
	require.Equal(t, before, atomic.LoadUint64(&published.Refcnt))
}
