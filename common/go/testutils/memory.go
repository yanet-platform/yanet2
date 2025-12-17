package testutils

//#cgo CFLAGS: -I../../..
/*

#include <stdlib.h>
#include <common/memory.h>

*/
import "C"
import "unsafe"

type MemoryContext struct {
	ptr unsafe.Pointer
}

func NewMemoryContext(name string, size uint64) MemoryContext {
	sizeOfArena := C.size_t(size)

	arena := C.malloc(sizeOfArena + C.sizeof_struct_memory_context + C.sizeof_struct_block_allocator)

	memCtx := (*C.struct_memory_context)(arena)
	arena = unsafe.Pointer(uintptr(arena) + C.sizeof_struct_memory_context)
	blockAlloc := (*C.struct_block_allocator)(arena)
	arena = unsafe.Pointer(uintptr(arena) + C.sizeof_struct_block_allocator)

	C.block_allocator_init(blockAlloc)
	C.block_allocator_put_arena(blockAlloc, arena, sizeOfArena)

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	C.memory_context_init(memCtx, cName, blockAlloc)

	return MemoryContext{
		ptr: unsafe.Pointer(memCtx),
	}
}

func (m *MemoryContext) Free() {
	C.free(m.ptr)
}

func (m *MemoryContext) AsRawPtr() unsafe.Pointer {
	return m.ptr
}
