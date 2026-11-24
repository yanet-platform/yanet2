//go:build linux && (amd64 || arm64)

// Package coracle runs the C code the native control plane reimplements,
// as the oracle its tests compare against.
//
// It uses cgo and only tests import it. The native control plane uses cgo
// in one other place: the AddressSanitizer-tagged file of the allocator,
// which reaches the sanitizer's interface; every other native package
// builds without cgo.
package coracle

/*
#cgo CFLAGS: -I${SRCDIR}/../../../..
#include <stdint.h>
#include <stdlib.h>
#include "common/memory.h"

// coracle_churn allocates blocks of the given sizes and frees them again,
// rounds times, filling each block with mark and checking the mark before
// the free. It returns the number of blocks found overwritten, or
// UINT64_MAX when an allocation fails.
static uint64_t
coracle_churn(
	struct block_allocator *allocator,
	const size_t *sizes,
	size_t count,
	size_t rounds,
	uint8_t mark
) {
	uint8_t **blocks = calloc(count, sizeof(*blocks));
	if (blocks == NULL) {
		return UINT64_MAX;
	}
	uint64_t overwritten = 0;
	for (size_t round = 0; round < rounds; ++round) {
		for (size_t idx = 0; idx < count; ++idx) {
			blocks[idx] = block_allocator_balloc(allocator, sizes[idx]);
			if (blocks[idx] == NULL) {
				for (size_t done = 0; done < idx; ++done) {
					block_allocator_bfree(
						allocator, blocks[done], sizes[done]
					);
				}
				free(blocks);
				return UINT64_MAX;
			}
			memset(blocks[idx], mark, sizes[idx]);
		}
		for (size_t idx = 0; idx < count; ++idx) {
			for (size_t pos = 0; pos < sizes[idx]; ++pos) {
				if (blocks[idx][pos] != mark) {
					++overwritten;
					break;
				}
			}
			block_allocator_bfree(allocator, blocks[idx], sizes[idx]);
		}
	}
	free(blocks);
	return overwritten;
}

// coracle_context_churn initialises transient as a child of root and
// finalises it again, rounds times, allocating blocks of the given sizes
// through transient, persistent and root in turn while it lives, filling
// each block with mark and checking the mark before the free. It returns
// the number of blocks found overwritten, or UINT64_MAX when an
// allocation fails.
static uint64_t
coracle_context_churn(
	struct memory_context *root,
	struct memory_context *persistent,
	struct memory_context *transient,
	const size_t *sizes,
	size_t count,
	size_t rounds,
	uint8_t mark
) {
	uint8_t **blocks = calloc(count, sizeof(*blocks));
	if (blocks == NULL) {
		return UINT64_MAX;
	}
	struct memory_context *owners[3] = {transient, persistent, root};
	uint64_t overwritten = 0;
	for (size_t round = 0; round < rounds; ++round) {
		memory_context_init_from(transient, root, "transient");
		for (size_t idx = 0; idx < count; ++idx) {
			blocks[idx] = memory_balloc(owners[idx % 3], sizes[idx]);
			if (blocks[idx] == NULL) {
				for (size_t done = 0; done < idx; ++done) {
					memory_bfree(
						owners[done % 3],
						blocks[done],
						sizes[done]
					);
				}
				memory_context_fini(transient);
				free(blocks);
				return UINT64_MAX;
			}
			memset(blocks[idx], mark, sizes[idx]);
		}
		for (size_t idx = 0; idx < count; ++idx) {
			for (size_t pos = 0; pos < sizes[idx]; ++pos) {
				if (blocks[idx][pos] != mark) {
					++overwritten;
					break;
				}
			}
			memory_bfree(owners[idx % 3], blocks[idx], sizes[idx]);
		}
		memory_context_fini(transient);
	}
	free(blocks);
	return overwritten;
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

// The C build constants the native allocator mirrors.
const (
	PoolCount = C.MEMORY_BLOCK_ALLOCATOR_EXP
	MinBits   = C.MEMORY_BLOCK_ALLOCATOR_MIN_BITS
	MaxAlign  = C.MEMORY_BLOCK_ALLOCATOR_MAX_ALIGN
	RedZone   = C.ASAN_RED_ZONE
	MaxSize   = C.MEMORY_BLOCK_ALLOCATOR_MAX_SIZE
)

// BlockAllocatorInit runs block_allocator_init.
func BlockAllocatorInit(allocator unsafe.Pointer) {
	C.block_allocator_init((*C.struct_block_allocator)(allocator))
}

// PutArena runs block_allocator_put_arena.
func PutArena(allocator, arena unsafe.Pointer, size uintptr) {
	C.block_allocator_put_arena(
		(*C.struct_block_allocator)(allocator), arena, C.size_t(size),
	)
}

// Alloc runs block_allocator_balloc.
func Alloc(allocator unsafe.Pointer, size uintptr) unsafe.Pointer {
	return C.block_allocator_balloc(
		(*C.struct_block_allocator)(allocator), C.size_t(size),
	)
}

// Free runs block_allocator_bfree.
func Free(allocator, block unsafe.Pointer, size uintptr) {
	C.block_allocator_bfree(
		(*C.struct_block_allocator)(allocator), block, C.size_t(size),
	)
}

// FreeSize runs block_allocator_free_size.
func FreeSize(allocator unsafe.Pointer) uintptr {
	return uintptr(C.block_allocator_free_size(
		(*C.struct_block_allocator)(allocator),
	))
}

// ErrChurnNoMemory reports a churn whose allocation found no free block.
var ErrChurnNoMemory = errors.New("C churn ran out of blocks")

// Churn allocates blocks of sizes and frees them again, rounds times, in
// one C call, and returns how many blocks another allocation overwrote
// while held.
//
// Every block is filled with mark and checked before its free, so a block
// handed out twice shows as overwritten. The call runs on its own thread
// for its whole length, so concurrent calls contend on the allocator's
// lock in C.
func Churn(allocator unsafe.Pointer, sizes []uintptr, rounds int, mark byte) (int, error) {
	if len(sizes) == 0 {
		return 0, nil
	}
	cSizes := make([]C.size_t, len(sizes))
	for idx, size := range sizes {
		cSizes[idx] = C.size_t(size)
	}
	overwritten := C.coracle_churn(
		(*C.struct_block_allocator)(allocator),
		&cSizes[0],
		C.size_t(len(cSizes)),
		C.size_t(rounds),
		C.uint8_t(mark),
	)
	if overwritten == C.UINT64_MAX {
		return 0, ErrChurnNoMemory
	}
	return int(overwritten), nil
}

// ContextChurn initialises transient as a child of root and finalises it
// again, rounds times, in one C call, and returns how many blocks another
// allocation overwrote while held.
//
// While transient lives, blocks of sizes are allocated through transient,
// persistent and root in turn, each filled with mark and checked before
// its free. The call runs on its own thread for its whole length, so
// concurrent calls contend on the allocator's lock in C.
func ContextChurn(root, persistent, transient unsafe.Pointer, sizes []uintptr, rounds int, mark byte) (int, error) {
	if len(sizes) == 0 {
		return 0, nil
	}
	cSizes := make([]C.size_t, len(sizes))
	for idx, size := range sizes {
		cSizes[idx] = C.size_t(size)
	}
	overwritten := C.coracle_context_churn(
		(*C.struct_memory_context)(root),
		(*C.struct_memory_context)(persistent),
		(*C.struct_memory_context)(transient),
		&cSizes[0],
		C.size_t(len(cSizes)),
		C.size_t(rounds),
		C.uint8_t(mark),
	)
	if overwritten == C.UINT64_MAX {
		return 0, ErrChurnNoMemory
	}
	return int(overwritten), nil
}

// ContextInit runs memory_context_init.
func ContextInit(context unsafe.Pointer, name string, allocator unsafe.Pointer) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	C.memory_context_init(
		(*C.struct_memory_context)(context),
		cName,
		(*C.struct_block_allocator)(allocator),
	)
}

// ContextInitFrom runs memory_context_init_from.
func ContextInitFrom(context, parent unsafe.Pointer, name string) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	C.memory_context_init_from(
		(*C.struct_memory_context)(context),
		(*C.struct_memory_context)(parent),
		cName,
	)
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

// ContextRealloc runs memory_brealloc.
func ContextRealloc(context, data unsafe.Pointer, oldSize, newSize uintptr) unsafe.Pointer {
	return C.memory_brealloc(
		(*C.struct_memory_context)(context),
		data,
		C.size_t(oldSize),
		C.size_t(newSize),
	)
}
