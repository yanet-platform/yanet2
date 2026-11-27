//go:build linux && (amd64 || arm64)

package registry

import (
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// KeyReader returns the key a string index stored a value under.
type KeyReader func(value uint32) []byte

func strValues(index *layout.StrIndex) []uint32 {
	return Slice(index.Values.Get(), uint64(index.Capacity))
}

// strSlot returns the first slot a key probes.
func strSlot(index *layout.StrIndex, key string, keySize int) uint32 {
	return uint32(FNV1a(FieldKey(key, keySize), 0)) % index.Capacity
}

// StrInit prepares an empty string index, as str_index_init does.
func StrInit(index *layout.StrIndex, context *layout.MemoryContext) {
	index.MemoryContext.Set(context)
	index.Values.Set(nil)
	index.Capacity = 0
	index.Size = 0
}

// StrFini frees a string index's slots, leaving its counts, as
// str_index_fini does.
func StrFini(index *layout.StrIndex) {
	values := strValues(index)
	if values == nil {
		return
	}
	shmalloc.FreeArray(index.MemoryContext.Get(), values)
	index.Values.Set(nil)
}

// StrLookup returns the value stored under a key, or Invalid.
func StrLookup(index *layout.StrIndex, key string, keySize int, read KeyReader) uint32 {
	if index.Capacity == 0 {
		return Invalid
	}
	values := strValues(index)
	want := FieldKey(key, keySize)
	slot := strSlot(index, key, keySize)
	for values[slot] != Invalid && string(read(values[slot])) != want {
		slot = (slot + 1) % index.Capacity
	}
	return values[slot]
}

// StrInsert stores a value under a key, first growing an index a quarter
// full and placing the keys it holds anew, as str_index_insert does.
func StrInsert(index *layout.StrIndex, key string, keySize int, value uint32, read KeyReader) bool {
	if index.Size >= index.Capacity/sparseFactor && !strExpand(index, keySize, read) {
		return false
	}
	strPlace(index, key, keySize, value)
	return true
}

// strPlace stores a value under a key in an index with room for it.
func strPlace(index *layout.StrIndex, key string, keySize int, value uint32) {
	values := strValues(index)
	slot := strSlot(index, key, keySize)
	for values[slot] != Invalid {
		slot = (slot + 1) % index.Capacity
	}
	values[slot] = value
	index.Size++
}

// strExpand moves a string index to twice the room its size asks for.
func strExpand(index *layout.StrIndex, keySize int, read KeyReader) bool {
	capacity := max(index.Size, 1) * sparseFactor * 2
	context := index.MemoryContext.Get()
	fresh, ok := shmalloc.AllocArray[uint32](context, uint64(capacity))
	if !ok {
		return false
	}
	for idx := range fresh {
		fresh[idx] = Invalid
	}
	old := strValues(index)
	index.Values.Set(&fresh[0])
	index.Capacity = capacity
	index.Size = 0
	for _, value := range old {
		if value != Invalid {
			strPlace(index, string(read(value)), keySize, value)
		}
	}
	shmalloc.FreeArray(context, old)
	return true
}
