//go:build sdk_rust_fixtures && linux && (amd64 || arm64)

package sdkfixture

import (
	"errors"
	"fmt"
	"sync/atomic"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// ErrInvalidArgument marks a request no memory state satisfies: a part
// larger than the largest block.
var ErrInvalidArgument = errors.New("invalid argument")

const (
	// stateRegionLen is the count of values of a worker's region of a
	// state object, as the fixture api builds one.
	stateRegionLen = 2
	// heldName and heldValues are the state object's gauge: its name in
	// the object's own registry and its one value.
	heldName   = "held"
	heldValues = 1
)

// The lines of a slot or a region of a per-worker field, as the SDK lays
// them out: each starts on a line of its own, and its block takes the
// most that line may lie past the block's start.
const (
	slotLine = 128
	// lineSlack is that most: none where the allocator aligns a block of
	// whole lines to a line, the red zone where a sanitizer build shifts
	// it.
	lineSlack = shmalloc.RedZone
)

// stateObject is the block of a state object: the object header, then
// the body.
type stateObject struct {
	Header layout.CpObject
	Body   StateConfig
}

// NewStateObject builds a state object named name of count slots in the
// agent's memory, as the fixture api's build of the state object does:
// slot n holds seed + n, wrapping, and each worker's slot and both
// values of its region hold the seed, beside the object's gauge.
//
// A name its field cannot hold, and slots past the largest block, are
// refused before anything is allocated. A failure frees what the build
// took through the object's own free; an exhausted agent fails with the
// allocator's out-of-memory error.
func NewStateObject(owner *agent.Agent, name string, seed, count uint64) (*layout.CpObject, error) {
	if err := cpconfig.CheckObjectNames(StateType, name); err != nil {
		return nil, err
	}
	if count > uint64(shmalloc.MaxSize)/uint64(unsafe.Sizeof(atomic.Uint64{})) {
		return nil, fmt.Errorf("state object %q of %d slots, past the largest block: %w", name, count, ErrInvalidArgument)
	}

	context := &owner.Agent.MemoryContext
	object := shmalloc.AllocNode[stateObject](context, 0)
	if object == nil {
		return nil, fmt.Errorf("allocate state object %q: %w", name, shmalloc.ErrNoMemory)
	}
	object.Body = StateConfig{}
	if err := cpconfig.InitObject(&object.Header, owner, StateType, name); err != nil {
		shmalloc.FreeNode(context, object, 0)
		return nil, err
	}
	if err := buildState(object, owner.Instance.Dp.WorkerCount, seed, count); err != nil {
		destroyState(object)
		return nil, fmt.Errorf("build state object %q: %w", name, err)
	}
	return &object.Header, nil
}

// FreeStateObject frees a state object no generation holds.
//
// One a generation holds is refused as still referenced, and nothing is
// freed.
func FreeStateObject(object *layout.CpObject) error {
	if err := cpconfig.TryDestroyObject(object); err != nil {
		return err
	}
	owner := object.Entity.Agent.Get()
	destroyState((*stateObject)(unsafe.Pointer(object)))
	cpconfig.DestroyReleased(owner)
	return nil
}

// buildState builds the body in the SDK's order: the seed, the slots,
// the slot per worker, the regions and the gauge.
func buildState(object *stateObject, workers, seed, count uint64) error {
	context := &object.Header.Entity.MemoryContext
	body := &object.Body
	body.Seed = seed
	slots, ok := shmalloc.AllocArray[atomic.Uint64](context, count)
	if !ok {
		return fmt.Errorf("slots: %w", shmalloc.ErrNoMemory)
	}
	body.Slots.Ptr.Set(unsafe.SliceData(slots))
	body.Slots.Len = count
	for idx := range slots {
		slots[idx].Store(seed + uint64(idx))
	}
	if err := newSlots(context, &body.Workers, workers, seed); err != nil {
		return fmt.Errorf("worker slots: %w", err)
	}
	if err := newRegions(context, &body.Regions, workers, stateRegionLen, seed); err != nil {
		return fmt.Errorf("worker regions: %w", err)
	}
	id, err := cpconfig.RegisterCounter(&object.Header.CounterRegistry, heldName, heldValues)
	if err != nil {
		return fmt.Errorf("gauge: %w", err)
	}
	body.Held.ID = id
	return nil
}

// slotStride returns the bytes from one worker's slot of T to the next:
// the value in whole lines.
func slotStride[T any]() uint64 {
	return (uint64(unsafe.Sizeof(*new(T))) + slotLine - 1) / slotLine * slotLine
}

// slotBlock returns the bytes of the block of the slots of that many
// workers: the slots and the slack.
func slotBlock[T any](workers uint64) uintptr {
	return uintptr(workers*slotStride[T]() + lineSlack)
}

// regionBlock returns the bytes of a worker's region block of count
// values, none for no values: the values in whole lines and the slack.
func regionBlock[T any](count uint64) uintptr {
	values := count * uint64(unsafe.Sizeof(*new(T)))
	if values == 0 {
		return 0
	}
	return uintptr((values+slotLine-1)/slotLine*slotLine + lineSlack)
}

// firstLine returns the first line at or past the start of a block.
func firstLine(block unsafe.Pointer) unsafe.Pointer {
	return unsafe.Add(block, (slotLine-uintptr(block)%slotLine)%slotLine)
}

// newSlots writes a per-worker field of one slot per worker, each holding
// init, as the SDK lays one out: one block, its first line the first
// worker's slot, each next worker's slot a stride further.
//
// A field of no workers holds no block.
func newSlots[T any](context *layout.MemoryContext, field *PerWorker[T], workers uint64, init T) error {
	*field = PerWorker[T]{}
	if workers == 0 {
		return nil
	}
	block := shmalloc.ContextAlloc(context, slotBlock[T](workers))
	if block == nil {
		return shmalloc.ErrNoMemory
	}
	field.Slots.Set((*T)(block))
	field.Workers = workers
	first := firstLine(block)
	for worker := range workers {
		*(*T)(unsafe.Add(first, worker*slotStride[T]())) = init
	}
	return nil
}

// newRegions writes a per-worker slice field of count values per worker,
// each holding init, as the SDK lays one out: a table of each worker's
// region, as its offset from the table's entry, and the regions, each a
// block of its own whose values start on its first line.
//
// A field of no values, or of no workers, holds no table. A failure
// leaves what it allocated in the field for its free.
func newRegions[T any](context *layout.MemoryContext, field *PerWorkerSlice[T], workers, count uint64, init T) error {
	region := regionBlock[T](count)
	*field = PerWorkerSlice[T]{Workers: workers}
	if region == 0 || workers == 0 {
		return nil
	}
	table, ok := shmalloc.AllocArray[int64](context, workers)
	if !ok {
		return shmalloc.ErrNoMemory
	}
	clear(table)
	field.Regions.Set(&table[0])
	field.Len = count
	for worker := range table {
		block := shmalloc.ContextAlloc(context, region)
		if block == nil {
			return shmalloc.ErrNoMemory
		}
		table[worker] = int64(uintptr(block) - uintptr(unsafe.Pointer(&table[worker])))
		values := unsafe.Slice((*T)(firstLine(block)), count)
		for idx := range values {
			values[idx] = init
		}
	}
	return nil
}

// destroyState frees what the body holds in the SDK's release order,
// field by field, then the header and the block.
func destroyState(object *stateObject) {
	context := &object.Header.Entity.MemoryContext
	body := &object.Body
	shmalloc.FreeArray(context, unsafe.Slice(body.Slots.Ptr.Get(), body.Slots.Len))
	freeSlots(context, &body.Workers)
	freeRegions(context, &body.Regions)

	owner := object.Header.Entity.Agent.Get()
	cpconfig.FiniObject(&object.Header)
	shmalloc.FreeNode(&owner.MemoryContext, object, 0)
}

// freeSlots frees the block of a per-worker field's slots.
func freeSlots[T any](context *layout.MemoryContext, field *PerWorker[T]) {
	shmalloc.ContextFree(context, unsafe.Pointer(field.Slots.Get()), slotBlock[T](field.Workers))
}

// freeRegions frees each worker's region, the last first, then the
// table.
func freeRegions[T any](context *layout.MemoryContext, field *PerWorkerSlice[T]) {
	table := field.Regions.Get()
	if table == nil {
		return
	}
	region := regionBlock[T](field.Len)
	entries := unsafe.Slice(table, field.Workers)
	for worker := len(entries) - 1; worker >= 0; worker-- {
		if entries[worker] != 0 {
			shmalloc.ContextFree(context, unsafe.Add(unsafe.Pointer(&entries[worker]), entries[worker]), region)
		}
	}
	shmalloc.FreeArray(context, entries)
}
