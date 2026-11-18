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
	shm.LockByte(&allocator.Lock.Locked)
	defer shm.UnlockByte(&allocator.Lock.Locked)
	context.NextSibling.Set(parent.FirstChild.Get())
	parent.FirstChild.Set(context)
}

// ContextFini unlinks context from its parent, detaches its children and
// zeroes it. A zeroed context is left as it is.
func ContextFini(context *layout.MemoryContext) {
	allocator := context.BlockAllocator.Get()
	if allocator == nil {
		return
	}
	shm.LockByte(&allocator.Lock.Locked)
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
	shm.UnlockByte(&allocator.Lock.Locked)
	*context = layout.MemoryContext{}
}

// ContextAlloc returns a block of size bytes from the context's allocator
// and counts it against the context, or nil.
func ContextAlloc(context *layout.MemoryContext, size uintptr) unsafe.Pointer {
	block := Alloc(context.BlockAllocator.Get(), size)
	if block == nil {
		return nil
	}
	atomic.AddUint64(&context.BallocCount, 1)
	atomic.AddUint64(&context.BallocSize, uint64(size))
	return block
}

// ContextFree returns a block ContextAlloc gave.
func ContextFree(context *layout.MemoryContext, block unsafe.Pointer, size uintptr) {
	if block == nil || size == 0 {
		return
	}
	atomic.AddUint64(&context.BfreeCount, 1)
	atomic.AddUint64(&context.BfreeSize, uint64(size))
	Free(context.BlockAllocator.Get(), block, size)
}

// ContextRealloc moves a block to one of newSize bytes, keeping the bytes
// both sizes cover, as memory_brealloc does: a zero newSize frees it.
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
		copy(unsafe.Slice((*byte)(moved), min(oldSize, newSize)), unsafe.Slice((*byte)(data), min(oldSize, newSize)))
		ContextFree(context, data, oldSize)
	}
	return moved
}
