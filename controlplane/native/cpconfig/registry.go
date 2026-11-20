//go:build linux && (amd64 || arm64)

package cpconfig

import (
	"sync/atomic"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

const (
	// registryHashSeed seeds the hash of registry keys
	// (REGISTRY_HASH_SEED).
	registryHashSeed = 0x9e3779b9
	// hashIndexInvalid marks a free slot of a hash index.
	hashIndexInvalid = 0xffffffff
	// hashIndexSparseFactor keeps a hash index at most a quarter full.
	hashIndexSparseFactor = 4
	// hashIndexMaxCapacity is the largest count a hash index holds.
	hashIndexMaxCapacity = 0xffffffff / hashIndexSparseFactor
	// registryInitialCapacity is the capacity of a fresh registry.
	registryInitialCapacity = 8
)

// registryHashName hashes one key field up to its terminating zero.
func registryHashName(field []byte) uint64 {
	return fnv1a(shm.Bytes(field), registryHashSeed)
}

// registryHashNamePair hashes a two-field key: the first field's hash
// seeds the second's.
func registryHashNamePair(first, second []byte) uint64 {
	hash := fnv1a(shm.Bytes(first), registryHashSeed)
	return fnv1a(shm.Bytes(second), uint32(hash))
}

// registryHashFold folds a key hash into the 32 bits the index probes
// with.
func registryHashFold(hash uint64) uint32 {
	return uint32(hash ^ hash>>32)
}

// registryKeys is the canonical key of a registry: the hash its index
// is built on and the equality its lookups use.
type registryKeys struct {
	// ItemHash hashes the key of an item in the registry.
	ItemHash func(item *layout.RegistryItem) uint64
}

// containerOf returns the structure that holds a registry item at offset.
func containerOf[T any](item *layout.RegistryItem, offset uintptr) *T {
	return (*T)(unsafe.Add(unsafe.Pointer(item), -int(offset)))
}

var (
	moduleKeys = &registryKeys{ItemHash: func(item *layout.RegistryItem) uint64 {
		module := containerOf[layout.CpModule](item, 0)
		return registryHashNamePair(module.Name[:], module.Type[:])
	}}
	deviceKeys = &registryKeys{ItemHash: func(item *layout.RegistryItem) uint64 {
		device := containerOf[layout.CpDevice](item, 0)
		return registryHashNamePair(device.Name[:], device.Type[:])
	}}
	objectKeys = &registryKeys{ItemHash: func(item *layout.RegistryItem) uint64 {
		object := containerOf[layout.CpObject](item, 0)
		return registryHashNamePair(object.Name[:], object.Type[:])
	}}
	functionKeys = &registryKeys{ItemHash: func(item *layout.RegistryItem) uint64 {
		function := containerOf[layout.CpFunction](item, unsafe.Offsetof(layout.CpFunction{}.ConfigItem))
		return registryHashName(function.Name[:])
	}}
	pipelineKeys = &registryKeys{ItemHash: func(item *layout.RegistryItem) uint64 {
		pipeline := containerOf[layout.CpPipeline](item, unsafe.Offsetof(layout.CpPipeline{}.ConfigItem))
		return registryHashName(pipeline.Name[:])
	}}
)

// Hash index.

func hashIndexEntries(index *layout.HashIndex) []layout.HashIndexEntry {
	entries := index.Entries.Get()
	if entries == nil {
		return nil
	}
	return unsafe.Slice(entries, index.Capacity*hashIndexSparseFactor)
}

// fillInvalid marks every entry free, as memset 0xff does.
func fillInvalid(entries []layout.HashIndexEntry) {
	for idx := range entries {
		entries[idx] = layout.HashIndexEntry{Hash: hashIndexInvalid, Value: hashIndexInvalid}
	}
}

func hashIndexInit(index *layout.HashIndex, context *layout.MemoryContext, capacity uint32) bool {
	index.MemoryContext.Set(context)
	if capacity == 0 || capacity > hashIndexMaxCapacity {
		index.Entries.Set(nil)
		index.Capacity = 0
		index.Count = 0
		return capacity == 0
	}
	slots := uintptr(capacity) * hashIndexSparseFactor
	block := shmalloc.ContextAllocLocked(context, slots*unsafe.Sizeof(layout.HashIndexEntry{}))
	if block == nil {
		index.Entries.Set(nil)
		index.Capacity = 0
		index.Count = 0
		return false
	}
	entries := unsafe.Slice((*layout.HashIndexEntry)(block), slots)
	fillInvalid(entries)
	index.Entries.Set(&entries[0])
	index.Capacity = capacity
	index.Count = 0
	return true
}

func hashIndexFini(index *layout.HashIndex) {
	entries := hashIndexEntries(index)
	if entries == nil {
		return
	}
	shmalloc.ContextFreeLocked(index.MemoryContext.Get(), unsafe.Pointer(&entries[0]),
		uintptr(len(entries))*unsafe.Sizeof(layout.HashIndexEntry{}),
	)
	index.Entries.Set(nil)
	index.Capacity = 0
	index.Count = 0
}

func hashIndexRehash(index *layout.HashIndex, capacity uint32) bool {
	if capacity == 0 || capacity > hashIndexMaxCapacity || capacity < index.Count {
		return false
	}
	if capacity == index.Capacity {
		return true
	}
	context := index.MemoryContext.Get()
	slots := capacity * hashIndexSparseFactor
	entrySize := unsafe.Sizeof(layout.HashIndexEntry{})
	block := shmalloc.ContextAllocLocked(context, uintptr(slots)*entrySize)
	if block == nil {
		return false
	}
	fresh := unsafe.Slice((*layout.HashIndexEntry)(block), slots)
	fillInvalid(fresh)
	old := hashIndexEntries(index)
	for _, entry := range old {
		if entry.Value == hashIndexInvalid {
			continue
		}
		bucket := entry.Hash % slots
		for fresh[bucket].Value != hashIndexInvalid {
			bucket = (bucket + 1) % slots
		}
		fresh[bucket] = entry
	}
	index.Entries.Set(&fresh[0])
	index.Capacity = capacity
	if old != nil {
		shmalloc.ContextFreeLocked(context, unsafe.Pointer(&old[0]), uintptr(len(old))*entrySize)
	}
	return true
}

func hashIndexDistance(from, to, slots uint32) uint32 {
	if to >= from {
		return to - from
	}
	return uint32(uint64(to) + uint64(slots) - uint64(from))
}

func hashIndexRemove(index *layout.HashIndex, hash, value uint32) bool {
	if index.Capacity == 0 {
		return false
	}
	entries := hashIndexEntries(index)
	slots := index.Capacity * hashIndexSparseFactor
	bucket := hash % slots
	for entries[bucket].Value != hashIndexInvalid {
		if entries[bucket].Hash == hash && entries[bucket].Value == value {
			break
		}
		bucket = (bucket + 1) % slots
	}
	if entries[bucket].Value == hashIndexInvalid {
		return false
	}
	entries[bucket].Value = hashIndexInvalid
	hole := bucket
	for candidate := (bucket + 1) % slots; entries[candidate].Value != hashIndexInvalid; candidate = (candidate + 1) % slots {
		home := entries[candidate].Hash % slots
		if hashIndexDistance(home, hole, slots) < hashIndexDistance(home, candidate, slots) {
			entries[hole] = entries[candidate]
			entries[candidate].Value = hashIndexInvalid
			hole = candidate
		}
	}
	index.Count--
	return true
}

func hashIndexLookup(index *layout.HashIndex, hash uint32, equal func(value uint32) bool) uint32 {
	if index.Capacity == 0 {
		return hashIndexInvalid
	}
	entries := hashIndexEntries(index)
	slots := index.Capacity * hashIndexSparseFactor
	for bucket := hash % slots; entries[bucket].Value != hashIndexInvalid; bucket = (bucket + 1) % slots {
		if entries[bucket].Hash == hash && equal(entries[bucket].Value) {
			return entries[bucket].Value
		}
	}
	return hashIndexInvalid
}

func hashIndexInsert(index *layout.HashIndex, hash, value uint32) bool {
	if index.Count >= index.Capacity {
		return false
	}
	entries := hashIndexEntries(index)
	slots := index.Capacity * hashIndexSparseFactor
	bucket := hash % slots
	for entries[bucket].Value != hashIndexInvalid {
		bucket = (bucket + 1) % slots
	}
	entries[bucket] = layout.HashIndexEntry{Hash: hash, Value: value}
	index.Count++
	return true
}

// Registry.

func registrySlots(registry *layout.Registry) []shm.Ptr[layout.RegistryItem] {
	items := registry.Items.Get()
	if items == nil {
		return nil
	}
	return unsafe.Slice(items, registry.Capacity)
}

// registryGet returns the item in a slot, or nil for a free one: a free
// slot links to the next free slot, inside the slot array, where no item
// ever lies.
func registryGet(registry *layout.Registry, index uint64) *layout.RegistryItem {
	slots := registrySlots(registry)
	item := slots[index].Get()
	if item == nil {
		return nil
	}
	address := uintptr(unsafe.Pointer(item))
	start := uintptr(unsafe.Pointer(&slots[0]))
	if address >= start && address < start+uintptr(len(slots))*unsafe.Sizeof(slots[0]) {
		return nil
	}
	return item
}

func registrySet(registry *layout.Registry, index uint64, item *layout.RegistryItem) {
	registrySlots(registry)[index].Set(item)
}

func registryFreePop(registry *layout.Registry) (uint64, bool) {
	head := registry.FreeList.Get()
	if head == nil {
		return 0, false
	}
	slots := registrySlots(registry)
	link := (*shm.Ptr[layout.RegistryItem])(unsafe.Pointer(head))
	registry.FreeList.Set(link.Get())
	offset := uintptr(unsafe.Pointer(head)) - uintptr(unsafe.Pointer(&slots[0]))
	return uint64(offset / unsafe.Sizeof(slots[0])), true
}

func registryFreePush(registry *layout.Registry, index uint64) {
	slot := &registrySlots(registry)[index]
	slot.Set(registry.FreeList.Get())
	registry.FreeList.Set((*layout.RegistryItem)(unsafe.Pointer(slot)))
}

func registryIndexEnsure(registry *layout.Registry) bool {
	if uint64(registry.KeyIndex.Capacity) >= registry.Capacity {
		return true
	}
	return hashIndexRehash(&registry.KeyIndex, uint32(registry.Capacity))
}

func registryInit(context *layout.MemoryContext, owner *layout.CpConfig, registry *layout.Registry, capacity uint64) bool {
	if capacity > 0xffffffff {
		return false
	}
	registry.MemoryContext.Set(context)
	registry.Owner.Set(owner)
	registry.Capacity = capacity
	slotSize := unsafe.Sizeof(shm.Ptr[layout.RegistryItem]{})
	block := shmalloc.ContextAllocLocked(context, slotSize*uintptr(capacity))
	if block == nil {
		return false
	}
	registry.Items.Set((*shm.Ptr[layout.RegistryItem])(block))
	registry.FreeList.Set(nil)
	for idx := capacity; idx > 0; idx-- {
		registryFreePush(registry, idx-1)
	}
	if !hashIndexInit(&registry.KeyIndex, context, uint32(capacity)) {
		registry.Items.Set(nil)
		shmalloc.ContextFreeLocked(context, block, slotSize*uintptr(capacity))
		return false
	}
	return true
}

// registryCopy makes a registry holding the items of old, each with one
// more reference, and its index.
func registryCopy(context *layout.MemoryContext, keys *registryKeys, fresh, old *layout.Registry) bool {
	if !registryInit(context, old.Owner.Get(), fresh, old.Capacity) {
		return false
	}
	for idx := range old.Capacity {
		item := registryGet(old, idx)
		if item != nil {
			atomic.AddUint64(&item.Refcnt, 1)
		}
		registrySet(fresh, idx, item)
	}
	fresh.FreeList.Set(nil)
	for idx := fresh.Capacity; idx > 0; idx-- {
		if registryGet(fresh, idx-1) == nil {
			registryFreePush(fresh, idx-1)
		}
	}
	if keys != nil {
		for idx := range old.Capacity {
			if item := registryGet(fresh, idx); item != nil {
				hashIndexInsert(&fresh.KeyIndex, registryHashFold(keys.ItemHash(item)), uint32(idx))
			}
		}
	}
	return true
}

func registryExtend(registry *layout.Registry) bool {
	context := registry.MemoryContext.Get()
	oldCapacity := registry.Capacity
	capacity := oldCapacity * 2
	if oldCapacity == 0 {
		capacity = 1
	}
	slotSize := unsafe.Sizeof(shm.Ptr[layout.RegistryItem]{})
	block := shmalloc.ContextAllocLocked(context, slotSize*uintptr(capacity))
	if block == nil {
		return false
	}
	old := registrySlots(registry)
	grown := unsafe.Slice((*shm.Ptr[layout.RegistryItem])(block), capacity)
	for idx := range old {
		grown[idx].Set(old[idx].Get())
	}
	registry.Items.Set(&grown[0])
	for idx := capacity; idx > oldCapacity; idx-- {
		registry.Capacity = capacity
		registryFreePush(registry, idx-1)
	}
	if len(old) > 0 {
		shmalloc.ContextFreeLocked(context, unsafe.Pointer(&old[0]), slotSize*uintptr(oldCapacity))
	}
	registry.Capacity = capacity
	return true
}

// registryLookup finds the item matching through the index.
func registryLookup(registry *layout.Registry, hash uint64, equal func(item *layout.RegistryItem) bool) (uint64, bool) {
	found := hashIndexLookup(&registry.KeyIndex, registryHashFold(hash), func(value uint32) bool {
		item := registryGet(registry, uint64(value))
		return item != nil && equal(item)
	})
	if found == hashIndexInvalid {
		return 0, false
	}
	return uint64(found), true
}

func registryAcquireSlot(registry *layout.Registry) (uint64, bool) {
	if index, ok := registryFreePop(registry); ok {
		return index, true
	}
	if !registryExtend(registry) {
		return 0, false
	}
	return registryFreePop(registry)
}

// registryReplace puts item in the slot of the key, or a free slot when
// the key is absent, and drops the displaced item's reference; a nil item
// deletes the key's item, and fails when the key is absent.
func registryReplace(registry *layout.Registry, keys *registryKeys, hash uint64,
	equal func(item *layout.RegistryItem) bool, item *layout.RegistryItem,
) bool {
	index, found := registryLookup(registry, hash, equal)
	if !found {
		if item == nil {
			return false
		}
		var ok bool
		if index, ok = registryAcquireSlot(registry); !ok {
			return false
		}
		if !registryIndexEnsure(registry) {
			registryFreePush(registry, index)
			return false
		}
	}
	old := registryGet(registry, index)
	if item != nil {
		atomic.AddUint64(&item.Refcnt, 1)
		registrySet(registry, index, item)
	} else {
		registryFreePush(registry, index)
	}
	if old != nil {
		hashIndexRemove(&registry.KeyIndex, registryHashFold(keys.ItemHash(old)), uint32(index))
	}
	if item != nil {
		hashIndexInsert(&registry.KeyIndex, registryHashFold(keys.ItemHash(item)), uint32(index))
	}
	if old != nil {
		atomic.AddUint64(&old.Refcnt, ^uint64(0))
	}
	return true
}

// Entity registries: the module, device and object registries, whose
// items count into their agents' loaded-entity counts.

func entityOwner(item *layout.RegistryItem) *layout.Agent {
	return containerOf[layout.CpEntity](item, 0).Agent.Get()
}

func entityRegistryCopy(context *layout.MemoryContext, keys *registryKeys, fresh, old *layout.CpEntityRegistry) bool {
	if !registryCopy(context, keys, &fresh.Registry, &old.Registry) {
		return false
	}
	fresh.MemoryContext.Set(context)
	for idx := range fresh.Registry.Capacity {
		item := registryGet(&fresh.Registry, idx)
		if item == nil {
			continue
		}
		if owner := entityOwner(item); owner != nil {
			atomic.AddUint64(&owner.LoadedEntityCount, 1)
		}
	}
	return true
}

func entityRegistryReplace(registry *layout.CpEntityRegistry, keys *registryKeys, hash uint64,
	equal func(item *layout.RegistryItem) bool, item *layout.RegistryItem,
) bool {
	var old *layout.RegistryItem
	if index, found := registryLookup(&registry.Registry, hash, equal); found {
		old = registryGet(&registry.Registry, index)
	}
	if !registryReplace(&registry.Registry, keys, hash, equal, item) {
		return false
	}
	if item != nil {
		if owner := entityOwner(item); owner != nil {
			atomic.AddUint64(&owner.LoadedEntityCount, 1)
		}
	}
	if old != nil {
		if owner := entityOwner(old); owner != nil {
			atomic.AddUint64(&owner.LoadedEntityCount, ^uint64(0))
		}
	}
	return true
}
