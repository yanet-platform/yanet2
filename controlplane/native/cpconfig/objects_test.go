//go:build linux && (amd64 || arm64)

package cpconfig_test

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/coracle"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/modules/blackhole/bindings/go/cblackhole"
)

// objectSize is the size of a test object: its header and a body, as an
// object builder lays one out.
const objectSize = unsafe.Sizeof(layout.CpObject{}) + 256

// cPublishObject creates a ring object with a counter through C and
// publishes it through the fixture's agent.
func cPublishObject(t *testing.T, memory *publishedFixture, name string) {
	t.Helper()
	agent := unsafe.Pointer(memory.owner.Agent)
	object, err := coracle.NewObject(agent, "ring", name, objectSize)
	require.NoError(t, err)
	_, err = coracle.RegisterCounter(unsafe.Pointer(&(*layout.CpObject)(object).CounterRegistry), "drops", 2)
	require.NoError(t, err)
	require.NoError(t, coracle.UpdateObjects(agent, []unsafe.Pointer{object}))
}

// Test_UpdateObjects_MatchesC verifies that creating an object with a
// counter and publishing it from Go, a fresh object and one replacing a
// published one alike, leaves the storage as cp_object_init,
// counter_registry_register and cp_config_update_objects do: the object,
// its registry slot, its counters continued from the replaced object and
// every worker's object context.
func Test_UpdateObjects_MatchesC(t *testing.T) {
	for _, replaced := range []bool{false, true} {
		name := "fresh"
		if replaced {
			name = "replacing"
		}
		t.Run(name, func(t *testing.T) {
			memory := newPublishedFixture(t)
			if replaced {
				cPublishObject(t, memory, "state")
			}
			instance := memory.Instance()
			memory.CompareWithC(t,
				func() {
					cPublishObject(t, memory, "state")
				},
				func() {
					object, err := cpconfig.NewObject(memory.owner, "ring", "state", objectSize)
					require.NoError(t, err)
					_, err = cpconfig.RegisterCounter(&object.CounterRegistry, "drops", 2)
					require.NoError(t, err)
					require.NoError(t, cpconfig.UpdateObjects(instance, []*layout.CpObject{object},
						cpconfig.WithDelivery(nativetest.Deliver),
					))
				},
			)
		})
	}
}

// Test_UpdateModules_LinkedObject_MatchesC verifies that a module linking
// a published object, published from Go, leaves the storage as
// cp_module_link_object and cp_config_update_modules do: the module's
// link, and every worker's link context and its counter storage.
func Test_UpdateModules_LinkedObject_MatchesC(t *testing.T) {
	memory := newPublishedFixture(t)
	cPublishObject(t, memory, "state")
	instance := memory.Instance()
	memory.CompareWithC(t,
		func() {
			config, err := cblackhole.NewModuleConfig(memory.cAgent, "a")
			require.NoError(t, err)
			require.NoError(t, coracle.LinkObject(config.AsFFIModule().AsRawPtr(), "ring", "state"))
			require.NoError(t, memory.cAgent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}))
		},
		func() {
			module, err := cpconfig.NewBlackhole(memory.owner, "a")
			require.NoError(t, err)
			_, err = cpconfig.LinkObject(module, "ring", "state")
			require.NoError(t, err)
			require.NoError(t, cpconfig.UpdateModules(instance, []*layout.CpModule{module},
				cpconfig.WithDelivery(nativetest.Deliver),
			))
		},
	)
}

// Test_UpdateModules_RefusesLinkToMissingObject verifies that a module
// linking an object no generation holds is refused and the published
// generation stays.
func Test_UpdateModules_RefusesLinkToMissingObject(t *testing.T) {
	memory := newPublishedFixture(t)
	instance := memory.Instance()
	published := instance.Cp.CpConfigGen.Get()
	module, err := cpconfig.NewBlackhole(memory.owner, "a")
	require.NoError(t, err)
	_, err = cpconfig.LinkObject(module, "ring", "missing")
	require.NoError(t, err)
	require.Error(t, cpconfig.UpdateModules(instance, []*layout.CpModule{module}, cpconfig.WithDelivery(nativetest.Deliver)))
	require.Same(t, published, instance.Cp.CpConfigGen.Get())
}
