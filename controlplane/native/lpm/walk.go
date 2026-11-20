//go:build linux && (amd64 || arm64)

package lpm

import (
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// Walk calls visit with every stretch of keys the tree maps to one
// value, in ascending key order, one per run of neighbouring slots of a
// page that hold one value: from and to are the first and last key of
// the stretch, keySize bytes each, valid only during the call.
func Walk(tree *layout.Lpm, keySize int, visit func(from, to []byte, value uint32)) {
	from := make([]byte, keySize)
	to := make([]byte, keySize)
	walkPage(page(tree, 0), 0, from, to, visit)
}

// Free releases the pages of a tree and unlinks its memory context, as
// lpm_free does.
func Free(tree *layout.Lpm) {
	context := &tree.MemoryContext
	if directory := tree.Pages.Get(); directory != nil {
		chunkCount := (tree.PageCount + chunkSize - 1) / chunkSize
		entries := chunks(tree, chunkCount)
		for idx := range entries {
			shmalloc.ContextFree(context, unsafe.Pointer(entries[idx].Get()), unsafe.Sizeof(layout.LpmPage{})*chunkSize)
		}
		shmalloc.ContextFree(context, unsafe.Pointer(directory), unsafe.Sizeof(shm.Ptr[layout.LpmPage]{})*uintptr(chunkCapacity(chunkCount)))
		tree.Pages.Set(nil)
		tree.PageCount = 0
	}
	shmalloc.ContextFini(context)
}

func walkPage(current *layout.LpmPage, hop int, from, to []byte, visit func(from, to []byte, value uint32)) {
	for key := 0; key < len(current.Values); key++ {
		slot := &current.Values[key]
		from[hop] = byte(key)
		value := *slotValue(slot)
		if value&valueFlag == 0 {
			to[hop] = byte(key)
			walkPage(slotPage(slot).Get(), hop+1, from, to, visit)
			continue
		}
		for key+1 < len(current.Values) && *slotValue(&current.Values[key+1]) == value {
			key++
		}
		to[hop] = byte(key)
		for rest := hop + 1; rest < len(from); rest++ {
			from[rest] = 0
			to[rest] = 0xff
		}
		visit(from, to, uint32(value>>1))
	}
}
