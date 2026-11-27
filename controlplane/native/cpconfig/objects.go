//go:build linux && (amd64 || arm64)

package cpconfig

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/teardown"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// InitObject prepares the header every object opens with, as
// cp_object_init does.
//
// The header names the dataplane object type it configures and itself.
// It holds its own memory context, a child of the agent's, and two
// counter registries: the object's own and the one each module link
// spawns its counters from. A type or name its field cannot hold whole is
// refused before the header is touched. No generation references the
// object until an update publishes it.
func InitObject(object *layout.CpObject, owner *agent.Agent, objectType, name string) error {
	if err := CheckObjectNames(objectType, name); err != nil {
		return err
	}
	*object = layout.CpObject{}
	index, ok := lookupDataplaneObject(owner.Instance.Dp, objectType)
	if !ok {
		return fmt.Errorf("object type %q not found in dataplane config", objectType)
	}
	object.DpObjectIdx = index
	shm.CopyString(object.Type[:], objectType)
	shm.CopyString(object.Name[:], name)

	object.Entity.Agent.Set(owner.Agent)
	shmalloc.ContextInitFrom(&object.Entity.MemoryContext, &owner.Agent.MemoryContext, name)
	counterRegistryInit(&object.CounterRegistry, &object.Entity.MemoryContext)
	counterRegistryInit(&object.LinkCounterRegistry, &object.Entity.MemoryContext)
	return nil
}

// FiniObject frees what an object header holds and unlinks its memory
// context from the agent's, as cp_object_fini does.
//
// An object's typed free runs it once the object's own body is freed and
// before the object's block is returned to the agent.
func FiniObject(object *layout.CpObject) {
	teardown.FiniCounterRegistry(&object.LinkCounterRegistry)
	teardown.FiniCounterRegistry(&object.CounterRegistry)
	object.Entity.Agent.Set(nil)
	shmalloc.ContextFini(&object.Entity.MemoryContext)
}

// CheckObjectNames refuses an object type or name its field cannot hold
// whole.
//
// A typed build checks them before it allocates its object, so a refused
// name leaves the agent untouched.
func CheckObjectNames(objectType, name string) error {
	return checkNames(
		"object",
		nameField{"type", objectType, len(layout.CpObject{}.Type)},
		nameField{"name", name, len(layout.CpObject{}.Name)},
	)
}

// lookupDataplaneObject returns the index of the dataplane's object type
// of that name.
func lookupDataplaneObject(dp *layout.DpConfig, name string) (uint64, bool) {
	for idx, object := range registry.Slice(dp.DpObjects.Get(), dp.ObjectCount) {
		if shm.Equal(object.Name[:], name) {
			return uint64(idx), true
		}
	}
	return 0, false
}

// LinkObject returns the index of the object of a type and name among
// those the module links, appending it when missing, as
// cp_module_link_object does.
//
// The module's handler reaches the object's context at that index. A
// type or name its field cannot hold whole is refused before the module
// is touched.
func LinkObject(module *layout.CpModule, objectType, name string) (uint64, error) {
	err := checkNames(
		"linked object",
		nameField{"type", objectType, len(layout.CpModuleObject{}.Type)},
		nameField{"name", name, len(layout.CpModuleObject{}.Name)},
	)
	if err != nil {
		return 0, err
	}
	links := registry.Slice(module.Objects.Get(), module.ObjectCount)
	for idx := range links {
		if shm.Equal(links[idx].Type[:], objectType) && shm.Equal(links[idx].Name[:], name) {
			return uint64(idx), nil
		}
	}
	context := &module.Entity.MemoryContext
	grown, ok := shmalloc.AllocArray[layout.CpModuleObject](context, module.ObjectCount+1)
	if !ok {
		return 0, fmt.Errorf(
			"grow objects of module %s:%s: %w",
			shm.String(module.Type[:]), shm.String(module.Name[:]), errAgentMemory,
		)
	}
	copy(grown, links)
	shmalloc.FreeArray(context, links)
	index := module.ObjectCount
	shm.CopyString(grown[index].Type[:], objectType)
	shm.CopyString(grown[index].Name[:], name)
	module.Objects.Set(&grown[0])
	module.ObjectCount++
	return index, nil
}

