//go:build sdk_rust_fixtures

package cfixture_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/lib/rust/yanet-sdk-fixture-api/bindings/go/cfixture"
)

// stateSlots is the number of slots of a state object of the tests.
const stateSlots = 4

// withoutObjects starts a bed's harness with no object type loaded.
func withoutObjects(config *dataplaneut.Config) {
	config.ObjectsToLoad = nil
}

// buildState builds a state object named name of stateSlots slots from seed
// over the bed's agent, without registering it for cleanup.
func (m *bed) buildState(name string, seed uint64, fault cfixture.Fault) (*cfixture.StateObject, error) {
	return cfixture.NewStateObject(m.agent, name, seed, stateSlots, fault)
}

// state builds a state object named name from seed, which must succeed, and
// frees it when the test ends, before the agent goes.
func (m *bed) state(name string, seed uint64) *cfixture.StateObject {
	m.t.Helper()

	object, err := m.buildState(name, seed, cfixture.FaultNone)
	require.NoError(m.t, err)
	m.t.Cleanup(func() { _ = object.Free() })
	return object
}

// publishObjects publishes the objects as one generation.
func (m *bed) publishObjects(objects ...*cfixture.StateObject) {
	m.t.Helper()

	configs := make([]ffi.ObjectConfig, 0, len(objects))
	for _, object := range objects {
		configs = append(configs, object.AsFFIObject())
	}
	require.NoError(m.t, m.agent.UpdateObjects(configs))
}

// slots returns the values of every slot of the object.
func slots(t *testing.T, object *cfixture.StateObject) []uint64 {
	t.Helper()

	values := make([]uint64, 0, stateSlots)
	for index := range uint64(stateSlots) {
		value, err := object.Slot(index)
		require.NoError(t, err)
		values = append(values, value)
	}
	return values
}

// failedStateBuilds are the builds of a state object that C refuses, that the
// api refuses before it builds anything, or that fail after the build has
// allocated its slots.
var failedStateBuilds = []struct {
	name     string
	adjust   []func(*dataplaneut.Config)
	object   string
	fault    cfixture.Fault
	wantKind error
}{
	{name: "type not loaded", adjust: []func(*dataplaneut.Config){withoutObjects}, object: "state"},
	{name: "overlong name", object: strings.Repeat("s", 128), wantKind: ffi.ErrInvalidArgument},
	// The fault refuses the build as a block too large, which the api reports
	// as an invalid argument: no memory state would satisfy the request.
	{name: "fault error", object: "state", fault: cfixture.FaultError, wantKind: ffi.ErrInvalidArgument},
	{name: "fault panic", object: "state", fault: cfixture.FaultPanic},
}

// Test_StateObject_Publish_IsAccepted verifies that a built object is published
// into a generation.
func Test_StateObject_Publish_IsAccepted(t *testing.T) {
	bed := newBed(t)
	object := bed.state("state", 10)

	err := bed.agent.UpdateObjects([]ffi.ObjectConfig{object.AsFFIObject()})

	require.NoError(t, err)
}

// Test_StateObject_Build_Failure_ReportsItsKind verifies that a build refused
// by C, by the api or by its closure reports the kind of the failure, and none
// when the failure has none.
func Test_StateObject_Build_Failure_ReportsItsKind(t *testing.T) {
	for _, tc := range failedStateBuilds {
		t.Run(tc.name, func(t *testing.T) {
			bed := newBed(t, tc.adjust...)

			_, err := bed.buildState(tc.object, 10, tc.fault)

			if tc.wantKind == nil {
				requireUntagged(t, err)
				return
			}
			require.ErrorIs(t, err, tc.wantKind)
		})
	}
}

// Test_StateObject_Build_Failure_ReturnsMemoryToBaseline verifies that a
// failed build gives back every block it took.
func Test_StateObject_Build_Failure_ReturnsMemoryToBaseline(t *testing.T) {
	for _, tc := range failedStateBuilds {
		t.Run(tc.name, func(t *testing.T) {
			bed := newBed(t, tc.adjust...)

			_, err := bed.buildState(tc.object, 10, tc.fault)

			require.Error(t, err)
			require.Equal(t, bed.baseline, bed.agent.BlockAllocatorFreeSize())
		})
	}
}

