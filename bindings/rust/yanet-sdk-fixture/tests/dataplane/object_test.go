//go:build sdk_rust_fixtures

package dataplane_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/sdk/sdkfixture"
)

// stateSlots is the number of slots of a state object of the tests.
const stateSlots = 4

// withoutObjects starts a bed's harness with no object type loaded.
func withoutObjects(config *dataplaneut.Config) {
	config.ObjectsToLoad = nil
}

// stateObject is a state object a native builder made.
type stateObject = handle[layout.CpObject]

// buildState builds a state object named name of count slots from seed over
// the agent, without registering it for cleanup.
func buildState(owner *agent.Agent, name string, seed, count uint64) (*stateObject, error) {
	object, err := sdkfixture.NewStateObject(owner, name, seed, count)
	if err != nil {
		return nil, err
	}
	return &stateObject{entity: object, free: sdkfixture.FreeStateObject}, nil
}

// stateOf builds a state object named name of count slots from seed over the
// bed's agent, which must succeed, and frees it when the test ends, before
// the harness goes.
func (m *bed) stateOf(name string, seed, count uint64) *stateObject {
	m.t.Helper()

	object, err := buildState(m.agent, name, seed, count)
	require.NoError(m.t, err)
	m.t.Cleanup(func() { _ = object.Free() })
	return object
}

// state builds a state object named name of stateSlots slots from seed, as
// stateOf does.
func (m *bed) state(name string, seed uint64) *stateObject {
	m.t.Helper()

	return m.stateOf(name, seed, stateSlots)
}

// publishObjects publishes the objects as one generation.
func (m *bed) publishObjects(objects ...*stateObject) {
	m.t.Helper()

	require.NoError(m.t, m.updateObjects(objects...))
}

// updateObjects publishes the objects as one generation and returns the
// refusal of the update.
func (m *bed) updateObjects(objects ...*stateObject) error {
	configs := make([]*layout.CpObject, 0, len(objects))
	for _, object := range objects {
		configs = append(configs, object.entity)
	}
	return cpconfig.UpdateObjects(m.instance, configs, m.deliver())
}

// deleteState publishes a generation without the state object named name
// and returns the refusal of the delete.
func (m *bed) deleteState(name string) error {
	return cpconfig.DeleteObject(m.instance, sdkfixture.StateType, name, m.deliver())
}

// slots returns the values of every slot of the object.
func slots(t *testing.T, object *stateObject) []uint64 {
	t.Helper()

	values := make([]uint64, 0, stateSlots)
	for index := range uint64(stateSlots) {
		value, err := sdkfixture.StateSlot(object.entity, index)
		require.NoError(t, err)
		values = append(values, value)
	}
	return values
}

// failedStateBuilds are the builds of a state object that the builder refuses
// before it builds anything, or that the dataplane's types refuse.
var failedStateBuilds = []struct {
	name     string
	adjust   []func(*dataplaneut.Config)
	object   string
	wantKind error
}{
	{name: "type not loaded", adjust: []func(*dataplaneut.Config){withoutObjects}, object: "state"},
	{name: "overlong name", object: strings.Repeat("s", 128), wantKind: cerrors.InvalidArgument},
}

// Test_StateObject_Publish_IsAccepted verifies that a built object is published
// into a generation.
func Test_StateObject_Publish_IsAccepted(t *testing.T) {
	bed := newBed(t)
	object := bed.state("state", 10)

	err := bed.updateObjects(object)

	require.NoError(t, err)
}

// Test_StateObject_Build_Failure_ReportsItsKind verifies that a refused build
// reports the kind a service reports for the failure, and none for a failure
// C tags with none.
func Test_StateObject_Build_Failure_ReportsItsKind(t *testing.T) {
	for _, tc := range failedStateBuilds {
		t.Run(tc.name, func(t *testing.T) {
			bed := newBed(t, tc.adjust...)

			_, err := buildState(bed.agent, tc.object, 10, stateSlots)

			if tc.wantKind == nil {
				requireUntagged(t, err)
				return
			}
			require.ErrorIs(t, kindOf(err), tc.wantKind)
		})
	}
}

// Test_StateObject_Build_Failure_ReturnsMemoryToBaseline verifies that a
// failed build gives back every block it took.
func Test_StateObject_Build_Failure_ReturnsMemoryToBaseline(t *testing.T) {
	for _, tc := range failedStateBuilds {
		t.Run(tc.name, func(t *testing.T) {
			bed := newBed(t, tc.adjust...)

			_, err := buildState(bed.agent, tc.object, 10, stateSlots)

			require.Error(t, err)
			require.Equal(t, bed.baseline, freeSize(bed.agent))
		})
	}
}

