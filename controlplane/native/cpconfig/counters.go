//go:build linux && (amd64 || arm64)

package cpconfig

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
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
	// terminating NUL (COUNTER_NAME_LEN).
	counterNameSize = len(layout.Counter{}.Name)
	// counterUnplaced is the value offset of a counter not linked yet.
	counterUnplaced = ^uint64(0)
)

var (
	// errAgentMemory reports that an agent's arenas have no room left.
	errAgentMemory = fmt.Errorf("agent memory: %w", shmalloc.ErrNoMemory)
	// errZoneMemory reports that the control-plane zone has no room left.
	errZoneMemory = fmt.Errorf("control-plane zone: %w", shmalloc.ErrNoMemory)
)

func counterRegistryInit(counters *layout.CounterRegistry, context *layout.MemoryContext) {
	counters.MemoryContext.Set(context)
	counters.Count = 0
	for idx := range counterPoolSize {
		counters.Counts[idx] = 0
		counters.FreeSlots[idx].Set(nil)
		counters.FreeCounts[idx] = 0
		counters.FreeCapacities[idx] = 0
	}
	counters.Capacity = 0
	counters.Names.Set(nil)
	registry.StrInit(&counters.StrIndex, context)
}

func counterNames(counters *layout.CounterRegistry) []layout.Counter {
	return registry.Slice(counters.Names.Get(), counters.Capacity)
}

// counterName returns the reader of a registry's names its string index
// compares keys with.
func counterName(counters *layout.CounterRegistry) func(index uint32) []byte {
	return func(index uint32) []byte {
		return shm.Bytes(counterNames(counters)[index].Name[:])
	}
}

// counterLookup returns the index of the counter of that name, or the
// invalid index.
func counterLookup(counters *layout.CounterRegistry, name string) uint32 {
	return registry.StrLookup(&counters.StrIndex, name, counterNameSize, counterName(counters))
}

// counterRegistryExpand moves a registry's names to an array of that
// capacity, as counter_registry_expand does.
func counterRegistryExpand(counters *layout.CounterRegistry, capacity uint64) error {
	context := counters.MemoryContext.Get()
	grown, ok := shmalloc.AllocArray[layout.Counter](context, capacity)
	if !ok {
		return fmt.Errorf("allocate counter names: %w", shmalloc.ErrNoMemory)
	}
	old := counterNames(counters)
	copy(grown, old)
	counters.Names.Set(&grown[0])
	counters.Capacity = capacity
	shmalloc.FreeArray(context, old)
	return nil
}

// counterInsert adds a counter name to a registry, growing it as
// counter_registry_insert does, and returns its index.
func counterInsert(counters *layout.CounterRegistry, name string, size uint64, age uint32) (uint64, error) {
	if counters.Count >= counters.Capacity {
		if err := counterRegistryExpand(counters, max(counters.Capacity*2, 8)); err != nil {
			return counterUnplaced, err
		}
	}
	if !registry.StrInsert(&counters.StrIndex, name, counterNameSize, uint32(counters.Count), counterName(counters)) {
		return counterUnplaced, fmt.Errorf("index counter %q: %w", name, shmalloc.ErrNoMemory)
	}
	counter := &counterNames(counters)[counters.Count]
	shm.CopyString(counter.Name[:], name)
	counter.Size = size
	counter.Age = age
	counter.Offset = counterUnplaced
	counter.Flags = 0
	counters.Count++
	return counters.Count - 1, nil
}

// RegisterCounter registers a counter of that many values in a module's
// or an object's counter registry, as counter_registry_register does.
//
// It returns the counter's index. A name registered already restarts its
// age and returns its index, or is refused when its size differs.
func RegisterCounter(counters *layout.CounterRegistry, name string, size uint64) (uint64, error) {
	switch {
	case size == 0:
		return counterUnplaced, fmt.Errorf("counter %q: zero size", name)
	case size > counterMaxSize:
		return counterUnplaced, fmt.Errorf("counter %q: size %d, at most %d", name, size, counterMaxSize)
	}
	if err := checkNames("counter", nameField{"name", name, counterNameSize}); err != nil {
		return counterUnplaced, err
	}
	if index := counterLookup(counters, name); index != registry.Invalid {
		counter := &counterNames(counters)[index]
		// Registering a carried name again restarts its age.
		counter.Age = 0
		if counter.Size != size {
			return counterUnplaced, fmt.Errorf("counter %q: size %d, registered as %d", name, size, counter.Size)
		}
		return uint64(index), nil
	}
	return counterInsert(counters, name, size, 0)
}