// Test_StateObject_Build_TypeNotLoaded_NamesTheType verifies that C's refusal
// of an object type the dataplane has not loaded reaches the caller.
func Test_StateObject_Build_TypeNotLoaded_NamesTheType(t *testing.T) {
	bed := newBed(t, withoutObjects)

	_, err := bed.buildState("state", 10, cfixture.FaultNone)

	require.ErrorContains(t, err, "object type 'fixture_state' not found")
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

	_, err := object.Slot(stateSlots)

	require.ErrorIs(t, err, ffi.ErrInvalidArgument)
}

// Test_StateObject_Add_ChangesThePublishedObject verifies that the control
// plane changes a published object's state in place, through its atomics.
func Test_StateObject_Add_ChangesThePublishedObject(t *testing.T) {
	bed := newBed(t)
	object := bed.state("state", 10)
	bed.publishObjects(object)

	_, err := object.Add(1, 5)
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
	_, err := object.Add(0, 7)
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

	require.Equal(t, bed.baseline, bed.agent.BlockAllocatorFreeSize())
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
		require.NoError(t, bed.agent.DeleteObject(cfixture.StateType, "state"))

		require.NoError(t, object.Free())

		require.Equal(t, bed.baseline, bed.agent.BlockAllocatorFreeSize(), "%d workers", workers)
	}
	require.Greater(t, used[workerCount], used[1])
}

// Test_StateObject_Free_Unpublished_LeavesLoadedCountUnchanged verifies that
// the free returns the destruction hold it takes.
func Test_StateObject_Free_Unpublished_LeavesLoadedCountUnchanged(t *testing.T) {
	bed := newBed(t)
	before := dataplaneut.LoadedEntityCount(bed.agent)
	object := bed.state("state", 10)

	require.NoError(t, object.Free())

	require.Equal(t, before, dataplaneut.LoadedEntityCount(bed.agent))
}

// publishedState builds a state object and publishes it, which a generation
// holds until a later one drops it.
func publishedState(t *testing.T) (*bed, *cfixture.StateObject) {
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

	require.ErrorIs(t, object.Free(), ffi.ErrStillReferenced)
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
	held := dataplaneut.LoadedEntityCount(bed.agent)

	_ = object.Free()

	require.Equal(t, held, dataplaneut.LoadedEntityCount(bed.agent))
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

	require.ErrorIs(t, replacement.Free(), ffi.ErrStillReferenced)
}

// Test_StateObject_Free_AfterDelete_ReturnsMemoryToBaseline verifies that an
// object whose free was refused is freed once a delete drops it from the
// generation, and gives back all it took.
func Test_StateObject_Free_AfterDelete_ReturnsMemoryToBaseline(t *testing.T) {
	bed, object := publishedState(t)
	require.ErrorIs(t, object.Free(), ffi.ErrStillReferenced)
	require.NoError(t, bed.agent.DeleteObject(cfixture.StateType, "state"))

	require.NoError(t, object.Free())

	require.Equal(t, bed.baseline, bed.agent.BlockAllocatorFreeSize())
}

// Test_StateObject_Free_AfterReplaceAndDelete_ReturnsMemoryToBaseline verifies
// that both objects of a replacement are freed once neither is published.
func Test_StateObject_Free_AfterReplaceAndDelete_ReturnsMemoryToBaseline(t *testing.T) {
	bed, old := publishedState(t)
	replacement := bed.state("state", 20)
	bed.publishObjects(replacement)
	require.NoError(t, bed.agent.DeleteObject(cfixture.StateType, "state"))

	require.NoError(t, old.Free())
	require.NoError(t, replacement.Free())

	require.Equal(t, bed.baseline, bed.agent.BlockAllocatorFreeSize())
}

