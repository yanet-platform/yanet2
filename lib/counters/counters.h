#pragma once

#include <stdint.h>

#include "common/memory.h"

#define COUNTER_MAX_SIZE_EXP 4
#define COUNTER_POOL_SIZE (COUNTER_MAX_SIZE_EXP + 1)
#define COUNTER_STORAGE_PAGE_SIZE 4096

#ifndef CP_MODULE_DATA_NAME_LEN
#define CP_MODULE_DATA_NAME_LEN 80
#define CP_PIPELINE_NAME_LEN 80
#endif

#define COUNTER_NAME_LEN 64
#define COUNTER_INVALID (uint64_t)-1

struct counter_name {
	char name[COUNTER_NAME_LEN];
	uint64_t size;
	uint64_t gen;
};

struct counter_link {
	uint64_t offset;
	uint64_t pool_idx;
};

struct counter_registry {
	struct memory_context *memory_context;
	uint64_t gen;
	uint64_t capacity;
	uint64_t count;
	uint64_t counts[COUNTER_POOL_SIZE];

	struct counter_name *names;
	struct counter_link *links;
};

int
counter_registry_init(
	struct counter_registry *registry,
	struct memory_context *memory_context,
	uint64_t gen
);

int
counter_registry_copy(
	struct counter_registry *registry, struct counter_registry *src
);

uint64_t
counter_registry_register(
	struct counter_registry *registry, const char *name, uint64_t size
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

struct counter_storage_allocator {
	struct memory_context *memory_context;
	uint64_t instance_count;
};

void
counter_storage_allocator_init(
	struct counter_storage_allocator *counter_storage_allocator,
	struct memory_context *memory_context,
	uint64_t instance_count
);

struct counter_storage {
	struct memory_context *memory_context;
	struct counter_registry *registry;
	struct counter_storage_allocator *allocator;
	struct counter_storage_pool pools[COUNTER_POOL_SIZE];
};

struct counter_storage *
counter_storage_spawn(
	struct memory_context *memory_context,
	struct counter_storage_allocator *allocator,
	struct counter_storage *old_counter_storage,
	struct counter_registry *registry
);

void
counter_storage_free(struct counter_storage *storage);

struct counter_value_handle;
static inline struct counter_value_handle *
counter_get_value_handle(
	struct counter_link *link, struct counter_storage *storage
) {

#ifdef COUNTERS_CHECK
	if (link->pool_idx >= COUNTER_POOL_SIZE)
		return NULL;
#endif

	struct counter_storage_pool *pool = storage->pools + link->pool_idx;
	uint64_t block_idx = link->offset / COUNTER_STORAGE_PAGE_SIZE;
	uint64_t offset = link->offset % COUNTER_STORAGE_PAGE_SIZE;

#ifdef COUNTERS_CHECK
	if (block_idx >= pool->block_count)
		return NULL;
#endif

	struct counter_storage_block *block =
		ADDR_OF(ADDR_OF(&pool->blocks) + block_idx);
	return (struct counter_value_handle *)(ADDR_OF(&block->pages)->values +
					       offset);
}

static inline uint64_t *
counter_handle_get_value(
	struct counter_value_handle *value_handle, uint64_t instance_id
) {
	return (uint64_t *)((uintptr_t)value_handle +
			    COUNTER_STORAGE_PAGE_SIZE * instance_id);
}

static inline uint64_t *
counter_get_address(
	struct counter_link *link,
	struct counter_storage *storage,
	uint64_t instance_id
) {

	struct counter_value_handle *value_handle =
		counter_get_value_handle(link, storage);

#ifdef COUNTERS_CHECK
	if (value_handle == NULL)
		return NULL;
	if (instance_id >= storage->instance_count)
		return NULL;
#endif

	return counter_handle_get_value(value_handle, instance_id);
}
