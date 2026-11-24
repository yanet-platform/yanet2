//go:build linux && (amd64 || arm64)

package cpconfig

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/teardown"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
)

const (
	// counterPageSize is the size of one page of counter values.
	counterPageSize = uint64(unsafe.Sizeof(layout.CounterStoragePage{}))
	// maxTagCount is the number of tags a counter storage carries
	// (MAX_TAG_COUNT).
	maxTagCount = len(layout.CpCounterStorage{}.Tags)
	// storageRegistryPrealloc is the initial capacity of a worker's
	// counter storage registry (COUNTER_REGISTRY_PREALLOC).
	storageRegistryPrealloc = 8
)

// spawnStorage returns the worker's storage of a counter registry's
// values, as counter_storage_spawn does.
//
// It continues the storage the previous generation had at the same place:
// the registry that storage was built for shares it whole; another one
// shares its value pages and adds pages for counters beyond them. A
// failed storage frees what it built.
func (m *build) spawnStorage(old *layout.CounterStorage, counters *layout.CounterRegistry) (*layout.CounterStorage, error) {
	if old != nil && old.Registry.Get() == counters {
		atomic.AddUint64(&old.Refcnt, 1)
		return old, nil
	}
	context := &m.cp.CounterStorageMemoryContext
	storage := newNode[layout.CounterStorage](context, 0)
	if storage == nil {
		return nil, fmt.Errorf("allocate storage: %w", errZoneMemory)
	}
	storage.MemoryContext.Set(context)
	storage.Registry.Set(counters)
	storage.Refcnt = 1
	for pool := range storage.Pools {
		if err := m.fillPool(storage, old, counters, pool); err != nil {
			teardown.FreeCounterStorage(storage)
			return nil, err
		}
	}

	handles, ok := allocArray[shm.Ptr[byte]](context, counters.Count, stale)
	if !ok {
		teardown.FreeCounterStorage(storage)
		return nil, fmt.Errorf("allocate value handles: %w", errZoneMemory)
	}
	for idx, counter := range counterNames(counters)[:counters.Count] {
		pool := &storage.Pools[logUp(counter.Size)]
		page := registry.Slice(pool.Blocks.Get(), pool.BlockCount)[counter.Offset/counterPageSize].Get().Pages.Get()
		values := page.Values[counter.Offset%counterPageSize/8:][:counter.Size]
		if counter.Flags&counterFlagClearSlot != 0 {
			// No published worker writes a released slot any more: the
			// new name starts from zero.
			clear(values)
		}
		handles[idx].Set(byteView(&values[0]))
	}
	if len(handles) > 0 {
		storage.CounterValueHandles.Set(&handles[0])
	}
	return storage, nil
}

// fillPool gives a fresh storage the value pages of one pool: those of
// the previous storage, shared, then new ones for counters beyond them.
//
// The pool holds each page as it takes it, so freeing the storage frees
// a pool filled part-way.
func (m *build) fillPool(storage, old *layout.CounterStorage, counters *layout.CounterRegistry, pool int) error {
	context := &m.cp.CounterStorageMemoryContext
	count := (counters.Counts[pool]*(8<<pool) + counterPageSize - 1) / counterPageSize
	var shared []shm.Ptr[layout.CounterStorageBlock]
	if old != nil {
		shared = registry.Slice(old.Pools[pool].Blocks.Get(), old.Pools[pool].BlockCount)
		count = max(count, uint64(len(shared)))
	}
	blocks, ok := allocArray[shm.Ptr[layout.CounterStorageBlock]](context, count, zeroed)
	if !ok {
		return fmt.Errorf("allocate page table of pool %d: %w", pool, errZoneMemory)
	}
	if count > 0 {
		storage.Pools[pool].Blocks.Set(&blocks[0])
	}
	storage.Pools[pool].BlockCount = count
	for idx := range shared {
		reused := shared[idx].Get()
		atomic.AddUint64(&reused.Refcnt, 1)
		blocks[idx].Set(reused)
	}
	for idx := len(shared); idx < len(blocks); idx++ {
		block := newNode[layout.CounterStorageBlock](context, 0)
		if block == nil {
			return fmt.Errorf("allocate page block of pool %d: %w", pool, errZoneMemory)
		}
		block.Refcnt = 1
		blocks[idx].Set(block)
		page := newNode[layout.CounterStoragePage](context, 0)
		if page == nil {
			return fmt.Errorf("allocate page of pool %d: %w", pool, errZoneMemory)
		}
		block.Pages.Set(page)
	}
	return nil
}

// byteView views a counter value as the byte its handle points at.
func byteView(value *uint64) *byte {
	return (*byte)(unsafe.Pointer(value))
}

func storageItems(storages *layout.CpConfigCounterStorageRegistry) []layout.CpCounterStorage {
	return registry.Slice(storages.Items.Get(), storages.Capacity)
}

