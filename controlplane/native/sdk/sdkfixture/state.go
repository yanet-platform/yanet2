//go:build sdk_rust_fixtures && linux && (amd64 || arm64)

package sdkfixture

import (
	"fmt"
	"sync/atomic"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

const (
	// stateRegionLen is the count of values of a worker's region of a
	// state object.
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
// agent's memory: slot n holds seed + n, wrapping, and each worker's slot,
// both values of its region and the locked count hold the seed, beside the
// object's gauge.
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
// the slot per worker, the regions, the gauge and the locked count.
func buildState(object *stateObject, workers, seed, count uint64) error {
	context := &object.Header.Entity.MemoryContext
	body := &object.Body
	body.Seed = seed
	slots, err := newSlice(context, &body.Slots, count)
	if err != nil {
		return fmt.Errorf("slots: %w", err)
	}
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
	body.Locked.Value = seed
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
	freeSlice(context, &body.Slots)
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

// StateShape is the shape of a state object, as one query reads it: its
// seed, its number of slots, and the workers and values per worker of
// its regions.
type StateShape struct {
	Seed      uint64
	Slots     uint64
	Workers   uint64
	RegionLen uint64
}

// stateOf returns the body of a state object.
func stateOf(object *layout.CpObject) *StateConfig {
	return &(*stateObject)(unsafe.Pointer(object)).Body
}

// stateSlots returns the slots of a state object.
func stateSlots(state *StateConfig) []atomic.Uint64 {
	return unsafe.Slice(state.Slots.Ptr.Get(), state.Slots.Len)
}

// stateSlot returns slot index of a state object, which may be
// published; an index past the slots is refused.
func stateSlot(object *layout.CpObject, index uint64) (*atomic.Uint64, error) {
	slots := stateSlots(stateOf(object))
	if index >= uint64(len(slots)) {
		return nil, fmt.Errorf("slot %d of a state object of %d slots: %w", index, len(slots), ErrInvalidArgument)
	}
	return &slots[index], nil
}

// StateSlot returns the value of slot index of a state object, which may
// be published.
func StateSlot(object *layout.CpObject, index uint64) (uint64, error) {
	slot, err := stateSlot(object, index)
	if err != nil {
		return 0, err
	}
	return slot.Load(), nil
}

// StateAdd adds delta to slot index of a state object, which may be
// published, and returns the sum, wrapping.
func StateAdd(object *layout.CpObject, index, delta uint64) (uint64, error) {
	slot, err := stateSlot(object, index)
	if err != nil {
		return 0, err
	}
	return slot.Add(delta), nil
}

// StateLocked returns the locked count of a state object, which may be
// published, read under its lock.
func StateLocked(object *layout.CpObject) uint64 {
	return StateLockedAdd(object, 0)
}

// StateLockedAdd adds delta to the locked count of a state object, which
// may be published, under its lock, as the stash adds to it, and returns
// the sum, wrapping.
func StateLockedAdd(object *layout.CpObject, delta uint64) uint64 {
	locked := &stateOf(object).Locked
	shm.Lock(&locked.Lock)
	defer shm.Unlock(&locked.Lock)
	locked.Value += delta
	return locked.Value
}

// StateSeed returns the seed a state object was built from.
func StateSeed(object *layout.CpObject) uint64 {
	return stateOf(object).Seed
}

// StateValues writes the values of a state object's slots, from the
// first, into as many places as values has, and returns the number of
// slots; the places past the slots end up zero.
func StateValues(object *layout.CpObject, values []uint64) uint64 {
	return readSlots(stateOf(object), values)
}

// readSlots writes the values of the slots into the places and returns
// the number of slots, zeroing the places past them.
func readSlots(state *StateConfig, values []uint64) uint64 {
	slots := stateSlots(state)
	clear(values)
	for idx := range min(len(values), len(slots)) {
		values[idx] = slots[idx].Load()
	}
	return uint64(len(slots))
}

// ReadStateShape writes the shape of a state object into shape and
// returns its number of slots, the length of the places StateValues fills
// whole.
func ReadStateShape(object *layout.CpObject, shape *StateShape) uint64 {
	state := stateOf(object)
	*shape = StateShape{
		Seed:      state.Seed,
		Slots:     state.Slots.Len,
		Workers:   state.Regions.Workers,
		RegionLen: state.Regions.Len,
	}
	return shape.Slots
}
