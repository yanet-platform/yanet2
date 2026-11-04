//go:build sdk_rust_fixtures

package cfixture_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/lib/rust/yanet-sdk-fixture-api/bindings/go/cfixture"
	"github.com/yanet-platform/yanet2/objects/ring/bindings/go/cring"
)

// withRing also loads the ring object type, an object type other than the
// state's.
func withRing(config *dataplaneut.Config) {
	config.ObjectsToLoad = append(config.ObjectsToLoad, cring.ObjectType)
}

// stashInstance builds a stash configuration named name linking the state
// object named state, as an instance of the module type stash.
func (m *bed) stashInstance(name, state string) *instance {
	m.t.Helper()

	config, err := cfixture.NewStashConfig(m.agent, name, state)
	require.NoError(m.t, err)
	m.freeAtEnd(config)
	return &instance{moduleType: "stash", name: name, config: config}
}

// servedStash publishes a state object named state from seed 10 and serves a
// stash linking it before a forwarding instance.
func servedStash(t *testing.T) (*bed, *cfixture.StateObject) {
	t.Helper()

	bed := newBed(t)
	object := bed.state("state", 10)
	bed.publishObjects(object)
	bed.serveBeforeForward(bed.stashInstance("stash", "state"))
	return bed, object
}

// Test_Stash_Build_OverlongStateName_IsInvalidArgument verifies that a link to
// an object name C would cut short is refused before C sees it.
func Test_Stash_Build_OverlongStateName_IsInvalidArgument(t *testing.T) {
	bed := newBed(t)

	_, err := cfixture.NewStashConfig(bed.agent, "stash", strings.Repeat("s", ffi.MaxObjectNameLen))

	require.ErrorIs(t, err, ffi.ErrInvalidArgument)
}

// Test_Stash_Build_OverlongStateName_ReturnsMemoryToBaseline verifies that a
// build whose link is refused gives back every block it took.
func Test_Stash_Build_OverlongStateName_ReturnsMemoryToBaseline(t *testing.T) {
	bed := newBed(t)

	_, err := cfixture.NewStashConfig(bed.agent, "stash", strings.Repeat("s", ffi.MaxObjectNameLen))

	require.Error(t, err)
	require.Equal(t, bed.baseline, bed.agent.BlockAllocatorFreeSize())
}

// Test_Stash_Build_LongestStateName_IsAccepted verifies that a link to an
// object name filling its C field with the terminator is built.
func Test_Stash_Build_LongestStateName_IsAccepted(t *testing.T) {
	bed := newBed(t)

	config, err := cfixture.NewStashConfig(bed.agent, "stash", strings.Repeat("s", ffi.MaxObjectNameLen-1))

	require.NoError(t, err)
	require.NoError(t, config.Free())
}

// Test_Stash_Install_ObjectUnpublished_IsFailedPrecondition verifies that a
// configuration linking an object no generation holds is not installed.
func Test_Stash_Install_ObjectUnpublished_IsFailedPrecondition(t *testing.T) {
	bed := newBed(t)
	stash := bed.stashInstance("stash", "state")

	err := bed.agent.UpdateModules([]ffi.ModuleConfig{stash.config.AsFFIModule()})

	require.ErrorIs(t, err, ffi.ErrFailedPrecondition)
}

// Test_Stash_Install_ObjectUnpublished_ReturnsMemoryToBaseline verifies that a
// refused install keeps nothing of the configuration once it is freed.
func Test_Stash_Install_ObjectUnpublished_ReturnsMemoryToBaseline(t *testing.T) {
	bed := newBed(t)
	stash := bed.stashInstance("stash", "state")
	require.Error(t, bed.agent.UpdateModules([]ffi.ModuleConfig{stash.config.AsFFIModule()}))

	require.NoError(t, stash.config.Free())

	require.Equal(t, bed.baseline, bed.agent.BlockAllocatorFreeSize())
}

// Test_Stash_Install_ObjectOfAnotherType_IsFailedPrecondition verifies that a
// link resolves by type and name: an object of another type under the linked
// name does not satisfy it.
func Test_Stash_Install_ObjectOfAnotherType_IsFailedPrecondition(t *testing.T) {
	bed := newBed(t, withRing)
	ring, err := cring.NewObject(bed.agent, "state", 64, cring.DefaultPublishBatch)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ring.Free() })
	require.NoError(t, ring.Publish())
	stash := bed.stashInstance("stash", "state")

	err = bed.agent.UpdateModules([]ffi.ModuleConfig{stash.config.AsFFIModule()})

	require.ErrorIs(t, err, ffi.ErrFailedPrecondition)
}

