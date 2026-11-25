//go:build linux && (amd64 || arm64)

package cpconfig

import (
	"errors"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/yanet-platform/yanet2/controlplane/native/internal/teardown"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
)

// ErrStillReferenced marks the free of a configuration a generation still
// holds; C refuses it as busy, and nothing is freed.
var ErrStillReferenced = errors.New("still referenced by a live generation")

// TryDestroyModule takes a module configuration no generation holds out
// of circulation for its typed free, as cp_module_try_destroy does.
//
// On success the module's agent is held for the free, and the caller
// returns that hold with DestroyReleased once the module is freed.
func TryDestroyModule(module *layout.CpModule) error {
	// The identity is read before the gate: past it, a generation's last
	// release may free the memory the module lives in.
	moduleType, name := shm.String(module.Type[:]), shm.String(module.Name[:])
	if !tryDestroy(&module.Entity) {
		return fmt.Errorf("module %s:%s: %w", moduleType, name, ErrStillReferenced)
	}
	return nil
}

// TryDestroyObject takes an object no generation holds out of circulation
// for its typed free, as cp_object_try_destroy does.
//
// On success the object's agent is held for the free, and the caller
// returns that hold with DestroyReleased once the object is freed.
func TryDestroyObject(object *layout.CpObject) error {
	// The identity is read before the gate, as for a module.
	objectType, name := shm.String(object.Type[:]), shm.String(object.Name[:])
	if !tryDestroy(&object.Entity) {
		return fmt.Errorf("object %s:%s: %w", objectType, name, ErrStillReferenced)
	}
	return nil
}

// tryDestroy reports whether no generation references the entity and, if
// so, holds its agent for the caller's free, as cp_entity_try_destroy
// does.
//
// The reference count is read under the configuration and agent locks:
// once they are released, a generation's last release may drop the
// entity's registries and a sweep may reclaim the agent's memory. Like
// every hold of those locks, it sweeps the superseded agents before
// leaving.
func tryDestroy(entity *layout.CpEntity) bool {
	owner := entity.Agent.Get()
	var cp *layout.CpConfig
	if owner != nil {
		cp = owner.CpConfig.Get()
	}
	if cp != nil {
		lock := shm.LockPid(&cp.ConfigLock, int32(os.Getpid()))
		defer lock.Unlock()
		shm.LockSpin(&cp.AgentLock.Locked)
		defer shm.UnlockSpin(&cp.AgentLock.Locked)
		defer teardown.SweepAgentsLocked(cp)
	}
	if atomic.LoadUint64(&entity.ConfigItem.Refcnt) != 0 {
		return false
	}
	if owner != nil {
		atomic.AddUint64(&owner.LoadedEntityCount, 1)
	}
	return true
}

// DestroyReleased returns the hold a typed free took on an agent, as
// agent_destroy_released does.
//
// The return that leaves the agent unreferenced sweeps the superseded
// agents itself, so a free that outlived its agent's supersession
// reclaims it at once. The caller does not hold the agent lock.
func DestroyReleased(owner *layout.Agent) {
	if owner == nil {
		return
	}
	// The zone is read first: once the count drops to zero, a sweep may
	// reclaim the agent.
	cp := owner.CpConfig.Get()
	if atomic.AddUint64(&owner.LoadedEntityCount, ^uint64(0)) != 0 || cp == nil {
		return
	}
	sweepAgents(cp)
}