// Test_StateObject_Build_EachAllocationRefused_ReturnsMemoryToBaseline
// verifies that a build refused at any of its allocations refuses as
// exhausted and gives back all it took.
func Test_StateObject_Build_EachAllocationRefused_ReturnsMemoryToBaseline(t *testing.T) {
	bed := newBed(t)

	sweepAllocations(t, bed.agent, func(owner *agent.Agent) (func() error, error) {
		object, err := buildState(owner, "state", 10, stateSlots)
		if err != nil {
			return nil, err
		}
		return object.Free, nil
	})
}

// Test_StateObject_Build_TypeNotLoaded_IsUnknownType verifies that the
// refusal of an object type the dataplane has not loaded reaches the caller
// as an unknown type.
func Test_StateObject_Build_TypeNotLoaded_IsUnknownType(t *testing.T) {
	bed := newBed(t, withoutObjects)

	_, err := buildState(bed.agent, "state", 10, stateSlots)

	require.ErrorIs(t, err, cpconfig.ErrUnknownType)
}

// Test_StateObject_Slot_ReadsTheBuiltValues verifies that the control plane
// reads a published object's body as its build wrote it.
func Test_StateObject_Slot_ReadsTheBuiltValues(t *testing.T) {
	bed := newBed(t)
	object := bed.state("state", 10)
	bed.publishObjects(object)

	require.Equal(t, []uint64{10, 11, 12, 13}, slots(t, object))
}

// Test_StateObject_Slot_OutOfRange_IsInvalidArgument verifies that a query of
// a slot the object does not have is refused as an invalid argument.
func Test_StateObject_Slot_OutOfRange_IsInvalidArgument(t *testing.T) {
	bed := newBed(t)
	object := bed.state("state", 10)

	_, err := sdkfixture.StateSlot(object.entity, stateSlots)

	require.ErrorIs(t, err, sdkfixture.ErrInvalidArgument)
}

// Test_StateObject_Add_ChangesThePublishedObject verifies that the control
// plane changes a published object's state in place, through its atomics.
func Test_StateObject_Add_ChangesThePublishedObject(t *testing.T) {
	bed := newBed(t)
	object := bed.state("state", 10)
	bed.publishObjects(object)

	_, err := sdkfixture.StateAdd(object.entity, 1, 5)
	require.NoError(t, err)

	require.Equal(t, []uint64{10, 16, 12, 13}, slots(t, object))
}

// Test_StateObject_State_OutlivesModuleUpdates verifies that the state of a
// published object stays as it was while module configurations are published
// and replaced around it.
func Test_StateObject_State_OutlivesModuleUpdates(t *testing.T) {
	bed := newBed(t)
	object := bed.state("state", 10)
	bed.publishObjects(object)
	_, err := sdkfixture.StateAdd(object.entity, 0, 7)
	require.NoError(t, err)

	bed.publishModules(bed.tallyInstance("tally", 1))
	bed.publishModules(bed.tallyInstance("tally", 2))

	require.Equal(t, []uint64{17, 11, 12, 13}, slots(t, object))
}

// Test_StateObject_Free_Unpublished_ReturnsMemoryToBaseline verifies that
// freeing an object no generation holds gives back all it took.
func Test_StateObject_Free_Unpublished_ReturnsMemoryToBaseline(t *testing.T) {
	bed := newBed(t)
	object := bed.state("state", 10)

	require.NoError(t, object.Free())

	require.Equal(t, bed.baseline, freeSize(bed.agent))
}

// Test_StateObject_PerWorker_SizedByTheWorkers verifies that the slot per
// worker of an object takes more memory on a dataplane of more workers, and
// that the published object gives all of it back once deleted and freed.
func Test_StateObject_PerWorker_SizedByTheWorkers(t *testing.T) {
	used := map[uint64]uint64{}
	for _, workers := range []uint64{1, workerCount} {
		bed := newBed(t, withWorkers(workers))
		object := bed.state("state", 10)
		used[workers] = bed.used()
		bed.publishObjects(object)
		require.NoError(t, bed.deleteState("state"))

		require.NoError(t, object.Free())

		require.Equal(t, bed.baseline, freeSize(bed.agent), "%d workers", workers)
	}
	require.Greater(t, used[workerCount], used[1])
}

// Test_StateObject_Free_Unpublished_LeavesLoadedCountUnchanged verifies that
// the free returns the destruction hold it takes.
func Test_StateObject_Free_Unpublished_LeavesLoadedCountUnchanged(t *testing.T) {
	bed := newBed(t)
	before := loadedCount(bed.agent)
	object := bed.state("state", 10)

	require.NoError(t, object.Free())

	require.Equal(t, before, loadedCount(bed.agent))
}

