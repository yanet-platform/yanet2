//go:build linux && (amd64 || arm64)

package cpconfig

import (
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// ErrStillReferenced marks a destroy of a module configuration or an
// object a live generation still holds: nothing is freed, and the destroy
// succeeds once the generations holding it are retired.
var ErrStillReferenced = errors.New("still referenced by a live generation")

// DestroyModule frees a module configuration of size bytes, allocated in
// its agent's memory, once no live generation holds it, as a module's
// typed free does: cp_module_try_destroy, then freeBody, which frees
// what the module's builder allocated for the body, then cp_module_fini
// and the block, then agent_destroy_released.
//
// It reports ErrStillReferenced, freeing nothing, while a live generation
// holds the configuration.
func DestroyModule(module *layout.CpModule, size uintptr, freeBody func()) error {
	moduleType, name := shm.String(module.Type[:]), shm.String(module.Name[:])
	owner, ok := tryDestroy(&module.Entity)
	if !ok {
		return fmt.Errorf("module %s:%s: %w", moduleType, name, ErrStillReferenced)
	}
	if freeBody != nil {
		freeBody()
	}
	moduleFini(module)
	shmalloc.ContextFree(&owner.MemoryContext, unsafe.Pointer(module), size)
	destroyReleased(owner)
	return nil
}

// DestroyObject frees an object of size bytes, allocated in its agent's
// memory, once no live generation holds it, as an object's typed free
// does: cp_object_try_destroy, then freeBody, then cp_object_fini and the
// block, then agent_destroy_released.
//
// It reports ErrStillReferenced, freeing nothing, while a live generation
// holds the object.
func DestroyObject(object *layout.CpObject, size uintptr, freeBody func()) error {
	objectType, name := shm.String(object.Type[:]), shm.String(object.Name[:])
	owner, ok := tryDestroy(&object.Entity)
	if !ok {
		return fmt.Errorf("object %s:%s: %w", objectType, name, ErrStillReferenced)
	}
	if freeBody != nil {
		freeBody()
	}
	counterRegistryFini(&object.LinkCounterRegistry)
	counterRegistryFini(&object.CounterRegistry)
	entityFini(&object.Entity)
	shmalloc.ContextFree(&owner.MemoryContext, unsafe.Pointer(object), size)
	destroyReleased(owner)
	return nil
}

// tryDestroy is cp_entity_try_destroy: under the configuration and agent
// locks it reads whether a generation references the entity and, when
// none does, holds the owner against reclamation for the caller's
// destroy; it sweeps the superseded agents before leaving the locks.
func tryDestroy(entity *layout.CpEntity) (*layout.Agent, bool) {
	owner := entity.Agent.Get()
	var cp *layout.CpConfig
	if owner != nil {
		cp = owner.CpConfig.Get()
	}
	if cp != nil {
		lock := shm.LockPid(&cp.ConfigLock, int32(os.Getpid()))
		defer lock.Unlock()
		shm.LockByte(&cp.AgentLock.Locked)
		defer shm.UnlockByte(&cp.AgentLock.Locked)
		defer agent.SweepLocked(cp)
	}
	if atomic.LoadUint64(&entity.ConfigItem.Refcnt) != 0 {
		return nil, false
	}
	if owner != nil {
		atomic.AddUint64(&owner.LoadedEntityCount, 1)
	}
	return owner, true
}

// destroyReleased is agent_destroy_released: it drops the hold a destroy
// took and, when that was the agent's last reference, sweeps the
// superseded agents.
func destroyReleased(owner *layout.Agent) {
	if owner == nil {
		return
	}
	cp := owner.CpConfig.Get()
	if atomic.AddUint64(&owner.LoadedEntityCount, ^uint64(0)) != 0 || cp == nil {
		return
	}
	shm.LockByte(&cp.AgentLock.Locked)
	defer shm.UnlockByte(&cp.AgentLock.Locked)
	agent.SweepLocked(cp)
}

// moduleFini frees a module configuration's counter registries, device
// and object links, and its memory context, as cp_module_fini does.
func moduleFini(module *layout.CpModule) {
	context := &module.Entity.MemoryContext
	counterRegistryFini(&module.CounterRegistry)
	if first := module.RuntimeCounterRegistries.Get(); first != nil {
		entries := unsafe.Slice(first, module.RuntimeCounterRegistryCount)
		for idx := range entries {
			entry := entries[idx].Get()
			counterRegistryFini(&entry.Registry)
			shmalloc.ContextFree(context, unsafe.Pointer(entry), unsafe.Sizeof(layout.CpModuleCounterRegistry{}))
		}
		shmalloc.ContextFree(context, unsafe.Pointer(first), 8*uintptr(module.RuntimeCounterRegistryCount))
	}
	module.RuntimeCounterRegistries.Set(nil)
	module.RuntimeCounterRegistryCount = 0
	if devices := module.Devices.Get(); devices != nil {
		shmalloc.ContextFree(context, unsafe.Pointer(devices), unsafe.Sizeof(layout.CpModuleDevice{})*uintptr(module.DeviceCount))
	}
	module.Devices.Set(nil)
	module.DeviceCount = 0
	if objects := module.Objects.Get(); objects != nil {
		shmalloc.ContextFree(context, unsafe.Pointer(objects), unsafe.Sizeof(layout.CpModuleObject{})*uintptr(module.ObjectCount))
	}
	module.Objects.Set(nil)
	module.ObjectCount = 0
	entityFini(&module.Entity)
}

// entityFini unlinks an entity from its agent and finishes its memory
// context, as cp_entity_fini does.
func entityFini(entity *layout.CpEntity) {
	entity.Agent.Set(nil)
	shmalloc.ContextFini(&entity.MemoryContext)
}
