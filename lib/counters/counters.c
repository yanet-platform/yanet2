#include "counters.h"

#include "common/numutils.h"
#include "common/strutils.h"

#include "lib/controlplane/diag/diag.h"

int
counter_registry_init(
	struct counter_registry *registry,
	struct memory_context *memory_context,
	uint64_t gen
) {
	SET_OFFSET_OF(&registry->memory_context, memory_context);

	registry->count = 0;
	for (uint64_t idx = 0; idx < COUNTER_POOL_SIZE; ++idx)
		registry->counts[idx] = 0;

	registry->capacity = 0;
	registry->gen = gen;

	SET_OFFSET_OF(&registry->names, NULL);

	return 0;
}

void
counter_registry_free(struct counter_registry *registry) {
	struct memory_context *memory_context =
		ADDR_OF(&registry->memory_context);
	struct counter *names = ADDR_OF(&registry->names);
	if (names != NULL) {
		memory_bfree(
			memory_context,
			names,
			sizeof(struct counter) * registry->capacity
		);
	}
}

uint64_t
counter_registry_lookup_index(
	struct counter_registry *registry, const char *name, uint64_t size
) {
	struct counter *names = ADDR_OF(&registry->names);

	// FIXME: use hash index
	for (uint64_t idx = 0; idx < registry->count; ++idx) {
		if (!strncmp(name, names[idx].name, COUNTER_NAME_LEN) &&
		    names[idx].size == size) {
			return idx;
		}
	}

	return (uint64_t)-1;
}

static int
counter_registry_expand(
	struct counter_registry *registry, uint64_t new_capacity
) {
	uint64_t old_capacity = registry->capacity;
	if (new_capacity < registry->capacity) {
		NEW_ERROR(
			"requested capacity (%lu) is smaller than current "
			"capacity (%lu)",
			new_capacity,
			registry->capacity
		);
		return -1;
	}
	if (new_capacity == registry->capacity)
		return 0;

	struct memory_context *memory_context =
		ADDR_OF(&registry->memory_context);

	struct counter *new_names = (struct counter *)memory_balloc(
		memory_context, sizeof(struct counter) * new_capacity
	);
	if (new_names == NULL) {
		NEW_ERROR("failed to allocate counter names");
		return -1;
	}

	struct counter *names = ADDR_OF(&registry->names);

	/*
	 * FIXME: copying is not efficient here so names and links should be
	 * turned into chunked arrays.
	 */
	if (old_capacity > 0) {
		memcpy(new_names, names, sizeof(struct counter) * old_capacity);
	}

	SET_OFFSET_OF(&registry->names, new_names);
	registry->capacity = new_capacity;

	memory_bfree(
		memory_context, names, sizeof(struct counter) * old_capacity
	);

	return 0;
}

static uint64_t
counter_registry_insert(
	struct counter_registry *registry,
	const char *name,
	uint64_t size,
	uint64_t gen
) {
	if (!size)
		return -1;

	if (registry->count >= registry->capacity) {
		uint64_t new_capacity = registry->capacity * 2;
		if (new_capacity == 0)
			new_capacity = 8;
		if (counter_registry_expand(registry, new_capacity)) {
			PUSH_ERROR("failed to expand counter registry");
			return -1;
		}
	}

	struct counter *names = ADDR_OF(&registry->names);

	struct counter *new_name = names + registry->count;

	strtcpy(new_name->name, name, COUNTER_NAME_LEN);
	new_name->size = size;
	new_name->gen = gen;
	new_name->offset = (uint64_t)-1;

	return registry->count++;
}

uint64_t
counter_registry_register(
	struct counter_registry *registry, const char *name, uint64_t size
) {
	if (size == 0)
		return -1;
	if (size > (1 << COUNTER_MAX_SIZE_EXP))
		return -1;

	uint64_t idx = counter_registry_lookup_index(registry, name, size);

	if (idx != (uint64_t)-1) {
		struct counter *name = ADDR_OF(&registry->names) + idx;
		name->gen = registry->gen;

		return idx;
	}

	return counter_registry_insert(registry, name, size, registry->gen);
}