// initStorages prepares a worker's counter storage registry, as
// cp_config_counter_storage_registry_init does.
func (m *build) initStorages(storages *layout.CpConfigCounterStorageRegistry) error {
	context := &m.cp.MemoryContext
	items, ok := allocArray[layout.CpCounterStorage](context, storageRegistryPrealloc, stale)
	if !ok {
		return fmt.Errorf("allocate counter storage registry: %w", errZoneMemory)
	}
	if !registry.IndexInit(&storages.TagIndex, context, storageRegistryPrealloc) {
		registry.FreeArray(context, items)
		return fmt.Errorf("allocate counter storage index: %w", errZoneMemory)
	}
	storages.Items.Set(&items[0])
	storages.Capacity = storageRegistryPrealloc
	storages.Count = 0
	storages.MemoryContext.Set(context)
	return nil
}

// storageIndex returns the index of the storage registered under exactly
// the tags, or registry.Invalid.
func storageIndex(storages *layout.CpConfigCounterStorageRegistry, tags *tagSet, hash uint32) uint32 {
	items := storageItems(storages)
	return registry.IndexLookup(&storages.TagIndex, hash, func(value uint32) bool {
		return tags.Equal(&items[value])
	})
}

// registerStorage registers a storage under the tags in the worker's
// registry, as cp_config_counter_storage_registry_insert does.
func (m *build) registerStorage(tags *tagSet, hash uint32, storage *layout.CounterStorage) error {
	storages := m.tree.CounterStorageRegistry.Get()
	if err := tags.CheckRegistrable(); err != nil {
		return err
	}
	if storageIndex(storages, tags, hash) != registry.Invalid {
		return errors.New("already registered")
	}
	if storages.Count == storages.Capacity {
		context := storages.MemoryContext.Get()
		grown, ok := allocArray[layout.CpCounterStorage](context, storages.Capacity*2, stale)
		if !ok {
			return fmt.Errorf("grow counter storage registry: %w", errZoneMemory)
		}
		old := storageItems(storages)
		for idx := range storages.Count {
			grown[idx].Tags = old[idx].Tags
			grown[idx].TagCount = old[idx].TagCount
			grown[idx].Storage.Set(old[idx].Storage.Get())
		}
		registry.FreeArray(context, old)
		storages.Items.Set(&grown[0])
		storages.Capacity *= 2
	}
	if uint64(storages.TagIndex.Capacity) < storages.Capacity &&
		!registry.IndexRehash(&storages.TagIndex, uint32(storages.Capacity)) {
		return fmt.Errorf("grow counter storage index: %w", errZoneMemory)
	}
	item := &storageItems(storages)[storages.Count]
	item.Storage.Set(storage)
	item.TagCount = uint64(tags.Count)
	tags.Store(item)
	// The index was grown to hold every item.
	_ = registry.IndexInsert(&storages.TagIndex, hash, uint32(storages.Count))
	storages.Count++
	return nil
}

// oldStorage returns the storage the worker's contexts of the previous
// generation registered under exactly the tags, or nil.
//
// It finds it as cp_config_counter_storage_registry_lookup_exact does.
func (m *build) oldStorage(tags *tagSet, hash uint32) *layout.CounterStorage {
	if m.old == nil || m.worker >= m.old.ConfigGenEctxCount || tags.CheckUnique() != nil {
		return nil
	}
	root := registry.Slice(m.old.ConfigGenEctxs.Get(), m.old.ConfigGenEctxCount)[m.worker].Get()
	if root == nil || root.CounterStorageRegistry.IsNil() {
		return nil
	}
	storages := root.CounterStorageRegistry.Get()
	index := storageIndex(storages, tags, hash)
	if index == registry.Invalid {
		return nil
	}
	return storageItems(storages)[index].Storage.Get()
}

// storageFor puts the worker's storage of a counter registry in a slot
// and registers it under the tags, continuing the previous generation's
// storage.
//
// The slot holds the storage before it registers, as C's context builders
// hold it, so a storage that fails to register goes with the context.
func (m *build) storageFor(slot *shm.Ptr[layout.CounterStorage], tags tagSet, counters *layout.CounterRegistry) error {
	hash := tags.Hash()
	storage, err := m.spawnStorage(m.oldStorage(&tags, hash), counters)
	if err == nil {
		slot.Set(storage)
		err = m.registerStorage(&tags, hash, storage)
	}
	if err != nil {
		return fmt.Errorf("counter storage %s: %w", tags.String(), err)
	}
	return nil
}

// tagSet is a set of tags a counter storage registers under, kept sorted
// by key as a registered set is.
//
// Keys and values are cut to what their fields keep, so the set hashes
// and compares as its stored form does.
type tagSet struct {
	Keys   [maxTagCount]string
	Values [maxTagCount]string
	Count  int
}

