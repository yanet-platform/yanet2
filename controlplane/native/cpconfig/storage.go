package cpconfig

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"sync/atomic"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

const (
	// counterPageSize is the size of one page of counter values.
	counterPageSize = uint64(unsafe.Sizeof(layout.CounterStoragePage{}))
	// maxTagCount is the number of tags a counter storage carries.
	maxTagCount = len(layout.CpCounterStorage{}.Tags)
	// storageRegistryPrealloc is the initial capacity of a worker's
	// counter storage registry.
	storageRegistryPrealloc = 8
)

// spawnCounterStorage returns the storage of a counter registry's values
// for one worker of a generation, continuing old, the storage the
// previous generation had at the same place (nil for none): the same
// registry shares old whole, another one shares its value pages and adds
// pages for counters beyond them.
func spawnCounterStorage(context *layout.MemoryContext, old *layout.CounterStorage, registry *layout.CounterRegistry) *layout.CounterStorage {
	if old != nil && old.Registry.Get() == registry {
		atomic.AddUint64(&old.Refcnt, 1)
		return old
	}
	block := shmalloc.ContextAlloc(context, unsafe.Sizeof(layout.CounterStorage{}))
	if block == nil {
		return nil
	}
	clear(unsafe.Slice((*byte)(block), unsafe.Sizeof(layout.CounterStorage{})))
	storage := (*layout.CounterStorage)(block)
	storage.MemoryContext.Set(context)
	storage.Registry.Set(registry)
	storage.Refcnt = 1

	pointerSize := unsafe.Sizeof(shm.Ptr[layout.CounterStorageBlock]{})
	for pool := range counterPoolSize {
		size := registry.Counts[pool] * (8 << pool)
		count := (size + counterPageSize - 1) / counterPageSize
		if old != nil {
			count = max(count, old.Pools[pool].BlockCount)
		}
		entry := &storage.Pools[pool]
		table := shmalloc.ContextAlloc(context, uintptr(count)*pointerSize)
		if table == nil && count > 0 {
			return nil
		}
		blocks := unsafe.Slice((*shm.Ptr[layout.CounterStorageBlock])(table), count)
		clear(unsafe.Slice((*byte)(table), uintptr(count)*pointerSize))
		if count > 0 {
			entry.Blocks.Set(&blocks[0])
		} else {
			entry.Blocks.Set(nil)
		}
		entry.BlockCount = count

		shared := uint64(0)
		if old != nil {
			oldEntry := &old.Pools[pool]
			if oldEntry.BlockCount > 0 {
				oldBlocks := unsafe.Slice(oldEntry.Blocks.Get(), oldEntry.BlockCount)
				for ; shared < oldEntry.BlockCount; shared++ {
					reused := oldBlocks[shared].Get()
					atomic.AddUint64(&reused.Refcnt, 1)
					blocks[shared].Set(reused)
				}
			}
		}
		for idx := shared; idx < count; idx++ {
			fresh := shmalloc.ContextAlloc(context, unsafe.Sizeof(layout.CounterStorageBlock{}))
			if fresh == nil {
				return nil
			}
			clear(unsafe.Slice((*byte)(fresh), unsafe.Sizeof(layout.CounterStorageBlock{})))
			blockView := (*layout.CounterStorageBlock)(fresh)
			blockView.Refcnt = 1
			blocks[idx].Set(blockView)
			page := shmalloc.ContextAlloc(context, uintptr(counterPageSize))
			if page == nil {
				return nil
			}
			clear(unsafe.Slice((*byte)(page), counterPageSize))
			blockView.Pages.Set((*layout.CounterStoragePage)(page))
		}
	}

	handleSize := unsafe.Sizeof(shm.Ptr[byte]{})
	handles := shmalloc.ContextAlloc(context, uintptr(registry.Count)*handleSize)
	if handles == nil && registry.Count > 0 {
		return nil
	}
	handleSlots := unsafe.Slice((*shm.Ptr[byte])(handles), registry.Count)
	names := counterNames(registry)
	for idx := range registry.Count {
		counter := &names[idx]
		entry := &storage.Pools[logUp(counter.Size)]
		blocks := unsafe.Slice(entry.Blocks.Get(), entry.BlockCount)
		base := unsafe.Pointer(blocks[counter.Offset/counterPageSize].Get().Pages.Get())
		value := unsafe.Add(base, counter.Offset%counterPageSize)
		if counter.Flags&counterFlagClearSlot != 0 {
			clear(unsafe.Slice((*byte)(value), counter.Size*8))
		}
		handleSlots[idx].Set((*byte)(value))
	}
	if registry.Count > 0 {
		storage.CounterValueHandles.Set(&handleSlots[0])
	} else {
		storage.CounterValueHandles.Set(nil)
	}
	return storage
}

