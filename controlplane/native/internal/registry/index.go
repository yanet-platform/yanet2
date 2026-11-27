//go:build linux && (amd64 || arm64)

package registry

import (
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

const (
	// Invalid marks an empty slot of a hash or string index and a lookup
	// that found nothing (HASH_INDEX_INVALID, STR_INDEX_INVALID).
	Invalid = 0xffffffff
	// sparseFactor is how many slots an index keeps per value it holds
	// (HASH_INDEX_SPARSE_FACTOR, STR_INDEX_SPARSE_FACTOR).
	sparseFactor = 4
	// maxIndexCapacity is the largest count a hash index holds
	// (HASH_INDEX_MAX_CAPACITY).
	maxIndexCapacity = Invalid / sparseFactor
)

func indexEntries(index *layout.HashIndex) []layout.HashIndexEntry {
	return Slice(index.Entries.Get(), uint64(index.Capacity)*sparseFactor)
}

// newEntries allocates the entries of an index of that capacity, every
// one empty, or returns nil.
func newEntries(context *layout.MemoryContext, capacity uint32) []layout.HashIndexEntry {
	entries, _ := shmalloc.AllocArray[layout.HashIndexEntry](context, uint64(capacity)*sparseFactor)
	for idx := range entries {
		entries[idx] = layout.HashIndexEntry{Hash: Invalid, Value: Invalid}
	}
	return entries
}

// IndexInit prepares a hash index of that capacity, as hash_index_init
// does; a zero capacity holds no entries yet.
func IndexInit(index *layout.HashIndex, context *layout.MemoryContext, capacity uint32) bool {
	index.MemoryContext.Set(context)
	index.Entries.Set(nil)
	index.Capacity = 0
	index.Count = 0
	if capacity == 0 {
		return true
	}
	if capacity > maxIndexCapacity {
		return false
	}
	entries := newEntries(context, capacity)
	if entries == nil {
		return false
	}
	index.Entries.Set(&entries[0])
	index.Capacity = capacity
	return true
}

// IndexRehash moves a hash index to a new capacity, keeping every entry,
// as hash_index_rehash does; a failed move leaves the index as it was.
func IndexRehash(index *layout.HashIndex, capacity uint32) bool {
	if capacity == 0 || capacity > maxIndexCapacity || capacity < index.Count {
		return false
	}
	if capacity == index.Capacity {
		return true
	}
	fresh := newEntries(index.MemoryContext.Get(), capacity)
	if fresh == nil {
		return false
	}
	slots := uint32(len(fresh))
	old := indexEntries(index)
	for _, entry := range old {
		if entry.Value == Invalid {
			continue
		}
		bucket := entry.Hash % slots
		for fresh[bucket].Value != Invalid {
			bucket = (bucket + 1) % slots
		}
		fresh[bucket] = entry
	}
	index.Entries.Set(&fresh[0])
	index.Capacity = capacity
	shmalloc.FreeArray(index.MemoryContext.Get(), old)
	return true
}

// IndexFini frees a hash index's entries, as hash_index_fini does.
func IndexFini(index *layout.HashIndex) {
	entries := indexEntries(index)
	if entries == nil {
		return
	}
	shmalloc.FreeArray(index.MemoryContext.Get(), entries)
	index.Entries.Set(nil)
	index.Capacity = 0
	index.Count = 0
}

// IndexLookup returns the first value stored under the hash that matches,
// or Invalid.
func IndexLookup(index *layout.HashIndex, hash uint32, match func(value uint32) bool) uint32 {
	if index.Capacity == 0 {
		return Invalid
	}
	entries := indexEntries(index)
	slots := uint32(len(entries))
	for bucket := hash % slots; entries[bucket].Value != Invalid; bucket = (bucket + 1) % slots {
		if entries[bucket].Hash == hash && match(entries[bucket].Value) {
			return entries[bucket].Value
		}
	}
	return Invalid
}

// IndexInsert stores a value under the hash, or reports the index full.
func IndexInsert(index *layout.HashIndex, hash, value uint32) bool {
	if index.Count >= index.Capacity {
		return false
	}
	entries := indexEntries(index)
	slots := uint32(len(entries))
	bucket := hash % slots
	for entries[bucket].Value != Invalid {
		bucket = (bucket + 1) % slots
	}
	entries[bucket] = layout.HashIndexEntry{Hash: hash, Value: value}
	index.Count++
	return true
}

// indexRemove removes the value stored under the hash, shifting back the
// entries whose probe paths crossed it, as hash_index_remove does.
func indexRemove(index *layout.HashIndex, hash, value uint32) bool {
	if index.Capacity == 0 {
		return false
	}
	entries := indexEntries(index)
	slots := uint32(len(entries))
	bucket := hash % slots
	for entries[bucket].Value != Invalid && (entries[bucket].Hash != hash || entries[bucket].Value != value) {
		bucket = (bucket + 1) % slots
	}
	if entries[bucket].Value == Invalid {
		return false
	}
	entries[bucket].Value = Invalid
	hole := bucket
	for candidate := (bucket + 1) % slots; entries[candidate].Value != Invalid; candidate = (candidate + 1) % slots {
		home := entries[candidate].Hash % slots
		if distance(home, hole, slots) < distance(home, candidate, slots) {
			entries[hole] = entries[candidate]
			entries[candidate].Value = Invalid
			hole = candidate
		}
	}
	index.Count--
	return true
}

// distance is the cyclic distance from one slot to another in probe
// order.
func distance(from, to, slots uint32) uint32 {
	if to >= from {
		return to - from
	}
	return uint32(uint64(to) + uint64(slots) - uint64(from))
}