// newTagSet returns the tag set of key and value pairs.
func newTagSet(pairs ...string) tagSet {
	var m tagSet
	m.Add(pairs...)
	return m
}

// Add inserts the tags of key and value pairs in key order; a repeated
// key is left for the checks to refuse.
func (m *tagSet) Add(pairs ...string) {
	for idx := 0; idx+1 < len(pairs); idx += 2 {
		key := registry.FieldKey(pairs[idx], len(layout.CounterTag{}.Key))
		value := registry.FieldKey(pairs[idx+1], len(layout.CounterTag{}.Value))
		at := m.Count
		for at > 0 && m.Keys[at-1] > key {
			at--
		}
		copy(m.Keys[at+1:m.Count+1], m.Keys[at:m.Count])
		copy(m.Values[at+1:m.Count+1], m.Values[at:m.Count])
		m.Keys[at], m.Values[at] = key, value
		m.Count++
	}
}

// String lists the set's tags as key=value pairs.
func (m *tagSet) String() string {
	var text strings.Builder
	for idx := range m.Count {
		if idx > 0 {
			text.WriteByte(',')
		}
		text.WriteString(m.Keys[idx] + "=" + m.Values[idx])
	}
	return text.String()
}

// CheckUnique refuses a set that names a key twice.
func (m *tagSet) CheckUnique() error {
	for idx := 1; idx < m.Count; idx++ {
		if m.Keys[idx] == m.Keys[idx-1] {
			return fmt.Errorf("duplicate key %q", m.Keys[idx])
		}
	}
	return nil
}

// CheckRegistrable refuses a set a storage cannot register under: a key
// named twice, or a value reserved for the predicates of readers.
func (m *tagSet) CheckRegistrable() error {
	for idx := range m.Count {
		if m.Values[idx] == "" || m.Values[idx] == "*" {
			return fmt.Errorf("tag %q: value %q is reserved for predicates", m.Keys[idx], m.Values[idx])
		}
	}
	return m.CheckUnique()
}

// Hash hashes the set as C hashes a sorted one: the count, then every key
// and value in order.
func (m *tagSet) Hash() uint32 {
	var count [8]byte
	binary.LittleEndian.PutUint64(count[:], uint64(m.Count))
	hash := registry.FNV1a(count[:], 0)
	for idx := range m.Count {
		hash = registry.FNV1a(m.Keys[idx], uint32(hash))
		hash = registry.FNV1a(m.Values[idx], uint32(hash))
	}
	return uint32(hash)
}

// Equal reports whether a registered storage carries exactly the set.
func (m *tagSet) Equal(item *layout.CpCounterStorage) bool {
	if item.TagCount != uint64(m.Count) {
		return false
	}
	for idx := range m.Count {
		if !shm.Equal(item.Tags[idx].Key[:], m.Keys[idx]) || !shm.Equal(item.Tags[idx].Value[:], m.Values[idx]) {
			return false
		}
	}
	return true
}

// Store writes the set into a registered storage's tags.
//
// Each tag is zeroed past its strings, as counter_tag_init leaves it; the
// tags past the set stay as they were.
func (m *tagSet) Store(item *layout.CpCounterStorage) {
	for idx := range m.Count {
		tag := &item.Tags[idx]
		*tag = layout.CounterTag{}
		copy(tag.Key[:], m.Keys[idx])
		copy(tag.Value[:], m.Values[idx])
	}
}

// The tags the execution contexts register their storages under, as
// lib/controlplane/config/cp_counter.c names them.

func deviceTags(device string) tagSet {
	return newTagSet("device", device, "kind", "device")
}

func pipelineTags(device, pipeline string) tagSet {
	return newTagSet("device", device, "pipeline", pipeline, "kind", "pipeline")
}

func functionTags(device, pipeline, function string) tagSet {
	return newTagSet("device", device, "pipeline", pipeline, "function", function, "kind", "function")
}

func chainTags(device, pipeline, function, chain string) tagSet {
	return newTagSet("device", device, "pipeline", pipeline, "function", function, "chain", chain, "kind", "chain")
}

func modulePathTags(device, pipeline, function, chain, moduleType, moduleName string) tagSet {
	return newTagSet(
		"device", device, "pipeline", pipeline, "function", function,
		"chain", chain, "module_type", moduleType, "module_name", moduleName,
	)
}

func moduleTags(path tagSet) tagSet {
	path.Add("kind", "module")
	return path
}

func runtimeTags(path tagSet, registryTag string) tagSet {
	path.Add("kind", "runtime", "config", registryTag)
	return path
}

func objectLinkTags(path tagSet, objectType, objectName string) tagSet {
	path.Add("object_type", objectType, "object_name", objectName, "kind", "module_object_link")
	return path
}

func objectTags(objectType, objectName string) tagSet {
	return newTagSet("object_type", objectType, "object_name", objectName, "kind", "object")
}