// UpdateObjects publishes a generation in which the objects replace those
// of their types and names, as cp_config_update_objects does.
//
// It returns once every worker runs the generation, with no timeout, as C
// does. A nil object, an object whose agent was superseded and an object
// named twice are refused, and nothing is published. The caller passes
// objects built for the update; one whose update failed is freed, not
// passed again, and only the object currently published may be passed
// again.
func UpdateObjects(instance *zone.Instance, objects []*layout.CpObject, options ...UpdateOption) error {
	for idx, object := range objects {
		if object == nil {
			return fmt.Errorf("object %d of the update is nil", idx)
		}
	}
	return publish(instance, options, change{
		Kind:  "object",
		Count: len(objects),
		Entity: func(idx int) entityRef {
			object := objects[idx]
			return entityRef{
				Owner: object.Entity.Agent.Get(),
				Type:  shm.String(object.Type[:]),
				Name:  shm.String(object.Name[:]),
			}
		},
		Put: func(m *build) error {
			for _, object := range objects {
				if err := m.upsertObject(object); err != nil {
					return fmt.Errorf("upsert object %s:%s: %w", shm.String(object.Type[:]), shm.String(object.Name[:]), err)
				}
			}
			return nil
		},
	})
}

// objectKeyHash hashes an object's type and name as the object registry's
// index does.
func objectKeyHash(objectType, name string) uint64 {
	return registry.HashNamePair(
		registry.FieldKey(name, len(layout.CpObject{}.Name)),
		registry.FieldKey(objectType, len(layout.CpObject{}.Type)),
	)
}

// objectMatch matches the object of a type and name.
func objectMatch(objectType, name string) func(item *layout.RegistryItem) bool {
	return func(item *layout.RegistryItem) bool {
		object := registry.Container[layout.CpObject](item, 0)
		return shm.Equal(object.Name[:], registry.FieldKey(name, len(object.Name))) &&
			shm.Equal(object.Type[:], registry.FieldKey(objectType, len(object.Type)))
	}
}

// upsertObject puts an object into the generation's registry, as
// cp_object_registry_upsert does.
//
// It replaces the object of its type and name, whose counters and link
// counters it continues. The caller holds the instance's configuration
// lock and the object's agent current.
func (m *build) upsertObject(object *layout.CpObject) error {
	objects := &m.gen.ObjectRegistry.EntityRegistry
	objectType, name := shm.String(object.Type[:]), shm.String(object.Name[:])
	var oldCounters, oldLinkCounters *layout.CounterRegistry
	if index, found := lookupObject(m.gen, objectType, name); found {
		old := registry.Container[layout.CpObject](registry.Item(&objects.Registry, index), 0)
		oldCounters, oldLinkCounters = &old.CounterRegistry, &old.LinkCounterRegistry
	}
	if err := linkCounters(&object.CounterRegistry, oldCounters); err != nil {
		return fmt.Errorf("link counter registry: %w", err)
	}
	if err := linkCounters(&object.LinkCounterRegistry, oldLinkCounters); err != nil {
		return fmt.Errorf("link the link counter registry: %w", err)
	}
	if !registry.ReplaceEntity(
		objects, objectHash, objectKeyHash(objectType, name),
		objectMatch(objectType, name), &object.Entity.ConfigItem,
	) {
		return fmt.Errorf("replace object %s:%s in registry: %w", objectType, name, errZoneMemory)
	}
	return nil
}

// DeleteObject publishes a generation without the object of a type and
// name, as cp_config_delete_object does.
//
// It returns once every worker runs the generation. A type or name its
// field cannot hold whole is refused before the instance is touched. An
// object the published generation does not hold is refused as not found,
// and one a published module links as linked, before anything is built.
func DeleteObject(instance *zone.Instance, objectType, name string, options ...UpdateOption) error {
	if err := CheckObjectNames(objectType, name); err != nil {
		return err
	}
	return publish(instance, options, change{
		Check: func(old *layout.CpConfigGen) error {
			if _, found := lookupObject(old, objectType, name); !found {
				return fmt.Errorf("object %s:%s: %w", objectType, name, ErrNotFound)
			}
			if module := linkingModule(old, objectType, name); module != nil {
				return fmt.Errorf(
					"object %s:%s: %w: module %s:%s",
					objectType, name, ErrObjectLinked, shm.String(module.Type[:]), shm.String(module.Name[:]),
				)
			}
			return nil
		},
		Put: func(m *build) error {
			if !registry.ReplaceEntity(
				&m.gen.ObjectRegistry.EntityRegistry, objectHash, objectKeyHash(objectType, name),
				objectMatch(objectType, name), nil,
			) {
				return fmt.Errorf("delete object %s:%s from registry", objectType, name)
			}
			return nil
		},
	})
}

// linkingModule returns the first module of the generation that links
// the object of a type and name, or nil.
func linkingModule(gen *layout.CpConfigGen, objectType, name string) *layout.CpModule {
	for _, item := range registry.Items(&gen.ModuleRegistry.EntityRegistry.Registry) {
		module := registry.Container[layout.CpModule](item, 0)
		links := registry.Slice(module.Objects.Get(), module.ObjectCount)
		for idx := range links {
			if shm.Equal(links[idx].Type[:], objectType) && shm.Equal(links[idx].Name[:], name) {
				return module
			}
		}
	}
	return nil
}
