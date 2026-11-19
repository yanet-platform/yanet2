//go:build linux && (amd64 || arm64)

// Package cpconfig builds and publishes YANET configurations from Go
// without cgo, as lib/controlplane/config does: configurations, their
// counter registries and indexes, and the generations that publish them,
// laid out and hashed exactly as C lays them out and reads them.
package cpconfig

import (
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// strIndexInvalid marks a free slot of a string index (STR_INDEX_INVALID).
const strIndexInvalid = 0xffffffff

// strIndexSparseFactor keeps a string index at most a quarter full.
const strIndexSparseFactor = 4

// fnv1a is the string hash of common/str_index.h. Its tail of fewer than
// four bytes is hashed last byte first, as C hashes it: the slot a key
// lands in is part of the shared layout.
func fnv1a[Key string | []byte](key Key, seed uint32) uint64 {
	const prime = 1099511628211
	hash := uint64(14695981039346656037) ^ uint64(seed)
	whole := len(key) &^ 3
	for idx := range whole {
		hash ^= uint64(key[idx])
		hash *= prime
	}
	for idx := len(key) - 1; idx >= whole; idx-- {
		hash ^= uint64(key[idx])
		hash *= prime
	}
	return hash
}

// strIndexKey returns the bytes C hashes for a key: up to its first zero
// or keySize bytes.
func strIndexKey(key string, keySize int) string {
	length := min(len(key), keySize)
	for idx := range length {
		if key[idx] == 0 {
			length = idx
			break
		}
	}
	return key[:length]
}

// strIndexSlot returns the first slot a key probes.
func strIndexSlot(index *layout.StrIndex, key string, keySize int) uint32 {
	return uint32(fnv1a(strIndexKey(key, keySize), 0)) % index.Capacity
}

func strIndexValues(index *layout.StrIndex) []uint32 {
	values := index.Values.Get()
	if values == nil {
		return nil
	}
	return unsafe.Slice(values, index.Capacity)
}

func strIndexInit(index *layout.StrIndex, context *layout.MemoryContext) {
	index.MemoryContext.Set(context)
	index.Values.Set(nil)
	index.Capacity = 0
	index.Size = 0
}

// strIndexFini frees an index's values, as str_index_fini does.
func strIndexFini(index *layout.StrIndex) {
	values := index.Values.Get()
	if values == nil {
		return
	}
	shmalloc.ContextFree(index.MemoryContext.Get(), unsafe.Pointer(values), 4*uintptr(index.Capacity))
	index.Values.Set(nil)
}

// strIndexLookup returns the value whose key read equals key, or
// strIndexInvalid.
func strIndexLookup(index *layout.StrIndex, key string, keySize int, read func(value uint32) []byte) uint32 {
	if index.Capacity == 0 {
		return strIndexInvalid
	}
	values := strIndexValues(index)
	slot := strIndexSlot(index, key, keySize)
	want := strIndexKey(key, keySize)
	for values[slot] != strIndexInvalid {
		if string(read(values[slot])) == want {
			break
		}
		slot = (slot + 1) % index.Capacity
	}
	return values[slot]
}

func strIndexInsertForce(index *layout.StrIndex, key string, keySize int, value uint32) {
	values := strIndexValues(index)
	slot := strIndexSlot(index, key, keySize)
	for values[slot] != strIndexInvalid {
		slot = (slot + 1) % index.Capacity
	}
	values[slot] = value
	index.Size++
}

func strIndexExpand(index *layout.StrIndex, keySize int, capacity uint32, read func(value uint32) []byte) bool {
	context := index.MemoryContext.Get()
	block := shmalloc.ContextAlloc(context, uintptr(capacity)*4)
	if block == nil {
		return false
	}
	fresh := unsafe.Slice((*uint32)(block), capacity)
	for idx := range fresh {
		fresh[idx] = strIndexInvalid
	}
	old := strIndexValues(index)
	oldCapacity := index.Capacity
	index.Values.Set(&fresh[0])
	index.Capacity = capacity
	index.Size = 0
	for _, value := range old {
		if value != strIndexInvalid {
			strIndexInsertForce(index, string(read(value)), keySize, value)
		}
	}
	if old != nil {
		shmalloc.ContextFree(context, unsafe.Pointer(&old[0]), uintptr(oldCapacity)*4)
	}
	return true
}

// strIndexInsert adds a key, growing the index first when it is a quarter
// full. read returns the key of a value already in the index.
func strIndexInsert(index *layout.StrIndex, key string, keySize int, value uint32, read func(value uint32) []byte) bool {
	if index.Size >= index.Capacity/strIndexSparseFactor {
		grown := max(index.Size, 1) * strIndexSparseFactor * 2
		if !strIndexExpand(index, keySize, grown, read) {
			return false
		}
	}
	strIndexInsertForce(index, key, keySize, value)
	return true
}