int
counter_registry_link(
	struct counter_registry *dst, struct counter_registry *src
) {
	if (src != NULL) {
		for (uint64_t pool_idx = 0; pool_idx < COUNTER_POOL_SIZE;
		     ++pool_idx) {
			dst->counts[pool_idx] = src->counts[pool_idx];
		}

		for (uint64_t src_idx = 0; src_idx < src->count; ++src_idx) {
			struct counter *src_name =
				ADDR_OF(&src->names) + src_idx;

			// Skip outdated counters
			if (src_name->gen != src->gen)
				continue;

			uint64_t dst_idx = counter_registry_lookup_index(
				dst, src_name->name, src_name->size
			);
			if (dst_idx == (uint64_t)-1) {
				dst_idx = counter_registry_insert(
					dst,
					src_name->name,
					src_name->size,
					src_name->gen
				);
			}
			if (dst_idx == (uint64_t)-1) {
				return -1;
			}

			struct counter *dst_name =
				ADDR_OF(&dst->names) + dst_idx;
			dst_name->offset = src_name->offset;
		}
	}
	for (uint64_t dst_idx = 0; dst_idx < dst->count; ++dst_idx) {
		struct counter *dst_name = ADDR_OF(&dst->names) + dst_idx;

		if (dst_name->offset != (uint64_t)-1) {
			continue;
		}

		uint64_t pool_idx = uint64_log_up(dst_name->size);
		// FIXME reuse old links (with clearance)
		dst_name->offset = dst->counts[pool_idx]++ * (8 << pool_idx);
	}

	return 0;
}

void
counter_storage_allocator_init(
	struct counter_storage_allocator *counter_storage_allocator,
	struct memory_context *memory_context,
	uint64_t instance_count
) {
	SET_OFFSET_OF(
		&counter_storage_allocator->memory_context, memory_context
	);
	counter_storage_allocator->instance_count = instance_count;
}

static struct counter_storage_page *
counter_storage_allocator_new_pages(struct counter_storage_allocator *allocator
) {
	struct counter_storage_page *pages =
		(struct counter_storage_page *)memory_balloc(
			ADDR_OF(&allocator->memory_context),
			sizeof(struct counter_storage_page) *
				allocator->instance_count
		);
	if (pages == NULL)
		return NULL;
	memset(pages,
	       0,
	       sizeof(struct counter_storage_page) * allocator->instance_count);
	return pages;
}

static void
counter_storage_allocator_free_pages(
	struct counter_storage_allocator *allocator,
	struct counter_storage_page *pages
) {
	memory_bfree(
		ADDR_OF(&allocator->memory_context),
		pages,
		sizeof(struct counter_storage_page) * allocator->instance_count
	);
}

static void
counter_storage_init(
	struct memory_context *memory_context,
	struct counter_storage *storage,
	struct counter_storage_allocator *allocator,
	struct counter_registry *registry
) {
	SET_OFFSET_OF(&storage->memory_context, memory_context);
	SET_OFFSET_OF(&storage->allocator, allocator);
	SET_OFFSET_OF(&storage->registry, registry);
	memset(storage->pools,
	       0,
	       sizeof(struct counter_storage_pool) * COUNTER_POOL_SIZE);
}

