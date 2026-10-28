#pragma once

#include <stdint.h>

#define MEMORY_BLOCK_ALLOCATOR_EXP 24

// A lock-consistent copy of free capacity; block sizes include sanitizer
// red zones rather than describing the maximum user payload.
struct block_allocator_stats {
	uint64_t free_bytes;
	uint64_t largest_free_block_bytes;
	uint64_t free_blocks[MEMORY_BLOCK_ALLOCATOR_EXP];
};
