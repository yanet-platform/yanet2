//go:build linux && (amd64 || arm64)

package zone_test

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// Test_Generation_ModulesRegistryItemOpensModule verifies that a module's
// registry item sits at its very start, so the item a registry holds is
// the module itself.
func Test_Generation_ModulesRegistryItemOpensModule(t *testing.T) {
	require.Zero(t, unsafe.Offsetof(layout.CpModule{}.Entity))
	require.Zero(t, unsafe.Offsetof(layout.CpEntity{}.ConfigItem))
}

// publishedObjects returns the objects the pinned generation publishes and
// the capacity of its object registry.
func publishedObjects(t *testing.T, instance *zone.Instance) ([]zone.Object, uint64) {
	t.Helper()
	generation, err := instance.Pin()
	require.NoError(t, err)
	defer generation.Release()
	return generation.Objects(), generation.Gen.ObjectRegistry.EntityRegistry.Registry.Capacity
}

// Test_Generation_Objects_ReportsPublishedInRegistryOrder verifies that
// the published objects are reported in registry order with their owner,
// and that the registry's free slots report nothing.
func Test_Generation_Objects_ReportsPublishedInRegistryOrder(t *testing.T) {
	memory := newPublished(t)
	publishRing(t, memory, "first")
	publishRing(t, memory, "second")

	objects, capacity := publishedObjects(t, memory.Instance)

	require.Greater(t, capacity, uint64(len(objects)), "the registry has free slots")
	require.Equal(t, []zone.Object{
		{Type: "ring", Name: "first", Agent: "native-demo"},
		{Type: "ring", Name: "second", Agent: "native-demo"},
	}, objects)
}

// Test_Generation_Objects_SkipsDeleted verifies that an object deleted
// after its publication is not reported by a later generation, while the
// objects around its freed slot are.
func Test_Generation_Objects_SkipsDeleted(t *testing.T) {
	memory := newPublished(t)
	for _, name := range []string{"first", "gone", "last"} {
		publishRing(t, memory, name)
	}
	require.NoError(t, cpconfig.DeleteObject(memory.Instance, "ring", "gone", memory.Deliver()))

	objects, _ := publishedObjects(t, memory.Instance)

	require.Equal(t, []zone.Object{
		{Type: "ring", Name: "first", Agent: "native-demo"},
		{Type: "ring", Name: "last", Agent: "native-demo"},
	}, objects)
}

// Test_Generation_Objects_NamesSupersededOwner verifies that an object
// still published by an agent attached again under its name reports that
// name as its owner.
func Test_Generation_Objects_NamesSupersededOwner(t *testing.T) {
	memory := newPublished(t)
	publishRing(t, memory, "first")
	_, err := agent.Attach(memory.Instance, "native-demo", uint64(agentLimit))
	require.NoError(t, err)
	require.Equal(t, 1, superseded(t, memory.Instance, "native-demo"))

	objects, _ := publishedObjects(t, memory.Instance)

	require.Equal(t, []zone.Object{{Type: "ring", Name: "first", Agent: "native-demo"}}, objects)
}

// Test_Generation_Objects_ReleasedGenerationReportsNothing verifies that
// a released pin no longer reads the objects of the generation it held.
func Test_Generation_Objects_ReleasedGenerationReportsNothing(t *testing.T) {
	memory := newPublished(t)
	publishRing(t, memory, "first")
	generation, err := memory.Instance.Pin()
	require.NoError(t, err)

	generation.Release()

	require.Empty(t, generation.Objects())
}

// Test_Instance_DataplaneObjects_ReportsLoadedTypes verifies that the
// object types the dataplane loaded are reported, and none for a
// dataplane that loaded none.
func Test_Instance_DataplaneObjects_ReportsLoadedTypes(t *testing.T) {
	cases := []struct {
		name     string
		instance func(t *testing.T) *zone.Instance
		want     []zone.DataplaneObject
	}{
		{"loaded ring type", func(t *testing.T) *zone.Instance {
			return newPublished(t).Instance
		}, []zone.DataplaneObject{{Name: "ring"}}},
		{"no loaded types", func(t *testing.T) *zone.Instance {
			return newGenerationDataplane(t).Instance()
		}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.instance(t).DataplaneObjects())
		})
	}
}