struct counter_storage *
counter_storage_spawn(
	struct memory_context *memory_context,
	struct counter_storage_allocator *allocator,
	struct counter_storage *old_counter_storage,
	struct counter_registry *counter_registry
) {
	if (old_counter_storage != NULL &&
	    ADDR_OF(&old_counter_storage->allocator) != allocator)
		return NULL;

	struct counter_storage *new_counter_storage = (struct counter_storage *)
		memory_balloc(memory_context, sizeof(struct counter_storage));
	if (new_counter_storage == NULL)
		return NULL;

	counter_storage_init(
		memory_context, new_counter_storage, allocator, counter_registry
	);

	for (uint64_t pool_idx = 0; pool_idx < COUNTER_POOL_SIZE; ++pool_idx) {

		uint64_t registry_size =
			counter_registry->counts[pool_idx] * (8 << pool_idx);
		uint64_t block_count =
			(registry_size + COUNTER_STORAGE_PAGE_SIZE - 1) /
			COUNTER_STORAGE_PAGE_SIZE;

		if (old_counter_storage != NULL) {
			struct counter_storage_pool *old_pool =
				old_counter_storage->pools + pool_idx;

			if (old_pool->block_count > block_count) {
				block_count = old_pool->block_count;
			}
		}

		struct counter_storage_pool *new_pool =
			new_counter_storage->pools + pool_idx;
		struct counter_storage_block **new_blocks = memory_balloc(
			memory_context,
			block_count * sizeof(struct counter_storage_block *)
		);
		if (new_blocks == NULL && block_count > 0) {
			goto error;
		}

		if (block_count) {
			memset(new_blocks,
			       0,
			       block_count *
				       sizeof(struct counter_storage_block *));
		}

		SET_OFFSET_OF(&new_pool->blocks, new_blocks);
		new_pool->block_count = block_count;

		uint64_t idx = 0;
		if (old_counter_storage != NULL) {
			struct counter_storage_pool *old_pool =
				old_counter_storage->pools + pool_idx;

			while (idx < old_pool->block_count) {
				struct counter_storage_block *block =
					ADDR_OF(ADDR_OF(&old_pool->blocks) + idx
					);
				block->refcnt += 1;
				SET_OFFSET_OF(new_blocks + idx, block);

				++idx;
			}
		}

		while (idx < block_count) {
			struct counter_storage_block *block =
				(struct counter_storage_block *)memory_balloc(
					memory_context,
					sizeof(struct counter_storage_block)
				);
			block->refcnt = 1;
			struct counter_storage_page *pages =
				counter_storage_allocator_new_pages(allocator);
			if (pages == NULL) {
				goto error;
			}
			SET_OFFSET_OF(&block->pages, pages);

			SET_OFFSET_OF(new_blocks + idx, block);

			++idx;
		}
	}

	struct counter_value_handle **counter_value_handles =
		(struct counter_value_handle **)memory_balloc(
			memory_context,
			sizeof(struct counter_value_handle *) *
				counter_registry->count
		);
	if (counter_value_handles == NULL && counter_registry->count > 0) {
		goto error;
	}

	for (uint64_t idx = 0; idx < counter_registry->count; ++idx) {
		struct counter *name = ADDR_OF(&counter_registry->names) + idx;

		uint64_t pool_idx = uint64_log_up(name->size);

		struct counter_storage_pool *pool =
			new_counter_storage->pools + pool_idx;

		uint64_t block_idx = name->offset / COUNTER_STORAGE_PAGE_SIZE;
		uint64_t offset = name->offset % COUNTER_STORAGE_PAGE_SIZE;

		struct counter_storage_block *block =
			ADDR_OF(ADDR_OF(&pool->blocks) + block_idx);

		uint8_t *base = (uint8_t *)ADDR_OF(&block->pages);

		SET_OFFSET_OF(
			counter_value_handles + idx,
			(struct counter_value_handle *)(base + offset)
		);
	}

	SET_OFFSET_OF(
		&new_counter_storage->counter_value_handles,
		counter_value_handles
	);

	return new_counter_storage;

error:
	counter_storage_free(new_counter_storage);
	return NULL;
}

static void
counter_storage_pool_destroy(
	struct counter_storage *storage, struct counter_storage_pool *pool
) {
	if (ADDR_OF(&pool->blocks) == NULL)
		return;

	struct memory_context *memory_context =
		ADDR_OF(&storage->memory_context);
	for (uint64_t idx = 0; idx < pool->block_count; ++idx) {
		struct counter_storage_block *block =
			ADDR_OF(ADDR_OF(&pool->blocks) + idx);
		if (block == NULL)
			continue;

		if (--block->refcnt == 0) {
			counter_storage_allocator_free_pages(
				ADDR_OF(&storage->allocator),
				ADDR_OF(&block->pages)
			);
			memory_bfree(
				memory_context,
				block,
				sizeof(struct counter_storage_block)
			);
		}
	}

	memory_bfree(
		memory_context,
		ADDR_OF(&pool->blocks),
		sizeof(struct counter_storage_block *) * pool->block_count
	);
}

void
counter_storage_free(struct counter_storage *storage) {
	struct memory_context *memory_context =
		ADDR_OF(&storage->memory_context);

	if (ADDR_OF(&storage->counter_value_handles) != NULL) {
		struct counter_registry *counter_registry =
			ADDR_OF(&storage->registry);
		memory_bfree(
			memory_context,
			ADDR_OF(&storage->counter_value_handles),
			sizeof(struct counter_structure_handle *) *
				counter_registry->count
		);
	}

	for (uint64_t pool_idx = 0; pool_idx < COUNTER_POOL_SIZE; ++pool_idx) {
		struct counter_storage_pool *pool = storage->pools + pool_idx;
		counter_storage_pool_destroy(storage, pool);
	}
}

void
counter_handle_accum(
	uint64_t *accum,
	size_t instances,
	size_t counter_size,
	struct counter_value_handle *handle
) {
	// counter_size is the number of uint64_t elements, not bytes
	memset(accum, 0, counter_size * sizeof(uint64_t));
	for (size_t instance_idx = 0; instance_idx < instances;
	     ++instance_idx) {
		uint64_t *value =
			counter_handle_get_value(handle, instance_idx);
		for (size_t idx = 0; idx < counter_size; ++idx) {
			accum[idx] += value[idx];
		}
	}
}
