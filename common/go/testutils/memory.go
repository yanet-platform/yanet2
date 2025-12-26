package testutils

//#cgo CFLAGS: -I../../..
/*

#include <stdlib.h>
#include "common/memory.h"
#include "common/asan.h"

bool asan_enabled() {
	#ifdef HAVE_ASAN
	return true;
	#else
	return false;
	#endif
}
size_t block_allocator_alignment() {
	return MEMORY_BLOCK_ALLOCATOR_MAX_ALIGN;
}
*/
import "C"
import (
	"unsafe"

	"github.com/c2h5oh/datasize"
)

type MemoryContext struct {
	ptr unsafe.Pointer
}

func NewMemoryContext(name string, size datasize.ByteSize) MemoryContext {
	sizeOfArena := C.size_t(size)

	arena := C.malloc(sizeOfArena + C.sizeof_struct_memory_context + C.sizeof_struct_block_allocator)

	memCtx := (*C.struct_memory_context)(arena)
	arena = unsafe.Add(arena, C.sizeof_struct_memory_context)
	blockAlloc := (*C.struct_block_allocator)(arena)
	arena = unsafe.Add(arena, C.sizeof_struct_block_allocator)

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

// CPAlignmentOverhead returns the memory overhead for block allocator alignment
// and memory sanitizer overhead if enabled.
//
// The overhead consists of:
// - Block allocator alignment overhead: 2MB (may be smaller but never larger in some cases)
// - Sanitizer RED ZONES overhead: ~4MB (if sanitizers are enabled; empirical value that depends on allocation count)
func CPAlignmentOverhead() datasize.ByteSize {
	// Block allocator alignment overhead on max block size
	// This value can be smaller but not bigger in some circumstances.
	// Using max possible value here seems ok for tests.
	alignmentOverhead := datasize.ByteSize(C.block_allocator_alignment())

	overhead := alignmentOverhead

	// Check if any sanitizer is enabled via runtime options
	// Note: This is a workaround since CGO_CFLAGS/CGO_LDFLAGS are not available at runtime
	if AsanEnabled() {
		// Memory Sanitizer RED ZONES overhead
		overhead += datasize.MB * 4
	}

	return overhead
}

func AsanEnabled() bool {
	return bool(C.asan_enabled())
}
