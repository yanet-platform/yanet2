//go:build linux && (amd64 || arm64)

package cpconfig

import (
	"bytes"
	"errors"
	"fmt"
	"math/bits"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

const (
	// counterMaxAge is how many replacements an unregistered counter
	// name is carried through before its slot is freed.
	counterMaxAge = 1
	// counterFlagClearSlot marks a counter placed in a reused slot, whose
	// values start from zero.
	counterFlagClearSlot = 1
)

// logUp returns the base-2 logarithm of value rounded up.
func logUp(value uint64) int {
	if value == 0 {
		return 0
	}
	length := bits.Len64(value)
	if value&(value-1) == 0 {
		return length - 1
	}
	return length
}

func freeSlots(registry *layout.CounterRegistry, pool int) []uint64 {
	slots := registry.FreeSlots[pool].Get()
	if slots == nil {
		return nil
	}
	return unsafe.Slice(slots, registry.FreeCapacities[pool])
}

// freeSlotPush remembers a released value slot for a later name of the
// same pool.
func freeSlotPush(registry *layout.CounterRegistry, pool int, offset uint64) error {
	if registry.FreeCounts[pool] == registry.FreeCapacities[pool] {
		capacity := max(registry.FreeCapacities[pool]*2, 8)
		context := registry.MemoryContext.Get()
		block := shmalloc.ContextAlloc(context, uintptr(capacity)*8)
		if block == nil {
			return fmt.Errorf("allocate free counter slots: %w", ErrNoMemory)
		}
		if old := freeSlots(registry, pool); old != nil {
			copy(unsafe.Slice((*uint64)(block), registry.FreeCounts[pool]), old[:registry.FreeCounts[pool]])
			shmalloc.ContextFree(context, unsafe.Pointer(&old[0]), uintptr(registry.FreeCapacities[pool])*8)
		}
		registry.FreeSlots[pool].Set((*uint64)(block))
		registry.FreeCapacities[pool] = capacity
	}
	freeSlots(registry, pool)[registry.FreeCounts[pool]] = offset
	registry.FreeCounts[pool]++
	return nil
}

// counterRegistryLink places the counters of fresh in value slots,
// continuing those of the displaced registry old (nil for none): a name
// both have keeps its slot, a name only old has is carried for a while,
// and a new name takes a released slot or a new one.
func counterRegistryLink(fresh, old *layout.CounterRegistry) error {
	if fresh == old {
		return nil
	}
	if old != nil {
		for pool := range counterPoolSize {
			fresh.Counts[pool] = old.Counts[pool]
			fresh.FreeCounts[pool] = 0
			oldSlots := freeSlots(old, pool)
			for idx := range old.FreeCounts[pool] {
				if err := freeSlotPush(fresh, pool, oldSlots[idx]); err != nil {
					return err
				}
			}
		}
		oldNames := counterNames(old)
		for idx := range old.Count {
			carried := &oldNames[idx]
			name := shm.String(carried.Name[:])
			if index := strIndexLookup(&fresh.StrIndex, name, counterNameSize, counterName(fresh)); index != strIndexInvalid {
				counter := &counterNames(fresh)[index]
				if counter.Size != carried.Size {
					return errors.New("counter redeclared with different size")
				}
				counter.Offset = carried.Offset
				continue
			}
			if carried.Age >= counterMaxAge {
				if err := freeSlotPush(fresh, logUp(carried.Size), carried.Offset); err != nil {
					return err
				}
				continue
			}
			index, err := counterInsert(fresh, name, carried.Size, carried.Age+1)
			if err != nil {
				return err
			}
			counterNames(fresh)[index].Offset = carried.Offset
		}
	}
	names := counterNames(fresh)
	for idx := range fresh.Count {
		counter := &names[idx]
		if counter.Offset != counterUnplaced {
			continue
		}
		pool := logUp(counter.Size)
		if fresh.FreeCounts[pool] > 0 {
			fresh.FreeCounts[pool]--
			counter.Offset = freeSlots(fresh, pool)[fresh.FreeCounts[pool]]
			counter.Flags |= counterFlagClearSlot
			continue
		}
		counter.Offset = fresh.Counts[pool] * (8 << pool)
		fresh.Counts[pool]++
	}
	return nil
}

// moduleEqual matches the module of a type and name.
func moduleEqual(moduleType, name string) func(item *layout.RegistryItem) bool {
	return func(item *layout.RegistryItem) bool {
		module := containerOf[layout.CpModule](item, 0)
		return shm.Equal(module.Type[:], moduleType) && shm.Equal(module.Name[:], name)
	}
}

// moduleKeyHash hashes a module key as the module registry indexes it.
func moduleKeyHash(moduleType, name string) uint64 {
	nameField := make([]byte, len(name)+1)
	copy(nameField, name)
	typeField := make([]byte, len(moduleType)+1)
	copy(typeField, moduleType)
	return registryHashNamePair(nameField, typeField)
}

// lookupModule returns the module of a type and name in the registry, or
// nil.
func lookupModule(registry *layout.CpModuleRegistry, moduleType, name string) *layout.CpModule {
	entities := &registry.EntityRegistry.Registry
	index, found := registryLookup(entities, moduleKeyHash(moduleType, name), moduleEqual(moduleType, name))
	if !found {
		return nil
	}
	return containerOf[layout.CpModule](registryGet(entities, index), 0)
}

// UpsertModule puts a module configuration into the registry, replacing
// the one of its type and name, whose counters it continues.
func UpsertModule(registry *layout.CpModuleRegistry, module *layout.CpModule) error {
	allocator := &registry.EntityRegistry.Registry.Owner.Get().BlockAllocator
	shmalloc.Lock(allocator)
	defer shmalloc.Unlock(allocator)
	return upsertModule(registry, module)
}

// upsertModule is UpsertModule for a caller holding the instance's
// allocator lock.
func upsertModule(registry *layout.CpModuleRegistry, module *layout.CpModule) error {
	moduleType, name := shm.String(module.Type[:]), shm.String(module.Name[:])
	old := lookupModule(registry, moduleType, name)
	var oldCounters *layout.CounterRegistry
	if old != nil {
		oldCounters = &old.CounterRegistry
	}
	if err := counterRegistryLink(&module.CounterRegistry, oldCounters); err != nil {
		return fmt.Errorf("link counter registry: %w", err)
	}
	if err := linkRuntimeCounters(module, old); err != nil {
		return err
	}
	if !entityRegistryReplace(&registry.EntityRegistry, moduleKeys, moduleKeyHash(moduleType, name),
		moduleEqual(moduleType, name), &module.Entity.ConfigItem,
	) {
		return fmt.Errorf("replace module %s:%s in registry: %w", moduleType, name, ErrNoMemory)
	}
	return nil
}

// linkRuntimeCounters continues each tagged runtime counter registry of
// a module from the displaced module's registry of the same tag.
func linkRuntimeCounters(module, old *layout.CpModule) error {
	registries := runtimeRegistries(module)
	for idx := range registries {
		entry := registries[idx].Get()
		var displaced *layout.CounterRegistry
		if old != nil {
			oldRegistries := runtimeRegistries(old)
			for oldIdx := range oldRegistries {
				oldEntry := oldRegistries[oldIdx].Get()
				if bytes.Equal(shm.Bytes(oldEntry.Tag[:]), shm.Bytes(entry.Tag[:])) {
					displaced = &oldEntry.Registry
					break
				}
			}
		}
		if err := counterRegistryLink(&entry.Registry, displaced); err != nil {
			return fmt.Errorf("link counter registry %q: %w", shm.String(entry.Tag[:]), err)
		}
	}
	return nil
}

func runtimeRegistries(module *layout.CpModule) []shm.Ptr[layout.CpModuleCounterRegistry] {
	first := module.RuntimeCounterRegistries.Get()
	if first == nil {
		return nil
	}
	return unsafe.Slice(first, module.RuntimeCounterRegistryCount)
}

// counterInsert adds a counter name to a registry, growing it as
// counter_registry_insert does, and returns its index.
func counterInsert(registry *layout.CounterRegistry, name string, size uint64, age uint32) (uint64, error) {
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
	counter.Age = age
	counter.Offset = counterUnplaced
	counter.Flags = 0
	registry.Count++
	return registry.Count - 1, nil
}

// NewGeneration copies a generation into a fresh one numbered after it:
// every registry holds the same items with one more reference each.
func NewGeneration(cp *layout.CpConfig, old *layout.CpConfigGen) (*layout.CpConfigGen, error) {
	shmalloc.Lock(&cp.BlockAllocator)
	defer shmalloc.Unlock(&cp.BlockAllocator)
	return newGeneration(cp, old)
}

// newGeneration is NewGeneration for a caller holding the instance's
// allocator lock.
func newGeneration(cp *layout.CpConfig, old *layout.CpConfigGen) (*layout.CpConfigGen, error) {
	block := shmalloc.ContextAllocLocked(&cp.MemoryContext, unsafe.Sizeof(layout.CpConfigGen{}))
	if block == nil {
		return nil, fmt.Errorf("allocate generation: %w", ErrNoMemory)
	}
	clear(unsafe.Slice((*byte)(block), unsafe.Sizeof(layout.CpConfigGen{})))
	gen := (*layout.CpConfigGen)(block)
	gen.Gen = old.Gen + 1
	gen.ConfigGenEctxCount = 0
	gen.ConfigGenEctxs.Set(nil)
	// The new generation starts with the reference its publication will
	// hold.
	gen.Refcnt = 1
	gen.DpConfig.Set(old.DpConfig.Get())
	gen.CpConfig.Set(old.CpConfig.Get())

	context := &cp.MemoryContext
	copies := []struct {
		Name string
		Copy func() bool
	}{
		{"module", func() bool {
			return entityRegistryCopy(context, moduleKeys, &gen.ModuleRegistry.EntityRegistry, &old.ModuleRegistry.EntityRegistry)
		}},
		{"function", func() bool {
			if !registryCopy(context, functionKeys, &gen.FunctionRegistry.Registry, &old.FunctionRegistry.Registry) {
				return false
			}
			gen.FunctionRegistry.MemoryContext.Set(context)
			return true
		}},
		{"pipeline", func() bool {
			if !registryCopy(context, pipelineKeys, &gen.PipelineRegistry.Registry, &old.PipelineRegistry.Registry) {
				return false
			}
			gen.PipelineRegistry.MemoryContext.Set(context)
			return true
		}},
		{"device", func() bool {
			return entityRegistryCopy(context, deviceKeys, &gen.DeviceRegistry.EntityRegistry, &old.DeviceRegistry.EntityRegistry)
		}},
		{"object", func() bool {
			return entityRegistryCopy(context, objectKeys, &gen.ObjectRegistry.EntityRegistry, &old.ObjectRegistry.EntityRegistry)
		}},
	}
	for _, registry := range copies {
		if !registry.Copy() {
			return nil, fmt.Errorf("copy %s registry: %w", registry.Name, ErrNoMemory)
		}
	}
	return gen, nil
}