// publishedState builds a state object and publishes it, which a generation
// holds until a later one drops it.
func publishedState(t *testing.T) (*bed, *stateObject) {
	t.Helper()

	bed := newBed(t)
	object := bed.state("state", 10)
	bed.publishObjects(object)
	return bed, object
}

// Test_StateObject_Free_Published_ReportsStillReferenced verifies that freeing
// an object a live generation holds is refused as still referenced.
func Test_StateObject_Free_Published_ReportsStillReferenced(t *testing.T) {
	_, object := publishedState(t)

	require.ErrorIs(t, object.Free(), cpconfig.ErrStillReferenced)
}

// Test_StateObject_Free_Published_KeepsMemory verifies that a refused free
// takes none of the object's memory back.
func Test_StateObject_Free_Published_KeepsMemory(t *testing.T) {
	bed, object := publishedState(t)
	used := bed.used()

	_ = object.Free()

	require.Equal(t, used, bed.used())
}

// Test_StateObject_Free_Published_KeepsTheObjectReadable verifies that a
// refused free keeps the handle and the state of the object.
func Test_StateObject_Free_Published_KeepsTheObjectReadable(t *testing.T) {
	_, object := publishedState(t)

	_ = object.Free()

	require.Equal(t, []uint64{10, 11, 12, 13}, slots(t, object))
}

// Test_StateObject_Free_Published_TakesNoDestructionHold verifies that a
// refused free leaves the agent's loaded count as the generation made it.
func Test_StateObject_Free_Published_TakesNoDestructionHold(t *testing.T) {
	bed, object := publishedState(t)
	held := loadedCount(bed.agent)

	_ = object.Free()

	require.Equal(t, held, loadedCount(bed.agent))
}

// Test_StateObject_Free_AfterReplace_Succeeds verifies that an object replaced
// by a same-name upsert is freed, and the object that replaced it stays
// readable.
func Test_StateObject_Free_AfterReplace_Succeeds(t *testing.T) {
	bed, old := publishedState(t)
	replacement := bed.state("state", 20)
	bed.publishObjects(replacement)

	require.NoError(t, old.Free())

	require.Equal(t, []uint64{20, 21, 22, 23}, slots(t, replacement))
}

// Test_StateObject_Free_AfterReplace_KeepsTheReplacementPublished verifies that
// the object that replaced a freed one is still the one a generation holds.
func Test_StateObject_Free_AfterReplace_KeepsTheReplacementPublished(t *testing.T) {
	bed, old := publishedState(t)
	replacement := bed.state("state", 20)
	bed.publishObjects(replacement)
	require.NoError(t, old.Free())

	require.ErrorIs(t, replacement.Free(), cpconfig.ErrStillReferenced)
}

// Test_StateObject_Free_AfterDelete_ReturnsMemoryToBaseline verifies that an
// object whose free was refused is freed once a delete drops it from the
// generation, and gives back all it took.
func Test_StateObject_Free_AfterDelete_ReturnsMemoryToBaseline(t *testing.T) {
	bed, object := publishedState(t)
	require.ErrorIs(t, object.Free(), cpconfig.ErrStillReferenced)
	require.NoError(t, bed.deleteState("state"))

	require.NoError(t, object.Free())

	require.Equal(t, bed.baseline, freeSize(bed.agent))
}

// Test_StateObject_Free_AfterReplaceAndDelete_ReturnsMemoryToBaseline verifies
// that both objects of a replacement are freed once neither is published.
func Test_StateObject_Free_AfterReplaceAndDelete_ReturnsMemoryToBaseline(t *testing.T) {
	bed, old := publishedState(t)
	replacement := bed.state("state", 20)
	bed.publishObjects(replacement)
	require.NoError(t, bed.deleteState("state"))

	require.NoError(t, old.Free())
	require.NoError(t, replacement.Free())

	require.Equal(t, bed.baseline, freeSize(bed.agent))
}

// Test_StateObject_Seed_ReadsTheSeed verifies that a query reads the seed the
// object was built from.
func Test_StateObject_Seed_ReadsTheSeed(t *testing.T) {
	bed := newBed(t)
	object := bed.state("state", 10)

	require.Equal(t, uint64(10), sdkfixture.StateSeed(object.entity))
}

// Test_StateObject_LockedAdd_ChangesThePublishedCount verifies that the
// control plane adds to a published object's locked count under its lock,
// from the seed the count starts at.
func Test_StateObject_LockedAdd_ChangesThePublishedCount(t *testing.T) {
	_, object := publishedState(t)

	sum := sdkfixture.StateLockedAdd(object.entity, 5)

	require.Equal(t, []uint64{15, 15}, []uint64{sum, sdkfixture.StateLocked(object.entity)})
}

