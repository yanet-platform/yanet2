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

// objectSize is the size of a test object: its header and a body, as an
// object builder lays one out.
const objectSize = unsafe.Sizeof(layout.CpObject{}) + 256

// cNewObject creates a ring object through C with a counter and a link
// counter.
func cNewObject(t *testing.T, memory *publishedFixture, name string) unsafe.Pointer {
	t.Helper()
	object, err := coracle.NewObject(unsafe.Pointer(memory.owner.Agent), "ring", name, objectSize)
	require.NoError(t, err)
	_, err = coracle.RegisterCounter(unsafe.Pointer(&(*layout.CpObject)(object).CounterRegistry), "drops", 2)
	require.NoError(t, err)
	_, err = coracle.RegisterCounter(unsafe.Pointer(&(*layout.CpObject)(object).LinkCounterRegistry), "hits", 2)
	require.NoError(t, err)
	return object
}

// goNewObject creates a ring object natively with a counter and a link
// counter.
func goNewObject(t *testing.T, memory *publishedFixture, name string) *layout.CpObject {
	t.Helper()
	object, err := cpconfig.NewObject(memory.owner, "ring", name, objectSize)
	require.NoError(t, err)
	_, err = cpconfig.RegisterCounter(&object.CounterRegistry, "drops", 2)
	require.NoError(t, err)
	_, err = cpconfig.RegisterCounter(&object.LinkCounterRegistry, "hits", 2)
	require.NoError(t, err)
	return object
}

// cPublishObject creates a ring object with its counters through C and
// publishes it through the fixture's agent.
func cPublishObject(t *testing.T, memory *publishedFixture, name string) {
	t.Helper()
	object := cNewObject(t, memory, name)
	require.NoError(t, coracle.UpdateObjects(unsafe.Pointer(memory.owner.Agent), []unsafe.Pointer{object}))
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
					object := goNewObject(t, memory, "state")
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

// Test_UpdateObjects_SeveralLinkedMatchesC verifies that replacing a
// linked object and adding another in one update from Go leaves the
// storage as C does: both objects, the replaced one's counters and link
// counters continued, and the linking module's link contexts spawned
// from the new link counters.
func Test_UpdateObjects_SeveralLinkedMatchesC(t *testing.T) {
	memory := newPublishedFixture(t)
	cPublishObject(t, memory, "state")
	config, err := cblackhole.NewModuleConfig(memory.cAgent, "a")
	require.NoError(t, err)
	require.NoError(t, coracle.LinkObject(config.AsFFIModule().AsRawPtr(), "ring", "state"))
	require.NoError(t, memory.cAgent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}))
	instance := memory.Instance()
	memory.CompareWithC(t,
		func() {
			objects := []unsafe.Pointer{cNewObject(t, memory, "state"), cNewObject(t, memory, "other")}
			require.NoError(t, coracle.UpdateObjects(unsafe.Pointer(memory.owner.Agent), objects))
		},
		func() {
			objects := []*layout.CpObject{goNewObject(t, memory, "state"), goNewObject(t, memory, "other")}
			require.NoError(t, cpconfig.UpdateObjects(instance, objects, cpconfig.WithDelivery(nativetest.Deliver)))
		},
	)
}

// Test_NewObject_RefusesUnknownType verifies that an object of a type
// the dataplane has not loaded is refused, as cp_object_init refuses it.
func Test_NewObject_RefusesUnknownType(t *testing.T) {
	memory := newPublishedFixture(t)
	_, err := cpconfig.NewObject(memory.owner, "missing", "state", objectSize)
	require.Error(t, err)
}

// Test_UpdateObjects_RefusesRepeatedObject verifies that an update naming
// one object twice is refused and the published generation stays.
func Test_UpdateObjects_RefusesRepeatedObject(t *testing.T) {
	memory := newPublishedFixture(t)
	instance := memory.Instance()
	published := instance.Cp.CpConfigGen.Get()
	objects := []*layout.CpObject{goNewObject(t, memory, "state"), goNewObject(t, memory, "state")}
	require.Error(t, cpconfig.UpdateObjects(instance, objects, cpconfig.WithDelivery(nativetest.Deliver)))
	require.Same(t, published, instance.Cp.CpConfigGen.Get())
}

// Test_DeleteObject_MatchesC verifies that deleting a published object
// from Go leaves the storage as cp_config_delete_object does: its
// registry slot freed, its owner's loaded count dropped and every
// worker's context rebuilt without it.
func Test_DeleteObject_MatchesC(t *testing.T) {
	memory := newPublishedFixture(t)
	cPublishObject(t, memory, "state")
	cPublishObject(t, memory, "other")
	instance := memory.Instance()
	memory.CompareWithC(t,
		func() {
			require.NoError(t, memory.cAgent.DeleteObject("ring", "state"))
		},
		func() {
			require.NoError(t, cpconfig.DeleteObject(instance, "ring", "state", cpconfig.WithDelivery(nativetest.Deliver)))
		},
	)
}

// Test_DeleteObject_RefusesMissingObject verifies that deleting an object
// the published generation does not hold is refused as not found and the
// published generation stays.
func Test_DeleteObject_RefusesMissingObject(t *testing.T) {
	memory := newPublishedFixture(t)
	instance := memory.Instance()
	published := instance.Cp.CpConfigGen.Get()
	err := cpconfig.DeleteObject(instance, "ring", "missing", cpconfig.WithDelivery(nativetest.Deliver))
	require.ErrorIs(t, err, cpconfig.ErrNotFound)
	require.Same(t, published, instance.Cp.CpConfigGen.Get())
}

// Test_DeleteObject_RefusesLinkedObject verifies that deleting an object
// a published module links is refused as linked, as C refuses it, and
// the published generation stays.
func Test_DeleteObject_RefusesLinkedObject(t *testing.T) {
	memory := newPublishedFixture(t)
	cPublishObject(t, memory, "state")
	instance := memory.Instance()
	module, err := cpconfig.NewBlackhole(memory.owner, "a")
	require.NoError(t, err)
	_, err = cpconfig.LinkObject(module, "ring", "state")
	require.NoError(t, err)
	require.NoError(t, cpconfig.UpdateModules(instance, []*layout.CpModule{module}, cpconfig.WithDelivery(nativetest.Deliver)))
	published := instance.Cp.CpConfigGen.Get()
	require.Error(t, memory.cAgent.DeleteObject("ring", "state"))
	err = cpconfig.DeleteObject(instance, "ring", "state", cpconfig.WithDelivery(nativetest.Deliver))
	require.ErrorIs(t, err, cpconfig.ErrObjectLinked)
	require.Same(t, published, instance.Cp.CpConfigGen.Get())
}

// Test_LinkObject_ReturnsTheExistingLink verifies that linking a module to
// an object it links already returns that link and appends none.
func Test_LinkObject_ReturnsTheExistingLink(t *testing.T) {
	memory := newPublishedFixture(t)
	module, err := cpconfig.NewBlackhole(memory.owner, "a")
	require.NoError(t, err)
	first, err := cpconfig.LinkObject(module, "ring", "state")
	require.NoError(t, err)
	second, err := cpconfig.LinkObject(module, "ring", "other")
	require.NoError(t, err)
	again, err := cpconfig.LinkObject(module, "ring", "state")
	require.NoError(t, err)
	require.Equal(t, first, again)
	require.NotEqual(t, first, second)
	require.Equal(t, uint64(2), module.ObjectCount)
}