// tag returns a counter tag of a key and value, zeroed past both.
func tag(key, value string) layout.CounterTag {
	var result layout.CounterTag
	shm.CopyString(result.Key[:], key)
	shm.CopyString(result.Value[:], value)
	return result
}

// normalizeTags sorts tags by key and refuses duplicate keys and, for a
// tag set being registered, the values reserved for predicates.
func normalizeTags(tags []layout.CounterTag, predicate bool) error {
	if len(tags) > maxTagCount {
		return fmt.Errorf("tag count exceeds max %d", maxTagCount)
	}
	for idx := range tags {
		value := shm.String(tags[idx].Value[:])
		if !predicate && (value == "" || value == "*") {
			return fmt.Errorf("tag %q: value %q is reserved for predicates", shm.String(tags[idx].Key[:]), value)
		}
	}
	sort.Slice(tags, func(left, right int) bool {
		return shm.String(tags[left].Key[:]) < shm.String(tags[right].Key[:])
	})
	for idx := 0; idx+1 < len(tags); idx++ {
		if shm.String(tags[idx].Key[:]) == shm.String(tags[idx+1].Key[:]) {
			return fmt.Errorf("duplicate key %q", shm.String(tags[idx].Key[:]))
		}
	}
	return nil
}

// tagsHash hashes a normalised tag set: the count, then every key and
// value in order.
func tagsHash(tags []layout.CounterTag) uint32 {
	var count [8]byte
	binary.LittleEndian.PutUint64(count[:], uint64(len(tags)))
	hash := fnv1a(count[:], 0)
	for idx := range tags {
		hash = fnv1a([]byte(shm.String(tags[idx].Key[:])), uint32(hash))
		hash = fnv1a([]byte(shm.String(tags[idx].Value[:])), uint32(hash))
	}
	return uint32(hash)
}

func storageRegistryItems(registry *layout.CpConfigCounterStorageRegistry) []layout.CpCounterStorage {
	items := registry.Items.Get()
	if items == nil {
		return nil
	}
	return unsafe.Slice(items, registry.Capacity)
}

func storageRegistryInit(context *layout.MemoryContext, registry *layout.CpConfigCounterStorageRegistry) error {
	itemSize := unsafe.Sizeof(layout.CpCounterStorage{})
	items := shmalloc.ContextAlloc(context, itemSize*storageRegistryPrealloc)
	if items == nil {
		return fmt.Errorf("allocate counter storage registry: %w", ErrNoMemory)
	}
	if !hashIndexInit(&registry.TagIndex, context, storageRegistryPrealloc) {
		shmalloc.ContextFree(context, items, itemSize*storageRegistryPrealloc)
		return fmt.Errorf("allocate counter storage index: %w", ErrNoMemory)
	}
	registry.Items.Set((*layout.CpCounterStorage)(items))
	registry.Capacity = storageRegistryPrealloc
	registry.Count = 0
	registry.MemoryContext.Set(context)
	return nil
}

func storageRegistryIndex(registry *layout.CpConfigCounterStorageRegistry, tags []layout.CounterTag) uint32 {
	items := storageRegistryItems(registry)
	return hashIndexLookup(&registry.TagIndex, tagsHash(tags), func(value uint32) bool {
		item := &items[value]
		if item.TagCount != uint64(len(tags)) {
			return false
		}
		for idx := range tags {
			if shm.String(item.Tags[idx].Key[:]) != shm.String(tags[idx].Key[:]) ||
				shm.String(item.Tags[idx].Value[:]) != shm.String(tags[idx].Value[:]) {
				return false
			}
		}
		return true
	})
}