// Test_StateObject_Seed_ReadsTheSeed verifies that a query writes its value at
// the place the caller gave.
func Test_StateObject_Seed_ReadsTheSeed(t *testing.T) {
	bed := newBed(t)
	object := bed.state("state", 10)
	var out uint64

	require.NoError(t, object.Seed(cfixture.FaultNone, &out))

	require.Equal(t, uint64(10), out)
}

// Test_StateObject_Seed_Panic_IsUntagged verifies that a panicking query
// returns an error carrying the panic message and no kind.
func Test_StateObject_Seed_Panic_IsUntagged(t *testing.T) {
	bed := newBed(t)
	object := bed.state("state", 10)
	var out uint64

	err := object.Seed(cfixture.FaultPanic, &out)

	requireUntagged(t, err)
	require.ErrorContains(t, err, "panic: fixture fault")
}

// Test_StateObject_Seed_Failure_LeavesOutUntouched verifies that a failed
// query, a panicking one included, writes nothing at the caller's place.
func Test_StateObject_Seed_Failure_LeavesOutUntouched(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fault cfixture.Fault
	}{
		{name: "error", fault: cfixture.FaultError},
		{name: "panic", fault: cfixture.FaultPanic},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bed := newBed(t)
			object := bed.state("state", 10)
			out := uint64(0xdead)

			_ = object.Seed(tc.fault, &out)

			require.Equal(t, uint64(0xdead), out)
		})
	}
}

// Test_StateObject_Seed_NilOut_IsInvalidArgument verifies that a query with no
// place for its value is refused as an invalid argument.
func Test_StateObject_Seed_NilOut_IsInvalidArgument(t *testing.T) {
	bed := newBed(t)
	object := bed.state("state", 10)

	err := object.Seed(cfixture.FaultNone, nil)

	require.ErrorIs(t, err, ffi.ErrInvalidArgument)
}

// Test_StateObject_Free_Twice_IsNoOp verifies that a second free of a freed
// object does nothing and reports nothing.
func Test_StateObject_Free_Twice_IsNoOp(t *testing.T) {
	bed := newBed(t)
	object := bed.state("state", 10)
	require.NoError(t, object.Free())

	require.NoError(t, object.Free())
}

// Test_StateObject_Slot_AfterFree_IsInvalidArgument verifies that a query of a
// freed object is refused, as its handle is gone, and reads no memory.
func Test_StateObject_Slot_AfterFree_IsInvalidArgument(t *testing.T) {
	bed := newBed(t)
	object := bed.state("state", 10)
	require.NoError(t, object.Free())

	_, err := object.Slot(0)

	require.ErrorIs(t, err, ffi.ErrInvalidArgument)
}

// Test_StateObject_Add_AfterFree_IsInvalidArgument verifies that a change of a
// freed object is refused and writes no memory.
func Test_StateObject_Add_AfterFree_IsInvalidArgument(t *testing.T) {
	bed := newBed(t)
	object := bed.state("state", 10)
	require.NoError(t, object.Free())

	_, err := object.Add(0, 1)

	require.ErrorIs(t, err, ffi.ErrInvalidArgument)
}

// Test_StateObject_Add_Unpublished_ChangesTheSlot verifies that an object no
// generation holds is changed as a published one is.
func Test_StateObject_Add_Unpublished_ChangesTheSlot(t *testing.T) {
	bed := newBed(t)
	object := bed.state("state", 10)

	_, err := object.Add(3, 4)
	require.NoError(t, err)

	require.Equal(t, []uint64{10, 11, 12, 17}, slots(t, object))
}

// Test_StateObject_Add_Wrapping_WrapsTheSum verifies that a sum past the
// largest value wraps instead of failing.
func Test_StateObject_Add_Wrapping_WrapsTheSum(t *testing.T) {
	bed := newBed(t)
	object := bed.state("state", 10)

	sum, err := object.Add(0, ^uint64(0))

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
				if _, err := object.Add(2, 1); err != nil {
					return err
				}
			}
			return nil
		})
	}
	require.NoError(t, group.Wait())

	value, err := object.Slot(2)
	require.NoError(t, err)
	require.Equal(t, uint64(12+goroutines*adds), value)
}
