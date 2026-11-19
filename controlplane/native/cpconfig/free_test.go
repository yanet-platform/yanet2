//go:build linux && (amd64 || arm64)

package cpconfig_test

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
)

// Test_ReleaseGeneration_FreesAGenerationRetiredWhilePinned verifies that
// releasing the pin of a generation a publication retired meanwhile
// frees the generation, and with it its hold on the module configuration
// it published.
func Test_ReleaseGeneration_FreesAGenerationRetiredWhilePinned(t *testing.T) {
	memory := newPublishedFixture(t)
	instance := memory.Instance()
	deliver := cpconfig.WithDelivery(nativetest.Deliver)
	module, err := cpconfig.NewBlackhole(memory.owner, "d")
	require.NoError(t, err)
	require.NoError(t, cpconfig.UpdateModules(instance, []*layout.CpModule{module}, deliver))

	pinned := instance.Pin()
	replacement, err := cpconfig.NewBlackhole(memory.owner, "d")
	require.NoError(t, err)
	require.NoError(t, cpconfig.UpdateModules(instance, []*layout.CpModule{replacement}, deliver))
	size := unsafe.Sizeof(layout.CpModule{})
	require.ErrorIs(t, cpconfig.DestroyModule(module, size, nil), cpconfig.ErrStillReferenced,
		"the pinned generation holds the replaced module",
	)

	cpconfig.ReleaseGeneration(instance.Cp, pinned)
	require.Nil(t, pinned.Gen)
	require.NoError(t, cpconfig.DestroyModule(module, size, nil))
}