// storageRegistryInsert registers a storage under a tag set.
func storageRegistryInsert(registry *layout.CpConfigCounterStorageRegistry, tags []layout.CounterTag, storage *layout.CounterStorage) error {
	tags = append([]layout.CounterTag(nil), tags...)
	if err := normalizeTags(tags, false); err != nil {
		return err
	}
	if storageRegistryIndex(registry, tags) != hashIndexInvalid {
		return errors.New("counter storage already registered")
	}
	itemSize := unsafe.Sizeof(layout.CpCounterStorage{})
	if registry.Count == registry.Capacity {
		context := registry.MemoryContext.Get()
		block := shmalloc.ContextAlloc(context, itemSize*uintptr(registry.Capacity*2))
		if block == nil {
			return fmt.Errorf("allocate counter storage registry: %w", ErrNoMemory)
		}
		old := storageRegistryItems(registry)
		grown := unsafe.Slice((*layout.CpCounterStorage)(block), registry.Capacity*2)
		for idx := range registry.Count {
			grown[idx].Tags = old[idx].Tags
			grown[idx].TagCount = old[idx].TagCount
			grown[idx].Storage.Set(old[idx].Storage.Get())
		}
		shmalloc.ContextFree(context, unsafe.Pointer(&old[0]), itemSize*uintptr(registry.Count))
		registry.Items.Set(&grown[0])
		registry.Capacity *= 2
	}
	if uint64(registry.TagIndex.Capacity) < registry.Capacity {
		if !hashIndexRehash(&registry.TagIndex, uint32(registry.Capacity)) {
			return fmt.Errorf("grow counter storage index: %w", ErrNoMemory)
		}
	}
	item := &storageRegistryItems(registry)[registry.Count]
	item.Storage.Set(storage)
	item.TagCount = uint64(len(tags))
	copy(item.Tags[:], tags)
	if !hashIndexInsert(&registry.TagIndex, tagsHash(tags), uint32(registry.Count)) {
		return fmt.Errorf("index counter storage: %w", ErrNoMemory)
	}
	registry.Count++
	return nil
}

// storageRegistryLookup returns the storage registered under exactly the
// tag set, or nil.
func storageRegistryLookup(registry *layout.CpConfigCounterStorageRegistry, tags []layout.CounterTag) *layout.CounterStorage {
	if registry == nil {
		return nil
	}
	tags = append([]layout.CounterTag(nil), tags...)
	if normalizeTags(tags, true) != nil {
		return nil
	}
	index := storageRegistryIndex(registry, tags)
	if index == hashIndexInvalid {
		return nil
	}
	return storageRegistryItems(registry)[index].Storage.Get()
}

// The tag sets the execution contexts register their storages under.

func deviceTags(device string) []layout.CounterTag {
	return []layout.CounterTag{tag("device", device), tag("kind", "device")}
}

func pipelineTags(device, pipeline string) []layout.CounterTag {
	return []layout.CounterTag{tag("device", device), tag("pipeline", pipeline), tag("kind", "pipeline")}
}

func functionTags(device, pipeline, function string) []layout.CounterTag {
	return []layout.CounterTag{
		tag("device", device), tag("pipeline", pipeline), tag("function", function), tag("kind", "function"),
	}
}

func chainTags(device, pipeline, function, chain string) []layout.CounterTag {
	return []layout.CounterTag{
		tag("device", device), tag("pipeline", pipeline), tag("function", function),
		tag("chain", chain), tag("kind", "chain"),
	}
}

func modulePathTags(device, pipeline, function, chain, moduleType, moduleName string) []layout.CounterTag {
	return []layout.CounterTag{
		tag("device", device), tag("pipeline", pipeline), tag("function", function),
		tag("chain", chain), tag("module_type", moduleType), tag("module_name", moduleName),
	}
}

func moduleTags(path []layout.CounterTag) []layout.CounterTag {
	return append(append([]layout.CounterTag(nil), path...), tag("kind", "module"))
}

func runtimeTags(path []layout.CounterTag, registryTag string) []layout.CounterTag {
	return append(append([]layout.CounterTag(nil), path...), tag("kind", "runtime"), tag("config", registryTag))
}

func objectLinkTags(path []layout.CounterTag, objectType, objectName string) []layout.CounterTag {
	return append(append([]layout.CounterTag(nil), path...),
		tag("object_type", objectType), tag("object_name", objectName), tag("kind", "module_object_link"),
	)
}

func objectTags(objectType, objectName string) []layout.CounterTag {
	return []layout.CounterTag{tag("object_type", objectType), tag("object_name", objectName), tag("kind", "object")}
}
