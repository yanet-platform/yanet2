//go:build linux && (amd64 || arm64)

package cpconfig

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// NewObject allocates an object of size bytes in the agent's memory, its
// header first and after it the body its builder lays out, as the Rust
// SDK's object builder does, and initializes the header.
func NewObject(owner *agent.Agent, objectType, name string, size uintptr) (*layout.CpObject, error) {
	if size < unsafe.Sizeof(layout.CpObject{}) {
		return nil, fmt.Errorf("an object of %d bytes is smaller than its header", size)
	}
	block := shmalloc.ContextAlloc(&owner.Agent.MemoryContext, size)
	if block == nil {
		return nil, fmt.Errorf("allocate object %s:%s: %w", objectType, name, ErrNoMemory)
	}
	object := (*layout.CpObject)(block)
	if err := InitObject(object, owner, objectType, name); err != nil {
		shmalloc.ContextFree(&owner.Agent.MemoryContext, block, size)
		return nil, err
	}
	return object, nil
}

// InitObject prepares the header every object opens with, as
// cp_object_init does: the dataplane object type, its names, its own
// memory context under the agent's and its two counter registries, the
// object's own and the one its links spawn from. The object starts
// dangling: only an update references it.
func InitObject(object *layout.CpObject, owner *agent.Agent, objectType, name string) error {
	*object = layout.CpObject{}
	index, ok := lookupDataplaneObject(owner.Instance.Dp, objectType)
	if !ok {
		return fmt.Errorf("object type %q not found in dataplane config", objectType)
	}
	object.DpObjectIdx = index
	shm.CopyString(object.Type[:], objectType)
	shm.CopyString(object.Name[:], name)

	object.Entity.ConfigItem.Refcnt = 0
	object.Entity.Agent.Set(owner.Agent)
	shmalloc.ContextInitFrom(&object.Entity.MemoryContext, &owner.Agent.MemoryContext, name)
	counterRegistryInit(&object.CounterRegistry, &object.Entity.MemoryContext)
	counterRegistryInit(&object.LinkCounterRegistry, &object.Entity.MemoryContext)
	return nil
}

// lookupDataplaneObject returns the index of the dataplane's object type
// of that name.
func lookupDataplaneObject(dp *layout.DpConfig, name string) (uint64, bool) {
	first := dp.DpObjects.Get()
	if first == nil {
		return 0, false
	}
	for idx, object := range unsafe.Slice(first, dp.ObjectCount) {
		if shm.Equal(object.Name[:], name) {
			return uint64(idx), true
		}
	}
	return 0, false
}

// LinkObject returns the index of the object of a type and name among
// the objects a module links, appending it when missing, as
// cp_module_link_object does.
func LinkObject(module *layout.CpModule, objectType, name string) (uint64, error) {
	linkSize := unsafe.Sizeof(layout.CpModuleObject{})
	if first := module.Objects.Get(); first != nil {
		for idx, linked := range unsafe.Slice(first, module.ObjectCount) {
			if shm.Equal(linked.Type[:], objectType) && shm.Equal(linked.Name[:], name) {
				return uint64(idx), nil
			}
		}
	}
	grown := shmalloc.ContextRealloc(&module.Entity.MemoryContext,
		unsafe.Pointer(module.Objects.Get()),
		linkSize*uintptr(module.ObjectCount), linkSize*uintptr(module.ObjectCount+1),
	)
	if grown == nil {
		return 0, fmt.Errorf("reallocate objects of module %s:%s: %w",
			shm.String(module.Type[:]), shm.String(module.Name[:]), ErrNoMemory,
		)
	}
	links := unsafe.Slice((*layout.CpModuleObject)(grown), module.ObjectCount+1)
	shm.CopyString(links[module.ObjectCount].Type[:], objectType)
	shm.CopyString(links[module.ObjectCount].Name[:], name)
	module.Objects.Set(&links[0])
	index := module.ObjectCount
	module.ObjectCount++
	return index, nil
}

// RegisterCounter registers a counter of size values in a module's or
// an object's counter registry, as counter_registry_register does, and
// returns its index; a name registered already returns its index again.
func RegisterCounter(registry *layout.CounterRegistry, name string, size uint64) (uint64, error) {
	return counterRegister(registry, name, size)
}

// objectEqual matches the object of a type and name.
func objectEqual(objectType, name string) func(item *layout.RegistryItem) bool {
	return func(item *layout.RegistryItem) bool {
		object := containerOf[layout.CpObject](item, 0)
		return shm.Equal(object.Name[:], name) && shm.Equal(object.Type[:], objectType)
	}
}

