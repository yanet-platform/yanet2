package memoryowner

/*
#cgo CFLAGS: -I../../..
#include <stdlib.h>
#include "common/memory.h"
#include "common/memory_owner.h"

// Reads an arena's borrowed size through the offset-pointer discipline;
// the ADDR_OF macro itself is not callable from Go.
static size_t owner_arena_size(struct memory_owner *owner, uint64_t idx) {
	struct memory_arena *arenas = ADDR_OF(&owner->arenas);
	return (size_t)arenas[idx].size;
}
*/
import "C"

import (
	"unsafe"
)

// Go regression coverage for the C memory_owner growth path: the
// sanitized-build and thread-stress cases stay in the C suites, while
// the behavioral contract — grow on miss, truthful exhaustion, failure
// propagation, no surplus granules under concurrent misses, and exact
// release — is exercised through the production headers.

const (
	arenaAlign = 1 << 21
	minGranule = 64 << 10
)

// ownerFixture is a parent context over one malloc'd arena plus an
// owner bound to it, both driven through the production headers.
type ownerFixture struct {
	raw      unsafe.Pointer
	ba       *C.struct_block_allocator
	ctx      *C.struct_memory_context
	owner    *C.struct_memory_owner
	octx     *C.struct_memory_context
	baseline uint64
}

// newOwnerFixture builds a parent context over one arena of arenaSz
// bytes plus a 4 KiB slack block, and an owner bound to it. The slack
// funds the owner's arenas tracking array so the first growth succeeds;
// passing arenaSz below the granule size starves it on purpose.
func newOwnerFixture(arenaSz uint64) *ownerFixture {
	f := &ownerFixture{}
	f.raw = C.malloc(C.size_t(arenaSz + 4096 + arenaAlign))
	if f.raw == nil {
		panic("arena malloc failed")
	}

	f.ba = (*C.struct_block_allocator)(C.malloc(C.sizeof_struct_block_allocator))
	f.ctx = (*C.struct_memory_context)(C.malloc(C.sizeof_struct_memory_context))
	f.owner = (*C.struct_memory_owner)(C.malloc(C.sizeof_struct_memory_owner))
	f.octx = (*C.struct_memory_context)(C.malloc(C.sizeof_struct_memory_context))
	if f.ba == nil || f.ctx == nil || f.owner == nil || f.octx == nil {
		panic("struct malloc failed")
	}

	if rc := C.block_allocator_init(f.ba); rc != 0 {
		panic("block_allocator_init failed")
	}
	C.block_allocator_put_arena(
		f.ba,
		unsafe.Pointer((uintptr(f.raw)+arenaAlign-1)&^uintptr(arenaAlign-1)),
		C.size_t(arenaSz+4096),
	)

	name := C.CString("go-parent")
	rc := C.memory_context_init(f.ctx, name, f.ba)
	C.free(unsafe.Pointer(name))
	if rc != 0 {
		panic("memory_context_init failed")
	}
	oname := C.CString("go-owner")
	rc = C.memory_owner_init(f.owner, f.ctx, oname)
	C.free(unsafe.Pointer(oname))
	if rc != 0 {
		panic("memory_owner_init failed")
	}
	oname2 := C.CString("go-owner-alloc")
	rc = C.memory_context_init(f.octx, oname2, &f.owner.allocator)
	C.free(unsafe.Pointer(oname2))
	if rc != 0 {
		panic("owner context init failed")
	}
	f.baseline = f.freeSize()
	return f
}

func (f *ownerFixture) fini() {
	C.memory_context_fini(f.octx)
	C.memory_context_fini(f.ctx)
	C.free(unsafe.Pointer(f.octx))
	C.free(unsafe.Pointer(f.owner))
	C.free(unsafe.Pointer(f.ctx))
	C.free(unsafe.Pointer(f.ba))
	C.free(f.raw)
}

// balloc requests sz bytes through the owner-bound context.
func (f *ownerFixture) balloc(sz uint64) unsafe.Pointer {
	return C.memory_balloc(f.octx, C.size_t(sz))
}

// bfree returns a block of sz bytes through the owner-bound context.
func (f *ownerFixture) bfree(p unsafe.Pointer, sz uint64) {
	C.memory_bfree(f.octx, p, C.size_t(sz))
}

// releaseAll returns every borrowed arena to the parent context.
func (f *ownerFixture) releaseAll() {
	C.memory_owner_release_all(f.owner)
}

func (f *ownerFixture) freeSize() uint64 {
	return uint64(C.block_allocator_free_size(f.ba))
}

func (f *ownerFixture) ownerFreeSize() uint64 {
	return uint64(C.block_allocator_free_size(&f.owner.allocator))
}

func (f *ownerFixture) arenaCount() int {
	return int(f.owner.arena_count)
}

// redZone is the C sanitizer red-zone width of this build.
func redZone() uint64 {
	return uint64(C.ASAN_RED_ZONE)
}

// granuleBlock is the parent pool block one minimum granule occupies.
func granuleBlock() uint64 {
	return uint64(C.block_allocator_pool_size(
		nil,
		C.block_allocator_pool_index(nil, minGranule+2*C.ASAN_RED_ZONE),
	))
}

// reqBlock is the owner pool block one request of sz user bytes lands in.
func reqBlock(sz uint64) uint64 {
	return uint64(C.block_allocator_pool_size(
		nil,
		C.block_allocator_pool_index(nil, C.size_t(sz+2*C.ASAN_RED_ZONE)),
	))
}

// ingestedTotal sums the pool-rounded blocks of every borrowed arena.
func (f *ownerFixture) ingestedTotal() uint64 {
	total := uint64(0)
	for idx := C.uint64_t(0); idx < f.owner.arena_count; idx++ {
		size := C.owner_arena_size(f.owner, idx)
		total += uint64(C.block_allocator_pool_size(
			&f.owner.allocator,
			C.block_allocator_pool_index(
				&f.owner.allocator,
				size+2*C.ASAN_RED_ZONE,
			),
		))
	}
	return total
}
