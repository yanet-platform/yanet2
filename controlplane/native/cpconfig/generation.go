//go:build linux && (amd64 || arm64)

package cpconfig

import (
	"fmt"
	"math/bits"

	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/teardown"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

const (
	// counterMaxAge is how many replacements an unregistered counter name
	// is carried through before its slot is released (COUNTER_MAX_AGE).
	counterMaxAge = 1
	// counterFlagClearSlot marks a counter placed in a released slot,
	// whose values start from zero (COUNTER_FLAG_CLEAR_SLOT).
	counterFlagClearSlot = 1
)

// logUp returns the binary logarithm of a nonzero value, rounded up.
func logUp(value uint64) int {
	return bits.Len64(value - 1)
}

func freeSlots(counters *layout.CounterRegistry, pool int) []uint64 {
	return registry.Slice(counters.FreeSlots[pool].Get(), counters.FreeCapacities[pool])
}

// freeSlotPush remembers a released value slot for a later name of the
// same pool, growing the pool's array, as counter_registry_free_slot_push
// does.
func freeSlotPush(counters *layout.CounterRegistry, pool int, offset uint64) error {
	if counters.FreeCounts[pool] == counters.FreeCapacities[pool] {
		context := counters.MemoryContext.Get()
		capacity := max(counters.FreeCapacities[pool]*2, 8)
		grown, ok := shmalloc.AllocArray[uint64](context, capacity)
		if !ok {
			return fmt.Errorf("allocate released slots of pool %d: %w", pool, shmalloc.ErrNoMemory)
		}
		old := freeSlots(counters, pool)
		copy(grown, old[:counters.FreeCounts[pool]])
		shmalloc.FreeArray(context, old)
		counters.FreeSlots[pool].Set(&grown[0])
		counters.FreeCapacities[pool] = capacity
	}
	freeSlots(counters, pool)[counters.FreeCounts[pool]] = offset
	counters.FreeCounts[pool]++
	return nil
}

// linkCounters places a registry's counters in value slots, continuing
// the registry it displaces, as counter_registry_link does.
//
// A name both have keeps its slot, a name only the displaced one has is
// carried for a while, and a new name takes a released slot or a new one.
// Without a displaced registry every name takes a new slot. The registry
// is linked once, by the update it was built for; a failed link leaves it
// linked part-way, as C does.
func linkCounters(fresh, old *layout.CounterRegistry) error {
	if fresh == old {
		return nil
	}
	if old != nil {
		if err := carryCounters(fresh, old); err != nil {
			return err
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

// carryCounters takes over the value slots of the displaced registry.
//
// Its pool counts and released slots pass on, a name both have keeps its
// slot, and a name only it has is carried or released by age.
func carryCounters(fresh, old *layout.CounterRegistry) error {
	for pool := range counterPoolSize {
		fresh.Counts[pool] = old.Counts[pool]
		// The displaced registry's released slots replace the registry's
		// own, as counter_registry_link resets them.
		fresh.FreeCounts[pool] = 0
		released := freeSlots(old, pool)
		for idx := range old.FreeCounts[pool] {
			if err := freeSlotPush(fresh, pool, released[idx]); err != nil {
				return err
			}
		}
	}
	oldNames := counterNames(old)
	for idx := range old.Count {
		carried := &oldNames[idx]
		name := shm.String(carried.Name[:])
		if index := counterLookup(fresh, name); index != registry.Invalid {
			counter := &counterNames(fresh)[index]
			if counter.Size != carried.Size {
				return fmt.Errorf("counter %q: size %d, registered as %d", name, counter.Size, carried.Size)
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
	return nil
}

// linkModuleCounters continues the counters of the module it displaces,
// as cp_module_link_counters does.
//
// Its own registry continues the displaced one's, and each tagged runtime
// registry the displaced module's registry of that tag.
func linkModuleCounters(module, old *layout.CpModule) error {
	var oldCounters *layout.CounterRegistry
	if old != nil {
		oldCounters = &old.CounterRegistry
	}
	if err := linkCounters(&module.CounterRegistry, oldCounters); err != nil {
		return fmt.Errorf("link counter registry: %w", err)
	}
	registries := runtimeRegistries(module)
	for idx := range registries {
		entry := registries[idx].Get()
		var displaced *layout.CounterRegistry
		if old != nil {
			oldRegistries := runtimeRegistries(old)
			for oldIdx := range oldRegistries {
				oldEntry := oldRegistries[oldIdx].Get()
				if shm.Equal(oldEntry.Tag[:], shm.String(entry.Tag[:])) {
					displaced = &oldEntry.Registry
					break
				}
			}
		}
		if err := linkCounters(&entry.Registry, displaced); err != nil {
			return fmt.Errorf("link counter registry %q: %w", shm.String(entry.Tag[:]), err)
		}
	}
	return nil
}

func runtimeRegistries(module *layout.CpModule) []shm.Ptr[layout.CpModuleCounterRegistry] {
	return registry.Slice(module.RuntimeCounterRegistries.Get(), module.RuntimeCounterRegistryCount)
}

// moduleKeyHash hashes a module's type and name as the module registry's
// index does.
func moduleKeyHash(moduleType, name string) uint64 {
	return registry.HashNamePair(
		registry.FieldKey(name, len(layout.CpModule{}.Name)),
		registry.FieldKey(moduleType, len(layout.CpModule{}.Type)),
	)
}

// moduleMatch matches the module of a type and name.
func moduleMatch(moduleType, name string) func(item *layout.RegistryItem) bool {
	return func(item *layout.RegistryItem) bool {
		module := registry.Container[layout.CpModule](item, 0)
		return shm.Equal(module.Name[:], registry.FieldKey(name, len(module.Name))) &&
			shm.Equal(module.Type[:], registry.FieldKey(moduleType, len(module.Type)))
	}
}

// lookupModule returns the module of a type and name in the registry, or
// nil.
func lookupModule(modules *layout.CpModuleRegistry, moduleType, name string) *layout.CpModule {
	entities := &modules.EntityRegistry.Registry
	index, found := registry.Lookup(entities, moduleKeyHash(moduleType, name), moduleMatch(moduleType, name))
	if !found {
		return nil
	}
	return registry.Container[layout.CpModule](registry.Item(entities, index), 0)
}

// upsertModule puts a module configuration into the generation's
// registry, as cp_module_registry_upsert does.
//
// It replaces the module of its type and name, whose counters it
// continues. The caller holds the instance's configuration lock and the
// module's agent current.
func (m *build) upsertModule(module *layout.CpModule) error {
	modules := &m.gen.ModuleRegistry
	moduleType, name := shm.String(module.Type[:]), shm.String(module.Name[:])
	if err := linkModuleCounters(module, lookupModule(modules, moduleType, name)); err != nil {
		return err
	}
	if !registry.ReplaceEntity(
		&modules.EntityRegistry, moduleHash, moduleKeyHash(moduleType, name),
		moduleMatch(moduleType, name), &module.Entity.ConfigItem,
	) {
		return fmt.Errorf("replace module %s:%s in registry: %w", moduleType, name, errZoneMemory)
	}
	return nil
}

// copyGeneration copies the generation the build replaces into the one
// it builds, numbered after it, as cp_config_gen_new_from does.
//
// Every registry holds the same items with one more reference each. A
// failed copy frees the generation, its registries copied part-way.
func (m *build) copyGeneration() (err error) {
	context := &m.cp.MemoryContext
	gen := newNode[layout.CpConfigGen](context, 0)
	if gen == nil {
		return fmt.Errorf("allocate generation: %w", errZoneMemory)
	}
	defer func() {
		if err != nil {
			teardown.FreeGeneration(m.cp, gen)
		}
	}()
	m.gen = gen
	old := m.old
	gen.Gen = old.Gen + 1
	// The new generation starts with the reference its publication
	// holds.
	gen.Refcnt = 1
	gen.DpConfig.Set(old.DpConfig.Get())
	gen.CpConfig.Set(old.CpConfig.Get())

	if !registry.CopyEntities(context, moduleHash, &gen.ModuleRegistry.EntityRegistry, &old.ModuleRegistry.EntityRegistry) {
		return copyFailed("module")
	}
	if !registry.Copy(context, functionHash, &gen.FunctionRegistry.Registry, &old.FunctionRegistry.Registry) {
		return copyFailed("function")
	}
	gen.FunctionRegistry.MemoryContext.Set(context)
	if !registry.Copy(context, pipelineHash, &gen.PipelineRegistry.Registry, &old.PipelineRegistry.Registry) {
		return copyFailed("pipeline")
	}
	gen.PipelineRegistry.MemoryContext.Set(context)
	if !registry.CopyEntities(context, deviceHash, &gen.DeviceRegistry.EntityRegistry, &old.DeviceRegistry.EntityRegistry) {
		return copyFailed("device")
	}
	if !registry.CopyEntities(context, objectHash, &gen.ObjectRegistry.EntityRegistry, &old.ObjectRegistry.EntityRegistry) {
		return copyFailed("object")
	}
	return nil
}

// copyFailed reports a copy that ran out of memory at a registry.
func copyFailed(kind string) error {
	return fmt.Errorf("copy %s registry: %w", kind, errZoneMemory)
}
