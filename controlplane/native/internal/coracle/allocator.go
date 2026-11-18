//go:build linux && (amd64 || arm64)

// Package coracle runs the C code the native control plane reimplements,
// as the oracle its tests compare against. Only tests import it: it is
// the one place of the native control plane that uses cgo.
package coracle

/*
#cgo CFLAGS: -I${SRCDIR}/../../../..
#include "common/memory.h"
*/
import "C"

import "unsafe"

// The C build constants the native allocator mirrors.
const (
	PoolCount = C.MEMORY_BLOCK_ALLOCATOR_EXP
	RedZone   = C.ASAN_RED_ZONE
	MaxSize   = C.MEMORY_BLOCK_ALLOCATOR_MAX_SIZE
)

// BlockAllocatorInit runs block_allocator_init.
func BlockAllocatorInit(allocator unsafe.Pointer) {
	C.block_allocator_init((*C.struct_block_allocator)(allocator))
}

// PutArena runs block_allocator_put_arena.
func PutArena(allocator, arena unsafe.Pointer, size uintptr) {
	C.block_allocator_put_arena((*C.struct_block_allocator)(allocator), arena, C.size_t(size))
}

// Alloc runs block_allocator_balloc.
func Alloc(allocator unsafe.Pointer, size uintptr) unsafe.Pointer {
	return C.block_allocator_balloc((*C.struct_block_allocator)(allocator), C.size_t(size))
}

// Free runs block_allocator_bfree.
func Free(allocator, block unsafe.Pointer, size uintptr) {
	C.block_allocator_bfree((*C.struct_block_allocator)(allocator), block, C.size_t(size))
}

// ContextInit runs memory_context_init.
func ContextInit(context unsafe.Pointer, name string, allocator unsafe.Pointer) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	C.memory_context_init((*C.struct_memory_context)(context), cName, (*C.struct_block_allocator)(allocator))
}

// ContextInitFrom runs memory_context_init_from.
func ContextInitFrom(context, parent unsafe.Pointer, name string) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	C.memory_context_init_from((*C.struct_memory_context)(context), (*C.struct_memory_context)(parent), cName)
}

// ContextFini runs memory_context_fini.
func ContextFini(context unsafe.Pointer) {
	C.memory_context_fini((*C.struct_memory_context)(context))
}

// ContextAlloc runs memory_balloc.
func ContextAlloc(context unsafe.Pointer, size uintptr) unsafe.Pointer {
	return C.memory_balloc((*C.struct_memory_context)(context), C.size_t(size))
}

// ContextFree runs memory_bfree.
func ContextFree(context, block unsafe.Pointer, size uintptr) {
	C.memory_bfree((*C.struct_memory_context)(context), block, C.size_t(size))
}