// upsertObject puts an object into the registry, replacing the one of
// its type and name, whose counters and link counters it continues, as
// cp_object_registry_upsert does; the caller holds the instance's
// allocator lock.
func upsertObject(registry *layout.CpObjectRegistry, object *layout.CpObject) error {
	objectType, name := shm.String(object.Type[:]), shm.String(object.Name[:])
	entities := &registry.EntityRegistry.Registry
	var oldCounters, oldLinkCounters *layout.CounterRegistry
	if index, found := registryLookup(entities, hashStringPair(name, objectType), objectEqual(objectType, name)); found {
		old := containerOf[layout.CpObject](registryGet(entities, index), 0)
		oldCounters, oldLinkCounters = &old.CounterRegistry, &old.LinkCounterRegistry
	}
	if err := counterRegistryLink(&object.CounterRegistry, oldCounters); err != nil {
		return fmt.Errorf("link counter registry: %w", err)
	}
	if err := counterRegistryLink(&object.LinkCounterRegistry, oldLinkCounters); err != nil {
		return fmt.Errorf("link link counter registry: %w", err)
	}
	if !entityRegistryReplace(&registry.EntityRegistry, objectKeys, hashStringPair(name, objectType),
		objectEqual(objectType, name), &object.Entity.ConfigItem,
	) {
		return fmt.Errorf("replace object %s:%s in registry: %w", objectType, name, ErrNoMemory)
	}
	return nil
}

// UpdateObjects publishes a generation in which the objects replace
// those of their types and names, as cp_config_update_objects does, and
// returns once every worker runs it.
func UpdateObjects(instance *zone.Instance, objects []*layout.CpObject, options ...UpdateOption) error {
	return publishGeneration(instance, options, func(cp *layout.CpConfig, dp *layout.DpConfig, old *layout.CpConfigGen) (*layout.CpConfigGen, error) {
		owners, err := holdEntities(cp, "object", len(objects), func(idx int) entityRef {
			object := objects[idx]
			return entityRef{Owner: object.Entity.Agent.Get(), Type: shm.String(object.Type[:]), Name: shm.String(object.Name[:])}
		})
		if err != nil {
			return nil, err
		}
		defer releaseOwners(owners)
		return buildFrom(cp, dp, old, func(gen *layout.CpConfigGen) error {
			for _, object := range objects {
				if err := upsertObject(&gen.ObjectRegistry, object); err != nil {
					return fmt.Errorf("upsert object %s:%s: %w", shm.String(object.Type[:]), shm.String(object.Name[:]), err)
				}
			}
			return nil
		})
	})
}

// ErrObjectLinked marks a delete of an object a module of the published
// generation links: the module is updated or deleted first.
var ErrObjectLinked = errors.New("object is linked by a module")

// DeleteObject publishes a generation without the object of a type and
// name, as cp_config_delete_object does, and returns once every worker
// runs it; it refuses an object a module of the published generation
// links, whose workers' contexts would not build without it.
func DeleteObject(instance *zone.Instance, objectType, name string, options ...UpdateOption) error {
	return publishGeneration(instance, options, func(cp *layout.CpConfig, dp *layout.DpConfig, old *layout.CpConfigGen) (*layout.CpConfigGen, error) {
		if _, found := lookupObject(old, objectType, name); !found {
			return nil, fmt.Errorf("object %s:%s: %w", objectType, name, ErrNotFound)
		}
		if module := linkingModule(old, objectType, name); module != nil {
			return nil, fmt.Errorf("object %s:%s, linked by module %s:%s: %w",
				objectType, name, shm.String(module.Type[:]), shm.String(module.Name[:]), ErrObjectLinked)
		}
		return buildFrom(cp, dp, old, func(gen *layout.CpConfigGen) error {
			if !entityRegistryReplace(&gen.ObjectRegistry.EntityRegistry, objectKeys, hashStringPair(name, objectType),
				objectEqual(objectType, name), nil,
			) {
				return fmt.Errorf("object %s:%s: %w", objectType, name, ErrNotFound)
			}
			return nil
		})
	})
}

// linkingModule returns a module of a generation that links the object
// of a type and name, or nil.
func linkingModule(gen *layout.CpConfigGen, objectType, name string) *layout.CpModule {
	registry := &gen.ModuleRegistry.EntityRegistry.Registry
	for idx := range registry.Capacity {
		item := registryGet(registry, idx)
		if item == nil {
			continue
		}
		module := containerOf[layout.CpModule](item, 0)
		if module.ObjectCount == 0 {
			continue
		}
		for _, linked := range unsafe.Slice(module.Objects.Get(), module.ObjectCount) {
			if shm.Equal(linked.Type[:], objectType) && shm.Equal(linked.Name[:], name) {
				return module
			}
		}
	}
	return nil
}
