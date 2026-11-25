//go:build linux && (amd64 || arm64)

package registry

import (
	"iter"
	"sync/atomic"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// slotSize is the size of a registry slot.
const slotSize = shm.PtrSize

// ItemHash hashes the key of an item of a registry: the hash its index is
// built on.
type ItemHash func(item *layout.RegistryItem) uint64

// Match reports whether an item has the key a lookup looks for.
type Match func(item *layout.RegistryItem) bool

func slots(registry *layout.Registry) []shm.Ptr[layout.RegistryItem] {
	return Slice(registry.Items.Get(), registry.Capacity)
}

// Item returns the item in a slot, or nil for an empty one.
//
// An empty slot links to the next empty one, inside the slot array, where
// no item ever lies.
func Item(registry *layout.Registry, index uint64) *layout.RegistryItem {
	all := slots(registry)
	item := all[index].Get()
	if item == nil || inside(all, unsafe.Pointer(item)) {
		return nil
	}
	return item
}

// Items yields the item in every occupied slot with the slot's index, in
// slot order.
func Items(registry *layout.Registry) iter.Seq2[uint64, *layout.RegistryItem] {
	return func(yield func(uint64, *layout.RegistryItem) bool) {
		for idx := range registry.Capacity {
			if item := Item(registry, idx); item != nil && !yield(idx, item) {
				return
			}
		}
	}
}

// inside reports whether an address lies in the slot array.
func inside(all []shm.Ptr[layout.RegistryItem], address unsafe.Pointer) bool {
	start := uintptr(unsafe.Pointer(&all[0]))
	at := uintptr(address)
	return at >= start && at < start+uintptr(len(all))*slotSize
}

// pushEmpty puts a slot at the head of the chain of empty slots.
func pushEmpty(registry *layout.Registry, index uint64) {
	slot := &slots(registry)[index]
	slot.Set(registry.FreeList.Get())
	registry.FreeList.Set((*layout.RegistryItem)(unsafe.Pointer(slot)))
}

// popEmpty takes the slot at the head of the chain of empty slots.
func popEmpty(registry *layout.Registry) (uint64, bool) {
	head := registry.FreeList.Get()
	if head == nil {
		return 0, false
	}
	link := (*shm.Ptr[layout.RegistryItem])(unsafe.Pointer(head))
	registry.FreeList.Set(link.Get())
	offset := uintptr(unsafe.Pointer(head)) - uintptr(unsafe.Pointer(&slots(registry)[0]))
	return uint64(offset / slotSize), true
}

// newSlots allocates a slot array of that capacity.
func newSlots(context *layout.MemoryContext, capacity uint64) []shm.Ptr[layout.RegistryItem] {
	return Slice((*shm.Ptr[layout.RegistryItem])(shmalloc.ContextAlloc(context, slotSize*uintptr(capacity))), capacity)
}

// Init prepares an empty registry of that capacity, as registry_init
// does.
func Init(context *layout.MemoryContext, owner *layout.CpConfig, registry *layout.Registry, capacity uint64) bool {
	if capacity > Invalid {
		return false
	}
	registry.MemoryContext.Set(context)
	registry.Owner.Set(owner)
	registry.Capacity = capacity
	all := newSlots(context, capacity)
	if all == nil {
		return false
	}
	registry.Items.Set(&all[0])
	registry.FreeList.Set(nil)
	// Descending pushes leave the chain ascending.
	for idx := capacity; idx > 0; idx-- {
		pushEmpty(registry, idx-1)
	}
	if !IndexInit(&registry.KeyIndex, context, uint32(capacity)) {
		registry.Items.Set(nil)
		FreeArray(context, all)
		return false
	}
	return true
}

// Copy makes a registry that holds another's items, each with one more
// reference, and its own index, as registry_copy does.
func Copy(context *layout.MemoryContext, hash ItemHash, fresh, old *layout.Registry) bool {
	if !Init(context, old.Owner.Get(), fresh, old.Capacity) {
		return false
	}
	all := slots(fresh)
	for idx := range old.Capacity {
		item := Item(old, idx)
		if item != nil {
			atomic.AddUint64(&item.Refcnt, 1)
		}
		all[idx].Set(item)
	}
	fresh.FreeList.Set(nil)
	for idx := fresh.Capacity; idx > 0; idx-- {
		if Item(fresh, idx-1) == nil {
			pushEmpty(fresh, idx-1)
		}
	}
	for idx, item := range Items(fresh) {
		// The index was sized to hold every slot.
		_ = IndexInsert(&fresh.KeyIndex, fold(hash(item)), uint32(idx))
	}
	return true
}

// extend doubles a registry's slot array, chaining the new slots as
// empty, as registry_extend does.
func extend(registry *layout.Registry) bool {
	context := registry.MemoryContext.Get()
	oldCapacity := registry.Capacity
	capacity := max(oldCapacity*2, 1)
	grown := newSlots(context, capacity)
	if grown == nil {
		return false
	}
	old := slots(registry)
	for idx := range old {
		grown[idx].Set(old[idx].Get())
	}
	registry.Items.Set(&grown[0])
	registry.Capacity = capacity
	for idx := capacity; idx > oldCapacity; idx-- {
		pushEmpty(registry, idx-1)
	}
	FreeArray(context, old)
	return true
}

// Lookup returns the slot of the item that matches through the index.
func Lookup(registry *layout.Registry, keyHash uint64, match Match) (uint64, bool) {
	found := IndexLookup(&registry.KeyIndex, fold(keyHash), func(value uint32) bool {
		item := Item(registry, uint64(value))
		return item != nil && match(item)
	})
	return uint64(found), found != Invalid
}

// acquire takes an empty slot, doubling the registry when none is left.
func acquire(registry *layout.Registry) (uint64, bool) {
	if index, ok := popEmpty(registry); ok {
		return index, true
	}
	if !extend(registry) {
		return 0, false
	}
	return popEmpty(registry)
}

// Replace puts an item in the slot of its key, or in an empty slot, as
// registry_replace does; the displaced item loses the slot's reference.
//
// A nil item deletes the item of the key, returning its slot to the empty
// ones, and fails when the key has none.
func Replace(registry *layout.Registry, hash ItemHash, keyHash uint64, match Match, item *layout.RegistryItem) bool {
	index, found := Lookup(registry, keyHash, match)
	if !found {
		if item == nil {
			return false
		}
		var ok bool
		if index, ok = acquire(registry); !ok {
			return false
		}
		if uint64(registry.KeyIndex.Capacity) < registry.Capacity &&
			!IndexRehash(&registry.KeyIndex, uint32(registry.Capacity)) {
			pushEmpty(registry, index)
			return false
		}
	}
	old := Item(registry, index)
	if item != nil {
		atomic.AddUint64(&item.Refcnt, 1)
		slots(registry)[index].Set(item)
	} else {
		pushEmpty(registry, index)
	}
	// The index holds a slot for every slot of the registry, and the
	// displaced item's entry is there to remove.
	if old != nil {
		_ = indexRemove(&registry.KeyIndex, fold(hash(old)), uint32(index))
	}
	if item != nil {
		_ = IndexInsert(&registry.KeyIndex, fold(hash(item)), uint32(index))
	}
	if old != nil {
		atomic.AddUint64(&old.Refcnt, ^uint64(0))
	}
	return true
}

// Fini drops every item's reference and frees the registry's storage, as
// registry_fini does.
//
// An item whose last reference goes is handed to the free function, when
// one is given.
func Fini(registry *layout.Registry, free func(item *layout.RegistryItem)) {
	if registry.Items.IsNil() {
		return
	}
	for _, item := range Items(registry) {
		if atomic.AddUint64(&item.Refcnt, ^uint64(0)) == 0 && free != nil {
			free(item)
		}
	}
	IndexFini(&registry.KeyIndex)
	FreeArray(registry.MemoryContext.Get(), slots(registry))
	*registry = layout.Registry{}
}
