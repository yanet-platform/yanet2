//go:build linux && (amd64 || arm64)

package cpconfig_test

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/coracle"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/modules/blackhole/bindings/go/cblackhole"
)

// replacedModule publishes a blackhole configuration through C, then one
// replacing it, and returns the replaced one, which no live generation
// holds any more.
func replacedModule(t *testing.T, memory *publishedFixture, name string) *cblackhole.ModuleConfig {
	t.Helper()
	var configs [2]*cblackhole.ModuleConfig
	for idx := range configs {
		config, err := cblackhole.NewModuleConfig(memory.cAgent, name)
		require.NoError(t, err)
		require.NoError(t, memory.cAgent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}))
		configs[idx] = config
	}
	return configs[0]
}

// replacedObject publishes a ring object with its counters through C,
// then one replacing it, and returns the replaced one, which no live
// generation holds any more.
func replacedObject(t *testing.T, memory *publishedFixture, name string) unsafe.Pointer {
	t.Helper()
	replaced := cNewObject(t, memory, name)
	require.NoError(t, coracle.UpdateObjects(unsafe.Pointer(memory.owner.Agent), []unsafe.Pointer{replaced}))
	cPublishObject(t, memory, name)
	return replaced
}

// Test_DestroyModule_MatchesC verifies that destroying a replaced module
// configuration from Go leaves the storage as the typed free of C does:
// its counter registry and memory context finished, its block freed and
// the hold on its agent released.
func Test_DestroyModule_MatchesC(t *testing.T) {
	memory := newPublishedFixture(t)
	replaced := replacedModule(t, memory, "d")
	module := (*layout.CpModule)(replaced.AsFFIModule().AsRawPtr())
	memory.CompareWithC(t,
		func() {
			require.NoError(t, replaced.Free())
		},
		func() {
			require.NoError(t, cpconfig.DestroyModule(module, unsafe.Sizeof(layout.CpModule{}), nil))
		},
	)
}

// Test_DestroyModule_RefusesPublishedModule verifies that destroying a
// module configuration the published generation holds is refused as
// still referenced and frees nothing, its body included.
func Test_DestroyModule_RefusesPublishedModule(t *testing.T) {
	memory := newPublishedFixture(t)
	module, err := cpconfig.NewBlackhole(memory.owner, "d")
	require.NoError(t, err)
	require.NoError(t, cpconfig.UpdateModules(memory.Instance(), []*layout.CpModule{module}, cpconfig.WithDelivery(nativetest.Deliver)))
	err = cpconfig.DestroyModule(module, unsafe.Sizeof(layout.CpModule{}), func() {
		t.Fatal("the body of a published module is freed")
	})
	require.ErrorIs(t, err, cpconfig.ErrStillReferenced)
	require.NotNil(t, module.Entity.Agent.Get(), "the published module is finished")
}

// Test_DestroyObject_MatchesC verifies that destroying a replaced object
// from Go leaves the storage as the typed free of C does: its counter
// and link counter registries and its memory context finished, its block
// freed and the hold on its agent released.
func Test_DestroyObject_MatchesC(t *testing.T) {
	memory := newPublishedFixture(t)
	replaced := replacedObject(t, memory, "state")
	memory.CompareWithC(t,
		func() {
			require.NoError(t, coracle.FreeObject(replaced, objectSize))
		},
		func() {
			require.NoError(t, cpconfig.DestroyObject((*layout.CpObject)(replaced), objectSize, nil))
		},
	)
}

// Test_DestroyObject_RefusesPublishedObject verifies that destroying an
// object the published generation holds is refused as still referenced
// and frees nothing, its body included.
func Test_DestroyObject_RefusesPublishedObject(t *testing.T) {
	memory := newPublishedFixture(t)
	object := goNewObject(t, memory, "state")
	require.NoError(t, cpconfig.UpdateObjects(memory.Instance(), []*layout.CpObject{object}, cpconfig.WithDelivery(nativetest.Deliver)))
	err := cpconfig.DestroyObject(object, objectSize, func() {
		t.Fatal("the body of a published object is freed")
	})
	require.ErrorIs(t, err, cpconfig.ErrStillReferenced)
	require.NotNil(t, object.Entity.Agent.Get(), "the published object is finished")
}