// Test_Stash_DeleteObject_Linked_IsBusy verifies that an object a published
// configuration links is not deleted.
func Test_Stash_DeleteObject_Linked_IsBusy(t *testing.T) {
	bed := newBed(t)
	object := bed.state("state", 10)
	bed.publishObjects(object)
	bed.publishModules(bed.stashInstance("stash", "state"))

	err := bed.agent.DeleteObject(cfixture.StateType, "state")

	require.ErrorIs(t, err, ffi.ErrBusy)
}

// Test_Stash_DeleteObject_Linked_KeepsTheObjectServed verifies that a refused
// delete leaves the object in the generation, still written by the module
// linking it.
func Test_Stash_DeleteObject_Linked_KeepsTheObjectServed(t *testing.T) {
	bed, object := servedStash(t)
	require.ErrorIs(t, bed.agent.DeleteObject(cfixture.StateType, "state"), ffi.ErrBusy)

	bed.run(0, arpFrames(1)...)

	require.Equal(t, []uint64{11, 11, 12, 13}, slots(t, object))
}

// Test_Stash_DeleteObject_Unlinked_Succeeds verifies that an object is
// deleted once the configuration that linked it is replaced by one linking
// another object.
func Test_Stash_DeleteObject_Unlinked_Succeeds(t *testing.T) {
	bed, _ := servedStash(t)
	bed.publishObjects(bed.state("other", 20))
	bed.publishModules(bed.stashInstance("stash", "other"))

	err := bed.agent.DeleteObject(cfixture.StateType, "state")

	require.NoError(t, err)
}

// Test_Stash_Run_WritesTheLinkedSlots verifies that the module adds one per
// packet to the slot of the linked object its first byte selects.
func Test_Stash_Run_WritesTheLinkedSlots(t *testing.T) {
	bed, object := servedStash(t)

	bed.run(0, arpFrames(6)...)

	require.Equal(t, []uint64{12, 13, 13, 14}, slots(t, object))
}

// Test_Stash_Run_PassesEveryPacket verifies that the module passes the packets
// on to the forwarding instance after it.
func Test_Stash_Run_PassesEveryPacket(t *testing.T) {
	bed, _ := servedStash(t)
	frames := arpFrames(3)

	result := bed.run(0, frames...)

	require.Equal(t, []fate{fateOutput, fateOutput, fateOutput}, fatesOf(result, frames))
}

// Test_Stash_Run_AfterObjectReplace_WritesTheNewObject verifies that a
// same-name upsert of the object switches the unchanged configuration to the
// new object.
func Test_Stash_Run_AfterObjectReplace_WritesTheNewObject(t *testing.T) {
	bed, old := servedStash(t)
	replacement := bed.state("state", 20)
	bed.publishObjects(replacement)

	bed.run(0, arpFrames(1)...)

	require.Equal(t, [][]uint64{{10, 11, 12, 13}, {21, 21, 22, 23}}, [][]uint64{slots(t, old), slots(t, replacement)})
}

// Test_Stash_ObjectReplace_ReleasesTheOldObject verifies that an object
// replaced under a configuration linking it is freed, gives back what its
// build took, and a second free does nothing.
func Test_Stash_ObjectReplace_ReleasesTheOldObject(t *testing.T) {
	bed, old := servedStash(t)
	before := bed.used()
	replacement := bed.state("state", 20)
	cost := bed.used() - before
	bed.publishObjects(replacement)
	published := bed.used()

	require.NoError(t, old.Free())
	freed := bed.used()
	require.NoError(t, old.Free())

	require.Equal(t, []uint64{published - cost, published - cost}, []uint64{freed, bed.used()})
}

// Test_Stash_ConfigReplace_KeepsTheObjectState verifies that the state of a
// linked object outlives the configuration linking it: the configuration that
// replaces it writes on the same object.
func Test_Stash_ConfigReplace_KeepsTheObjectState(t *testing.T) {
	bed, object := servedStash(t)
	bed.run(0, arpFrames(1)...)

	bed.publishModules(bed.stashInstance("stash", "state"))
	bed.run(0, arpFrames(1)...)

	require.Equal(t, []uint64{12, 11, 12, 13}, slots(t, object))
}
