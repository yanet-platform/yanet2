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

#define COUNTER_NAME_LEN 128
#define COUNTER_INVALID (uint64_t)-1

/*
 * A name stays in a registry for this many replacements after a
 * configuration stopped registering it. The carried name holds its value
 * slot reserved, because workers of the replaced configuration keep
 * counting into the slot until the replacement is installed; every update
 * installs and waits for all workers before the next one can start, so
 * once the name leaves the registry no worker of any published generation
 * writes its slot again.
 */
#define COUNTER_MAX_AGE 1

struct counter {
	char name[COUNTER_NAME_LEN];
	uint64_t size;
	uint64_t offset;
	// Replacements since a configuration last registered this name; zero
	// while the registry's own configuration counts into it.
	uint32_t age;
	uint32_t flags;
};

// The slot was taken from a registry's free list at the last link and
// still holds the values of the name that left; a storage spawned for the
// registry zeroes it before any worker can count into it.
#define COUNTER_FLAG_CLEAR_SLOT 1

struct counter_registry {
	struct memory_context *memory_context;
	uint64_t capacity;
	uint64_t count;
	uint64_t counts[COUNTER_POOL_SIZE];

	// Free value slots per pool, as byte offsets, in reverse order of
	// their release. A name that needs a slot takes the latest released
	// one before the pool grows; the watermark never shrinks, so storage
	// stays at its historical peak.
	uint64_t *free_slots[COUNTER_POOL_SIZE];
	uint64_t free_counts[COUNTER_POOL_SIZE];
	uint64_t free_capacities[COUNTER_POOL_SIZE];

	struct counter *names;
	struct str_index str_index;
};

int
counter_registry_init(
	struct counter_registry *registry, struct memory_context *memory_context
);

uint64_t
counter_registry_register(
	struct counter_registry *registry,
	const char *name,
	uint64_t size,
	yanet_error **err
);

// Resolve a counter's registry index by name, or (uint64_t)-1 when the
// registry carries no counter of that name.
uint64_t
counter_registry_lookup_index(
	struct counter_registry *registry, const char *name
);

void
counter_registry_fini(struct counter_registry *registry);

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
