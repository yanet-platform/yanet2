#pragma once

#include <assert.h>
#include <stdint.h>

#include "common/memory.h"

#define COUNTER_MAX_SIZE_EXP 5
#define COUNTER_POOL_SIZE (COUNTER_MAX_SIZE_EXP + 1)
#define COUNTER_STORAGE_PAGE_SIZE 4096

#ifndef CP_MODULE_DATA_NAME_LEN
#define CP_MODULE_DATA_NAME_LEN 80
#define CP_PIPELINE_NAME_LEN 80
#endif

#define COUNTER_NAME_LEN 64
#define COUNTER_INVALID (uint64_t)-1

struct counter {
	char name[COUNTER_NAME_LEN];
	uint64_t size;
	uint64_t gen;
	uint64_t offset;
};

struct counter_registry {
	struct memory_context *memory_context;
	uint64_t gen;
	uint64_t capacity;
	uint64_t count;
	uint64_t counts[COUNTER_POOL_SIZE];

	struct counter *names;
};

int
counter_registry_init(
	struct counter_registry *registry,
	struct memory_context *memory_context,
	uint64_t gen
);

uint64_t
counter_registry_register(
	struct counter_registry *registry, const char *name, uint64_t size
);

void
counter_registry_free(struct counter_registry *registry);

int
counter_registry_link(
	struct counter_registry *dst, struct counter_registry *src
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

struct counter_value_handle;

struct counter_storage {
	struct memory_context *memory_context;
	struct counter_value_handle **counter_value_handles;
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

static inline struct counter_value_handle *
counter_get_value_handle(
	uint64_t counter_id, struct counter_storage *counter_storage
) {
	struct counter_value_handle **handles =
		ADDR_OF(&counter_storage->counter_value_handles);
	return ADDR_OF(handles + counter_id);
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
	uint64_t counter_id,
	uint64_t instance_id,
	struct counter_storage *storage
) {
	struct counter_value_handle *value_handle =
		counter_get_value_handle(counter_id, storage);

#ifdef COUNTERS_CHECK
	if (value_handle == NULL)
		return NULL;
	if (instance_id >= instances)
		return NULL;
#endif

	return counter_handle_get_value(value_handle, instance_id);
}

struct counter_handle;

// Accumulates `instances` counters into one `accum` counter.
void
counter_handle_accum(
	uint64_t *accum,
	size_t instances,
	size_t counter_size,
	struct counter_value_handle *handle
);
