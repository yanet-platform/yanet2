//go:build linux && (amd64 || arm64)

package shmalloc

import (
	"sync/atomic"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
)

// ContextInit makes context a root memory context allocating from
// allocator.
func ContextInit(context *layout.MemoryContext, name string, allocator *layout.BlockAllocator) {
	context.BallocCount = 0
	context.BfreeCount = 0
	context.BallocSize = 0
	context.BfreeSize = 0
	context.BlockAllocator.Set(allocator)
	shm.CopyString(context.Name[:], name)
	context.Parent.Set(nil)
	context.FirstChild.Set(nil)
	context.NextSibling.Set(nil)
}

// ContextInitFrom makes context a child of parent, allocating from the
// parent's allocator, at the head of the parent's children.
func ContextInitFrom(context, parent *layout.MemoryContext, name string) {
	context.BallocCount = 0
	context.BfreeCount = 0
	context.BallocSize = 0
	context.BfreeSize = 0
	allocator := parent.BlockAllocator.Get()
	context.BlockAllocator.Set(allocator)
	shm.CopyString(context.Name[:], name)
	context.Parent.Set(parent)
	context.FirstChild.Set(nil)

	// Parent and child share the allocator, whose lock guards the tree.
	Lock(allocator)
	defer Unlock(allocator)
	context.NextSibling.Set(parent.FirstChild.Get())
	parent.FirstChild.Set(context)
}

// ContextFini unlinks context from its parent, detaches its children and
// zeroes it.
//
// A zeroed context is left as it is, so finalising twice, or a parent
// before its children, is safe.
func ContextFini(context *layout.MemoryContext) {
	allocator := context.BlockAllocator.Get()
	if allocator == nil {
		return
	}
	Lock(allocator)
	defer Unlock(allocator)
	if parent := context.Parent.Get(); parent != nil {
		cursor := &parent.FirstChild
		for child := cursor.Get(); child != nil; child = cursor.Get() {
			if child == context {
				cursor.Set(context.NextSibling.Get())
				break
			}
			cursor = &child.NextSibling
		}
	}
	for child := context.FirstChild.Get(); child != nil; {
		next := child.NextSibling.Get()
		child.Parent.Set(nil)
		child.NextSibling.Set(nil)
		child = next
	}
	*context = layout.MemoryContext{}
}

// ContextAlloc returns a block of size bytes from the context's allocator
// and counts it against the context, or nil as Alloc does.
func ContextAlloc(context *layout.MemoryContext, size uintptr) unsafe.Pointer {
	allocator := context.BlockAllocator.Get()
	Lock(allocator)
	defer Unlock(allocator)
	return ContextAllocLocked(context, size)
}

// ContextAllocLocked is ContextAlloc for a caller holding the lock of the
// context's allocator.
func ContextAllocLocked(context *layout.MemoryContext, size uintptr) unsafe.Pointer {
	block := AllocLocked(context.BlockAllocator.Get(), size)
	if block == nil {
		return nil
	}
	atomic.AddUint64(&context.BallocCount, 1)
	atomic.AddUint64(&context.BallocSize, uint64(size))
	return block
}

// ContextFree returns a block ContextAlloc gave and counts it against the
// context; a nil block or a zero size is ignored.
func ContextFree(context *layout.MemoryContext, block unsafe.Pointer, size uintptr) {
	if block == nil || size == 0 {
		return
	}
	allocator := context.BlockAllocator.Get()
	Lock(allocator)
	defer Unlock(allocator)
	contextFree(context, block, size)
}

// ContextFreeLocked is ContextFree for a caller holding the lock of the
// context's allocator.
func ContextFreeLocked(context *layout.MemoryContext, block unsafe.Pointer, size uintptr) {
	if block == nil || size == 0 {
		return
	}
	contextFree(context, block, size)
}

// contextFree counts a block against the context and frees it, for a
// caller holding the allocator's lock with a block and a nonzero size.
func contextFree(context *layout.MemoryContext, block unsafe.Pointer, size uintptr) {
	atomic.AddUint64(&context.BfreeCount, 1)
	atomic.AddUint64(&context.BfreeSize, uint64(size))
	FreeLocked(context.BlockAllocator.Get(), block, size)
}

// ContextRealloc moves a block of oldSize bytes to a new one of newSize
// bytes, keeping the bytes both sizes cover, as memory_brealloc does.
//
// A zero newSize frees the block and returns nil; a zero oldSize only
// allocates. When no block of newSize is free, it returns nil and leaves
// the old block as it was.
func ContextRealloc(context *layout.MemoryContext, data unsafe.Pointer, oldSize, newSize uintptr) unsafe.Pointer {
	if newSize == 0 {
		ContextFree(context, data, oldSize)
		return nil
	}
	moved := ContextAlloc(context, newSize)
	if moved == nil {
		return nil
	}
	if oldSize != 0 {
		kept := min(oldSize, newSize)
		copy(unsafe.Slice((*byte)(moved), kept), unsafe.Slice((*byte)(data), kept))
		ContextFree(context, data, oldSize)
	}
	return moved
}

// AllocArray allocates count elements of T from a context, holding the
// bytes the allocator left, and reports whether it got them.
//
// No elements take no block.
func AllocArray[T any](context *layout.MemoryContext, count uint64) ([]T, bool) {
	if count == 0 {
		return nil, true
	}
	block := ContextAlloc(context, unsafe.Sizeof(*new(T))*uintptr(count))
	if block == nil {
		return nil, false
	}
	return unsafe.Slice((*T)(block), count), true
}

// FreeArray returns an array to a context at the size of its elements;
// an empty array is ignored.
func FreeArray[T any](context *layout.MemoryContext, array []T) {
	if len(array) == 0 {
		return
	}
	ContextFree(context, unsafe.Pointer(&array[0]), unsafe.Sizeof(array[0])*uintptr(len(array)))
}

// AllocNode allocates a T followed by tail bytes from a context, holding
// the bytes the allocator left, or returns nil.
func AllocNode[T any](context *layout.MemoryContext, tail uintptr) *T {
	return (*T)(ContextAlloc(context, unsafe.Sizeof(*new(T))+tail))
}

// FreeNode returns a node followed by tail bytes to a context; a nil
// node is ignored.
func FreeNode[T any](context *layout.MemoryContext, node *T, tail uintptr) {
	if node == nil {
		return
	}
	ContextFree(context, unsafe.Pointer(node), unsafe.Sizeof(*node)+tail)
}
