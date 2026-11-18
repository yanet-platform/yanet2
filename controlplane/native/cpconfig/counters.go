//go:build linux && (amd64 || arm64)

package cpconfig

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

const (
	// counterPoolSize is the number of counter value sizes a registry
	// pools (COUNTER_POOL_SIZE).
	counterPoolSize = len(layout.CounterRegistry{}.Counts)
	// counterMaxSize is the largest number of values one counter holds.
	counterMaxSize = 1 << (counterPoolSize - 1)
	// counterNameSize is the capacity of a counter name, counting its
	// terminating zero (COUNTER_NAME_LEN).
	counterNameSize = len(layout.Counter{}.Name)
	// counterUnplaced is the value offset of a counter not linked yet.
	counterUnplaced = ^uint64(0)
)

// ErrNoMemory reports that an agent's memory is exhausted.
var ErrNoMemory = errors.New("agent memory exhausted")

func counterRegistryInit(registry *layout.CounterRegistry, context *layout.MemoryContext) {
	registry.MemoryContext.Set(context)
	registry.Count = 0
	for idx := range counterPoolSize {
		registry.Counts[idx] = 0
		registry.FreeSlots[idx].Set(nil)
		registry.FreeCounts[idx] = 0
		registry.FreeCapacities[idx] = 0
	}
	registry.Capacity = 0
	registry.Names.Set(nil)
	strIndexInit(&registry.StrIndex, context)
}

func counterNames(registry *layout.CounterRegistry) []layout.Counter {
	names := registry.Names.Get()
	if names == nil {
		return nil
	}
	return unsafe.Slice(names, registry.Capacity)
}

// counterName returns the name of the counter at index.
func counterName(registry *layout.CounterRegistry) func(index uint32) []byte {
	return func(index uint32) []byte {
		return shm.Bytes(counterNames(registry)[index].Name[:])
	}
}

func counterRegistryExpand(registry *layout.CounterRegistry, capacity uint64) error {
	context := registry.MemoryContext.Get()
	counterSize := unsafe.Sizeof(layout.Counter{})
	block := shmalloc.ContextAlloc(context, counterSize*uintptr(capacity))
	if block == nil {
		return fmt.Errorf("allocate counter names: %w", ErrNoMemory)
	}
	old := registry.Names.Get()
	oldCapacity := registry.Capacity
	if oldCapacity > 0 {
		copy(unsafe.Slice((*byte)(block), counterSize*uintptr(oldCapacity)),
			unsafe.Slice((*byte)(unsafe.Pointer(old)), counterSize*uintptr(oldCapacity)),
		)
	}
	registry.Names.Set((*layout.Counter)(block))
	registry.Capacity = capacity
	shmalloc.ContextFree(context, unsafe.Pointer(old), counterSize*uintptr(oldCapacity))
	return nil
}

// counterRegister registers a counter of size values, or returns the
// index of the counter already registered under the name.
func counterRegister(registry *layout.CounterRegistry, name string, size uint64) (uint64, error) {
	if size == 0 {
		return counterUnplaced, errors.New("zero size")
	}
	if size > counterMaxSize {
		return counterUnplaced, fmt.Errorf("counter size %d exceeds max %d", size, counterMaxSize)
	}
	if len(strIndexKey(name, counterNameSize)) == counterNameSize {
		return counterUnplaced, fmt.Errorf("name length exceeds max %d", counterNameSize-1)
	}
	if index := strIndexLookup(&registry.StrIndex, name, counterNameSize, counterName(registry)); index != strIndexInvalid {
		counter := &counterNames(registry)[index]
		// Registering a carried name again restarts its age.
		counter.Age = 0
		if counter.Size != size {
			return counterUnplaced, errors.New("counter redeclared with different size")
		}
		return uint64(index), nil
	}

	if registry.Count >= registry.Capacity {
		if err := counterRegistryExpand(registry, max(registry.Capacity*2, 8)); err != nil {
			return counterUnplaced, err
		}
	}
	if !strIndexInsert(&registry.StrIndex, name, counterNameSize, uint32(registry.Count), counterName(registry)) {
		return counterUnplaced, fmt.Errorf("insert counter index: %w", ErrNoMemory)
	}
	counter := &counterNames(registry)[registry.Count]
	shm.CopyString(counter.Name[:], name)
	counter.Size = size
	counter.Age = 0
	counter.Offset = counterUnplaced
	counter.Flags = 0
	registry.Count++
	return registry.Count - 1, nil
}
