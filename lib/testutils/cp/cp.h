#pragma once

#include <stddef.h>
#include <stdint.h>

#include "lib/errors/errors.h"

// Opaque handle to the in-process test shared memory arena.
struct test_shm;

// Create an in-process arena that mimics a YANET shared memory segment.
//
// Allocates a single contiguous block, lays out dp_config at the front and
// cp_config at offset dp_size, initializes both allocators, cross-links them
// via offset pointers, allocates the cp_agent_registry and cp_config_gen
// stubs, and registers each module named in module_names by resolving
// new_module_<name> from the current process image.
//
// cp_size and dp_size are rounded up to 64-byte alignment internally.
// module_name_count may be 0; in that case module_names is ignored.
//
// Returns NULL and sets *err on failure.
struct test_shm *
test_shm_create(
	size_t cp_size,
	size_t dp_size,
	const char *const *module_names,
	size_t module_name_count,
	yanet_error **err
);

// Release all resources owned by shm, including the arena allocation.
//
// Logs a warning if the cp_config block allocator free size differs from the
// baseline captured after create finished its own bookkeeping allocations.
void
test_shm_destroy(struct test_shm *shm);

// Return the dp_config pointer for the arena owned by shm.
//
// Owned by shm; do not free the returned pointer.
void *
test_shm_dp_config(struct test_shm *shm);
