//go:build linux && (amd64 || arm64)

package registry

import (
	"sync/atomic"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
)

// Function returns the function an item of a function registry belongs
// to.
func Function(item *layout.RegistryItem) *layout.CpFunction {
	return Container[layout.CpFunction](item, unsafe.Offsetof(layout.CpFunction{}.ConfigItem))
}

// Pipeline returns the pipeline an item of a pipeline registry belongs
// to.
func Pipeline(item *layout.RegistryItem) *layout.CpPipeline {
	return Container[layout.CpPipeline](item, unsafe.Offsetof(layout.CpPipeline{}.ConfigItem))
}

// Owner returns the agent an item of an entity registry belongs to.
func Owner(item *layout.RegistryItem) *layout.Agent {
	return Container[layout.CpEntity](item, 0).Agent.Get()
}

// CopyEntities copies an entity registry and counts each copied item
// into its agent's loaded entities, as cp_entity_registry_copy does.
func CopyEntities(context *layout.MemoryContext, hash ItemHash, fresh, old *layout.CpEntityRegistry) bool {
	if !Copy(context, hash, &fresh.Registry, &old.Registry) {
		return false
	}
	fresh.MemoryContext.Set(context)
	for _, item := range Items(&fresh.Registry) {
		countOwner(item, 1)
	}
	return true
}

// ReplaceEntity replaces an entity, or deletes it for a nil item, and
// moves the loaded-entity counts of the agents of the new and the
// displaced item.
//
// It moves them as cp_entity_registry_replace does.
func ReplaceEntity(
	registry *layout.CpEntityRegistry,
	hash ItemHash,
	keyHash uint64,
	match Match,
	item *layout.RegistryItem,
) bool {
	var old *layout.RegistryItem
	if index, found := Lookup(&registry.Registry, keyHash, match); found {
		old = Item(&registry.Registry, index)
	}
	if !Replace(&registry.Registry, hash, keyHash, match, item) {
		return false
	}
	if item != nil {
		countOwner(item, 1)
	}
	if old != nil {
		countOwner(old, -1)
	}
	return true
}

// FiniEntities finalises an entity registry and then drops its items'
// agents' loaded counts, as cp_entity_registry_fini does.
//
// The counts drop last: a zero count makes an agent reclaimable, and its
// items live in its arenas.
func FiniEntities(registry *layout.CpEntityRegistry) {
	if registry.Registry.Items.IsNil() {
		return
	}
	// Registries hold few items; a larger one moves the snapshot to the
	// heap.
	var snapshot [16]*layout.Agent
	owners := snapshot[:0]
	for _, item := range Items(&registry.Registry) {
		owners = append(owners, Owner(item))
	}
	Fini(&registry.Registry, nil)
	for _, owner := range owners {
		if owner != nil {
			atomic.AddUint64(&owner.LoadedEntityCount, ^uint64(0))
		}
	}
}

// countOwner moves the loaded-entity count of an item's agent.
func countOwner(item *layout.RegistryItem, delta int64) {
	if owner := Owner(item); owner != nil {
		atomic.AddUint64(&owner.LoadedEntityCount, uint64(delta))
	}
}
