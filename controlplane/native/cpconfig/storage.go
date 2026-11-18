//go:build linux && (amd64 || arm64)

package cpconfig

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
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

// storageRegistryIndex returns the index of the storage registered under
// exactly the normalised tags of the hash, or hashIndexInvalid. Stored
// tags are zero past their strings, as the set's are, so equal bytes are
// equal tags.
func storageRegistryIndex(registry *layout.CpConfigCounterStorageRegistry, tags []layout.CounterTag, hash uint32) uint32 {
	items := storageRegistryItems(registry)
	return hashIndexLookup(&registry.TagIndex, hash, func(value uint32) bool {
		item := &items[value]
		return item.TagCount == uint64(len(tags)) && slices.Equal(item.Tags[:len(tags)], tags)
	})
}

// storageRegistryInsert registers a storage under a normalised tag set
// of the hash.
func storageRegistryInsert(registry *layout.CpConfigCounterStorageRegistry, tags []layout.CounterTag, hash uint32, storage *layout.CounterStorage) error {
	if storageRegistryIndex(registry, tags, hash) != hashIndexInvalid {
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
	if !hashIndexInsert(&registry.TagIndex, hash, uint32(registry.Count)) {
		return fmt.Errorf("index counter storage: %w", ErrNoMemory)
	}
	registry.Count++
	return nil
}

// storageRegistryLookup returns the storage registered under exactly the
// normalised tag set of the hash, or nil.
func storageRegistryLookup(registry *layout.CpConfigCounterStorageRegistry, tags []layout.CounterTag, hash uint32) *layout.CounterStorage {
	if registry == nil {
		return nil
	}
	index := storageRegistryIndex(registry, tags, hash)
	if index == hashIndexInvalid {
		return nil
	}
	return storageRegistryItems(registry)[index].Storage.Get()
}

// tagSet is the set of tags a counter storage registers under, kept
// sorted by key as a registered set must be.
//
// Every key and value is held twice: as a Go string, so that sorting,
// checking and hashing the set never search a fixed-size field for its
// end, and encoded as the zero-padded tag shared memory stores, so that a
// stored set matches it when their bytes are equal.
type tagSet struct {
	Keys   [maxTagCount]string
	Values [maxTagCount]string
	Tags   [maxTagCount]layout.CounterTag
	Count  int
}

// newTagSet returns the tag set of key and value pairs.
func newTagSet(pairs ...string) tagSet {
	var m tagSet
	m.Add(pairs...)
	return m
}

// Add inserts the tags of key and value pairs in key order, each cut to
// what its field keeps; a repeated key is left for Check to refuse.
func (m *tagSet) Add(pairs ...string) {
	for idx := 0; idx+1 < len(pairs); idx += 2 {
		key := fitted(pairs[idx], len(layout.CounterTag{}.Key))
		value := fitted(pairs[idx+1], len(layout.CounterTag{}.Value))
		at := m.Count
		for at > 0 && m.Keys[at-1] > key {
			at--
		}
		copy(m.Keys[at+1:m.Count+1], m.Keys[at:m.Count])
		copy(m.Values[at+1:m.Count+1], m.Values[at:m.Count])
		copy(m.Tags[at+1:m.Count+1], m.Tags[at:m.Count])
		m.Keys[at], m.Values[at] = key, value
		m.Tags[at] = layout.CounterTag{}
		copy(m.Tags[at].Key[:], key)
		copy(m.Tags[at].Value[:], value)
		m.Count++
	}
}

// Check refuses a set with a repeated key, or with a value reserved for
// the predicates readers select counters with.
func (m *tagSet) Check() error {
	for idx := range m.Count {
		if m.Values[idx] == "" || m.Values[idx] == "*" {
			return fmt.Errorf("tag %q: value %q is reserved for predicates", m.Keys[idx], m.Values[idx])
		}
		if idx > 0 && m.Keys[idx] == m.Keys[idx-1] {
			return fmt.Errorf("duplicate key %q", m.Keys[idx])
		}
	}
	return nil
}

// Hash hashes the set as C hashes a normalised one: the count, then every
// key and value in order.
func (m *tagSet) Hash() uint32 {
	var count [8]byte
	binary.LittleEndian.PutUint64(count[:], uint64(m.Count))
	hash := fnv1a(count[:], 0)
	for idx := range m.Count {
		hash = fnv1a(m.Keys[idx], uint32(hash))
		hash = fnv1a(m.Values[idx], uint32(hash))
	}
	return uint32(hash)
}

// fitted returns what of a string a C character field of size bytes
// keeps: the bytes before any NUL, at most one short of the field.
func fitted(value string, size int) string {
	value, _, _ = strings.Cut(value, "\x00")
	return value[:min(len(value), size-1)]
}

// tagPairs names the tags a storage registers under: key and value pairs
// in the order its path gives them.
type tagPairs struct {
	Pairs [2 * maxTagCount]string
	Count int
}

func newTagPairs(pairs ...string) tagPairs {
	var m tagPairs
	m.Add(pairs...)
	return m
}

// Add appends key and value pairs.
func (m *tagPairs) Add(pairs ...string) {
	m.Count += copy(m.Pairs[m.Count:], pairs)
}

// encodedTags is a checked tag set as a storage registry stores it: the
// sorted, zero-padded tags and their hash.
type encodedTags struct {
	Tags  [maxTagCount]layout.CounterTag
	Count int
	Hash  uint32
	// Build is the last build of its cache that used it.
	Build uint64
}

// encodeTags checks and encodes the tags of pairs.
func encodeTags(pairs tagPairs) (*encodedTags, error) {
	set := newTagSet(pairs.Pairs[:pairs.Count]...)
	if err := set.Check(); err != nil {
		return nil, err
	}
	return &encodedTags{Tags: set.Tags, Count: set.Count, Hash: set.Hash()}, nil
}

// tagCache remembers the encoded tag sets of an instance's builds.
//
// The names along a path do not change, so neither does what they encode
// to: a build reuses what earlier ones encoded, and every worker of a
// build reuses what the first one did. Sets no longer used are forgotten
// once they outnumber those the last build used.
type tagCache struct {
	entries map[string]*encodedTags
	build   uint64
	used    int
	// key is the buffer a lookup key is written into.
	key []byte
}

func newTagCache() *tagCache {
	return &tagCache{entries: map[string]*encodedTags{}}
}

// Encode returns the encoded tags of pairs.
func (m *tagCache) Encode(pairs tagPairs) (*encodedTags, error) {
	m.key = m.key[:0]
	for _, part := range pairs.Pairs[:pairs.Count] {
		m.key = binary.AppendUvarint(m.key, uint64(len(part)))
		m.key = append(m.key, part...)
	}
	tags, ok := m.entries[string(m.key)]
	if !ok {
		var err error
		if tags, err = encodeTags(pairs); err != nil {
			return nil, err
		}
		m.entries[string(m.key)] = tags
	}
	if tags.Build != m.build {
		tags.Build = m.build
		m.used++
	}
	return tags, nil
}

// EndBuild ends a build, forgetting the sets it did not use once they
// outnumber those it did.
func (m *tagCache) EndBuild() {
	if len(m.entries) > 2*m.used {
		for key, tags := range m.entries {
			if tags.Build != m.build {
				delete(m.entries, key)
			}
		}
	}
	m.build++
	m.used = 0
}

// tagCaches holds a tag cache per instance. A cache serves one build at
// a time: builds of an instance hold its configuration lock.
var tagCaches = struct {
	sync.Mutex
	ByConfig map[*layout.CpConfig]*tagCache
}{ByConfig: map[*layout.CpConfig]*tagCache{}}

// tagCacheOf returns the tag cache of an instance.
func tagCacheOf(cp *layout.CpConfig) *tagCache {
	tagCaches.Lock()
	defer tagCaches.Unlock()
	cache, ok := tagCaches.ByConfig[cp]
	if !ok {
		cache = newTagCache()
		tagCaches.ByConfig[cp] = cache
	}
	return cache
}

// The tags the execution contexts register their storages under.

func deviceTags(device string) tagPairs {
	return newTagPairs("device", device, "kind", "device")
}

func pipelineTags(device, pipeline string) tagPairs {
	return newTagPairs("device", device, "pipeline", pipeline, "kind", "pipeline")
}

func functionTags(device, pipeline, function string) tagPairs {
	return newTagPairs("device", device, "pipeline", pipeline, "function", function, "kind", "function")
}

func chainTags(device, pipeline, function, chain string) tagPairs {
	return newTagPairs("device", device, "pipeline", pipeline, "function", function, "chain", chain, "kind", "chain")
}

func modulePathTags(device, pipeline, function, chain, moduleType, moduleName string) tagPairs {
	return newTagPairs(
		"device", device, "pipeline", pipeline, "function", function,
		"chain", chain, "module_type", moduleType, "module_name", moduleName,
	)
}

func moduleTags(path tagPairs) tagPairs {
	path.Add("kind", "module")
	return path
}

func runtimeTags(path tagPairs, registryTag string) tagPairs {
	path.Add("kind", "runtime", "config", registryTag)
	return path
}

func objectLinkTags(path tagPairs, objectType, objectName string) tagPairs {
	path.Add("object_type", objectType, "object_name", objectName, "kind", "module_object_link")
	return path
}

func objectTags(objectType, objectName string) tagPairs {
	return newTagPairs("object_type", objectType, "object_name", objectName, "kind", "object")
}
