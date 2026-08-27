#pragma once

#include <stdint.h>

#include "common/memory.h"
#include "common/str_index.h"
#include "lib/errors/errors.h"

#define COUNTER_MAX_SIZE_EXP 6
#define COUNTER_POOL_SIZE (COUNTER_MAX_SIZE_EXP + 1)
#define COUNTER_STORAGE_PAGE_SIZE 4096

#ifndef CP_MODULE_DATA_NAME_LEN
#define CP_MODULE_DATA_NAME_LEN 80
#define CP_PIPELINE_NAME_LEN 80
#endif

// The name field leaves room for the three uint64_t fields of struct
// counter, so one entry is exactly 128 bytes: two cache lines, and a
// power-of-two entry count packs a chunk without padding.
#define COUNTER_NAME_LEN (128 - 8 - 8 - 8)
#define COUNTER_INVALID (uint64_t)-1

struct counter {
	char name[COUNTER_NAME_LEN];
	uint64_t size;
	uint64_t gen;
	uint64_t offset;
};

_Static_assert(
	sizeof(struct counter) == 128,
	"counter entry must stay exactly 128 bytes"
);

// Entries per registry chunk: 64 entries pack into one 8 KiB allocator
// class, so a chunk request never fragments a higher class and growth
// caps the arena's largest registry demand at one small page whatever
// the counter count.
#define COUNTER_REGISTRY_CHUNK 64

// Chunk-pointer slots per directory page, also one 8 KiB allocator class.
//
// A flat pointer array indexed by chunk would itself cross the one-chunk
// bound at 65,536 counters (1,025 pointers), so chunk pointers live in
// fixed directory pages instead: growth adds at most one entry chunk or
// one directory page, never a larger block.
#define COUNTER_REGISTRY_DIR_SLOTS 1024

_Static_assert(
	COUNTER_REGISTRY_CHUNK * sizeof(struct counter) == 8192,
	"registry chunk must pack exactly into the 8 KiB allocator class"
);
_Static_assert(
	COUNTER_REGISTRY_DIR_SLOTS * sizeof(struct counter *) == 8192,
	"registry directory page must pack exactly into the 8 KiB class"
);

struct counter_registry {
	struct memory_context *memory_context;
	uint64_t gen;
	uint64_t capacity;
	uint64_t count;
	uint64_t counts[COUNTER_POOL_SIZE];

	// Chunked names storage: entry idx lives in chunk slot
	// idx / COUNTER_REGISTRY_CHUNK of the chunk pointed at by directory
	// page (idx / COUNTER_REGISTRY_CHUNK) / COUNTER_REGISTRY_DIR_SLOTS,
	// slot (idx / COUNTER_REGISTRY_CHUNK) %
	// COUNTER_REGISTRY_DIR_SLOTS. capacity counts entries and is always
	// a multiple of COUNTER_REGISTRY_CHUNK; dir_page_count is the
	// allocated length of the pages array, which stays a handful of
	// pointers for any realistic registry. All pointers are
	// shared-memory relative.
	struct counter ***dirs;
	uint64_t dir_page_count;
	struct str_index str_index;
};

// Entry idx of registry, chunk- and directory-resolved.
//
// Valid for idx < registry->count only: the chunk slots beyond the last
// allocated chunk are never materialized.
static inline struct counter *
counter_registry_entry(struct counter_registry *registry, uint64_t idx) {
	uint64_t slot = idx / COUNTER_REGISTRY_CHUNK;
	struct counter ***dirs = ADDR_OF(&registry->dirs);
	struct counter **page =
		ADDR_OF(dirs + slot / COUNTER_REGISTRY_DIR_SLOTS);
	return ADDR_OF(page + slot % COUNTER_REGISTRY_DIR_SLOTS) +
	       idx % COUNTER_REGISTRY_CHUNK;
}

int
counter_registry_init(
	struct counter_registry *registry,
	struct memory_context *memory_context,
	uint64_t gen
);

uint64_t
counter_registry_register(
	struct counter_registry *registry,
	const char *name,
	uint64_t size,
	yanet_error **err
);

void
counter_registry_fini(struct counter_registry *registry);

// Index of the registered name, or (uint64_t)-1 when absent.
uint64_t
counter_registry_lookup_index(
	struct counter_registry *registry, const char *name
);

int
counter_registry_link(
	struct counter_registry *dst,
	struct counter_registry *src,
	yanet_error **err
);

struct counter_storage_page {
	uint64_t values[COUNTER_STORAGE_PAGE_SIZE / sizeof(uint64_t)];
};

struct counter_storage_block {
	uint64_t refcnt;
	struct counter_storage_page *pages;
};

struct counter_storage_pool {
	uint64_t block_count;
	struct counter_storage_block **blocks;
};

struct counter_value_handle;

struct counter_storage {
	struct memory_context *memory_context;
	struct counter_value_handle **counter_value_handles;
	struct counter_registry *registry;
	struct counter_storage_pool pools[COUNTER_POOL_SIZE];

	// Reference count for cross-generation sharing.
	//
	// When a config update does not change a module's counter layout, the
	// old generation's counter_storage is reused by bumping this count
	// instead of rebuilding the wrapper, handle array, and pool blocks.
	uint64_t refcnt;
};

struct counter_storage *
counter_storage_spawn(
	struct memory_context *memory_context,
	struct counter_storage *old_counter_storage,
	struct counter_registry *registry
);

void
counter_storage_free(struct counter_storage *storage);

static inline struct counter_value_handle *
counter_get_value_handle(
	uint64_t counter_id, struct counter_storage *counter_storage
) {
	struct counter_value_handle **handles =
		ADDR_OF_NONNULL(&counter_storage->counter_value_handles);
	return ADDR_OF_NONNULL(handles + counter_id);
}

static inline uint64_t *
counter_handle_get_value(struct counter_value_handle *value_handle) {
	return (uint64_t *)value_handle;
}

static inline uint64_t *
counter_get_address(uint64_t counter_id, struct counter_storage *storage) {
	struct counter_value_handle *value_handle =
		counter_get_value_handle(counter_id, storage);

#ifdef COUNTERS_CHECK
	if (value_handle == NULL) {
		return NULL;
	}
#endif

	return counter_handle_get_value(value_handle);
}

struct counter_handle;

// Copies a single counter's values into `accum`.
void
counter_handle_accum(
	uint64_t *accum,
	size_t counter_size,
	struct counter_value_handle *handle
);