// Test_StateObject_Values_ReadsEverySlotInOneCall verifies that a query fills
// the caller's slice with the values it reads.
func Test_StateObject_Values_ReadsEverySlotInOneCall(t *testing.T) {
	bed := newBed(t)
	object := bed.state("state", 10)
	values := make([]uint64, stateSlots)

	sdkfixture.StateValues(object.entity, values)

	require.Equal(t, []uint64{10, 11, 12, 13}, values)
}

// Test_StateObject_Values_ShortSlice_ReadsTheFirstSlots verifies that a query
// writes no further than the caller's slice reaches.
func Test_StateObject_Values_ShortSlice_ReadsTheFirstSlots(t *testing.T) {
	bed := newBed(t)
	object := bed.state("state", 10)
	values := make([]uint64, 2)

	sdkfixture.StateValues(object.entity, values)

	require.Equal(t, []uint64{10, 11}, values)
}

// Test_StateObject_Values_LongSlice_ZeroesThePlacesPastTheSlots verifies that
// the places of a slice the query leaves are zero, whatever they held.
func Test_StateObject_Values_LongSlice_ZeroesThePlacesPastTheSlots(t *testing.T) {
	bed := newBed(t)
	object := bed.state("state", 10)
	values := []uint64{0xdead, 0xdead, 0xdead, 0xdead, 0xdead, 0xdead}

	sdkfixture.StateValues(object.entity, values)

	require.Equal(t, []uint64{10, 11, 12, 13, 0, 0}, values)
}

// Test_StateObject_Values_NoPlaces_ReturnsTheSlotCount verifies that a query
// given an empty slice returns how many values it has, which sizes the slice
// of the next call.
func Test_StateObject_Values_NoPlaces_ReturnsTheSlotCount(t *testing.T) {
	bed := newBed(t)
	object := bed.state("state", 10)

	count := sdkfixture.StateValues(object.entity, nil)

	require.Equal(t, uint64(stateSlots), count)
}

// Test_StateObject_Shape_FillsTheRecord verifies that a query fills every
// field of the caller's record, whatever it held.
func Test_StateObject_Shape_FillsTheRecord(t *testing.T) {
	bed := newBed(t)
	object := bed.state("state", 10)
	shape := sdkfixture.StateShape{Seed: 0xdead, Slots: 0xdead, Workers: 0xdead, RegionLen: 0xdead}

	sdkfixture.ReadStateShape(object.entity, &shape)

	require.Equal(t, sdkfixture.StateShape{Seed: 10, Slots: stateSlots, Workers: workerCount, RegionLen: 2}, shape)
}

// Test_StateObject_Shape_ReturnsTheSlotCount verifies that a query with a
// record still returns its value.
func Test_StateObject_Shape_ReturnsTheSlotCount(t *testing.T) {
	bed := newBed(t)
	object := bed.state("state", 10)
	var shape sdkfixture.StateShape

	count := sdkfixture.ReadStateShape(object.entity, &shape)

	require.Equal(t, uint64(stateSlots), count)
}

// Test_StateObject_Add_Unpublished_ChangesTheSlot verifies that an object no
// generation holds is changed as a published one is.
func Test_StateObject_Add_Unpublished_ChangesTheSlot(t *testing.T) {
	bed := newBed(t)
	object := bed.state("state", 10)

	_, err := sdkfixture.StateAdd(object.entity, 3, 4)
	require.NoError(t, err)

	require.Equal(t, []uint64{10, 11, 12, 17}, slots(t, object))
}

// Test_StateObject_Add_Wrapping_WrapsTheSum verifies that a sum past the
// largest value wraps instead of failing.
func Test_StateObject_Add_Wrapping_WrapsTheSum(t *testing.T) {
	bed := newBed(t)
	object := bed.state("state", 10)

	sum, err := sdkfixture.StateAdd(object.entity, 0, ^uint64(0))

	require.NoError(t, err)
	require.Equal(t, uint64(9), sum)
}

// Test_StateObject_Add_Concurrent_CountsEveryAdd verifies that adds from many
// goroutines to one slot of a published object are all counted.
func Test_StateObject_Add_Concurrent_CountsEveryAdd(t *testing.T) {
	const goroutines, adds = 8, 1000
	_, object := publishedState(t)

	var group errgroup.Group
	for range goroutines {
		group.Go(func() error {
			for range adds {
				if _, err := sdkfixture.StateAdd(object.entity, 2, 1); err != nil {
					return err
				}
			}
			return nil
		})
	}
	require.NoError(t, group.Wait())

	value, err := sdkfixture.StateSlot(object.entity, 2)
	require.NoError(t, err)
	require.Equal(t, uint64(12+goroutines*adds), value)
}
