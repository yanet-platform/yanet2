//go:build linux && (amd64 || arm64)

// Package registry keeps the registries, hash indexes and string indexes
// of YANET's control-plane configuration in shared memory.
//
// They keep the format lib/controlplane and common read. A registry is a slot
// array whose empty slots chain to each other inside the array, with a hash
// index of its keys; a string index finds counter names. Blocks come from and
// go back to the memory contexts at once and reference counts move
// atomically, as C's registries do. The package is shared by the
// configuration builder and teardown, so each rule of the format exists once.
package registry

import (
	"iter"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
)

// Slice views a number of elements from an address, or returns nil for a
// nil address.
func Slice[T any](first *T, count uint64) []T {
	if first == nil {
		return nil
	}
	return unsafe.Slice(first, count)
}

// Pointees yields what a pointer array points to, in order, skipping nil
// pointers.
func Pointees[T any](pointers []shm.Ptr[T]) iter.Seq[*T] {
	return func(yield func(*T) bool) {
		for idx := range pointers {
			if target := pointers[idx].Get(); target != nil && !yield(target) {
				return
			}
		}
	}
}

// Container returns the structure a registry item is embedded in at that
// offset.
func Container[T any](item *layout.RegistryItem, offset uintptr) *T {
	return (*T)(unsafe.Add(unsafe.Pointer(item), -int(offset)))
}
