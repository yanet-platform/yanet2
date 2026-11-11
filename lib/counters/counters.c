#include "counters.h"

#include "common/numutils.h"
#include "common/strutils.h"

#include "lib/errors/errors.h"

int
counter_registry_init(
	struct counter_registry *registry, struct memory_context *memory_context
) {
	SET_OFFSET_OF(&registry->memory_context, memory_context);

	registry->count = 0;
	for (uint64_t idx = 0; idx < COUNTER_POOL_SIZE; ++idx) {
		registry->counts[idx] = 0;
		SET_OFFSET_OF(&registry->free_slots[idx], NULL);
		registry->free_counts[idx] = 0;
		registry->free_capacities[idx] = 0;
	}

	registry->capacity = 0;

	SET_OFFSET_OF(&registry->names, NULL);

	if (str_index_init(&registry->str_index, memory_context)) {
		return -1;
	}

	return 0;
}

void
counter_registry_fini(struct counter_registry *registry) {
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

	for (uint64_t pool_idx = 0; pool_idx < COUNTER_POOL_SIZE; ++pool_idx) {
		uint64_t *free_slots = ADDR_OF(&registry->free_slots[pool_idx]);
		if (free_slots != NULL) {
			memory_bfree(
				memory_context,
				free_slots,
				sizeof(uint64_t) *
					registry->free_capacities[pool_idx]
			);
		}
	}

	str_index_fini(&registry->str_index);

	// Reset to zero-init state so a second fini is a safe no-op.
	memset(registry, 0, sizeof(*registry));
}

static inline const char *
counter_registry_read_index(uint32_t index, const void *data) {
	const struct counter_registry *registry =
		(struct counter_registry *)data;

	struct counter *names = ADDR_OF(&registry->names);
	return names[index].name;
}

uint64_t
counter_registry_lookup_index(
	struct counter_registry *registry, const char *name
) {
	uint32_t index = str_index_lookup(
		&registry->str_index,
		name,
		COUNTER_NAME_LEN,
		counter_registry_read_index,
		registry
	);

	if (index != STR_INDEX_INVALID) {
		return index;
	}

	return (uint64_t)-1;
}

static int
counter_registry_expand(
	struct counter_registry *registry,
	uint64_t new_capacity,
	yanet_error **err
) {
	uint64_t old_capacity = registry->capacity;
	if (new_capacity < registry->capacity) {
		yanet_error_add(
			err,
			"requested capacity (%lu) is smaller than current "
			"capacity (%lu)",
			new_capacity,
			registry->capacity
		);
		return -1;
	}
	if (new_capacity == registry->capacity) {
		return 0;
	}

	struct memory_context *memory_context =
		ADDR_OF(&registry->memory_context);

	struct counter *new_names = (struct counter *)memory_balloc(
		memory_context, sizeof(struct counter) * new_capacity
	);
	if (new_names == NULL) {
		yanet_error_add(err, "failed to allocate counter names");
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
	uint32_t age,
	yanet_error **err
) {
	if (!size) {
		return -1;
	}

	if (registry->count >= registry->capacity) {
		uint64_t new_capacity = registry->capacity * 2;
		if (new_capacity == 0) {
			new_capacity = 8;
		}
		if (counter_registry_expand(registry, new_capacity, err)) {
			yanet_error_add(
				err, "failed to expand counter registry"
			);
			return -1;
		}
	}

	if (str_index_insert(
		    &registry->str_index,
		    name,
		    COUNTER_NAME_LEN,
		    registry->count,
		    counter_registry_read_index,
		    registry
	    )) {
		yanet_error_add(err, "failed to insert counter registry index");
		return -1;
	}

	struct counter *names = ADDR_OF(&registry->names);

	struct counter *new_name = names + registry->count;

	strtcpy(new_name->name, name, COUNTER_NAME_LEN);
	new_name->size = size;
	new_name->age = age;
	new_name->offset = (uint64_t)-1;
	new_name->flags = 0;

	return registry->count++;
}

uint64_t
counter_registry_register(
	struct counter_registry *registry,
	const char *name,
	uint64_t size,
	yanet_error **err
) {
	if (size == 0) {
		yanet_error_add(err, "zero size");
		return -1;
	}
	if (size > (1 << COUNTER_MAX_SIZE_EXP)) {
		yanet_error_add(
			err,
			"counter size %lu exceeds max %d",
			size,
			(1 << COUNTER_MAX_SIZE_EXP)
		);
		return -1;
	}
	if (strnlen(name, COUNTER_NAME_LEN) == COUNTER_NAME_LEN) {
		yanet_error_add(
			err, "name length exceeds max %d", COUNTER_NAME_LEN - 1
		);
		return -1;
	}

	uint64_t idx = counter_registry_lookup_index(registry, name);

	if (idx != (uint64_t)-1) {
		struct counter *name = ADDR_OF(&registry->names) + idx;
		// A configuration that registers a carried name again counts
		// into it, so its age restarts while its slot and values stay.
		name->age = 0;
		if (name->size != size) {
			yanet_error_add(
				err, "counter redeclared with different size"
			);
			return -1;
		}

		return idx;
	}

	return counter_registry_insert(registry, name, size, 0, err);
}

// Remember a released value slot for a later name of the same pool.
static int
counter_registry_free_slot_push(
	struct counter_registry *registry,
	uint64_t pool_idx,
	uint64_t offset,
	yanet_error **err
) {
	if (registry->free_counts[pool_idx] ==
	    registry->free_capacities[pool_idx]) {
		uint64_t new_capacity = registry->free_capacities[pool_idx] * 2;
		if (new_capacity == 0) {
			new_capacity = 8;
		}

		struct memory_context *memory_context =
			ADDR_OF(&registry->memory_context);
		uint64_t *new_slots = (uint64_t *)memory_balloc(
			memory_context, sizeof(uint64_t) * new_capacity
		);
		if (new_slots == NULL) {
			yanet_error_add(
				err, "failed to allocate free counter slots"
			);
			return -1;
		}

		uint64_t *free_slots = ADDR_OF(&registry->free_slots[pool_idx]);
		if (free_slots != NULL) {
			memcpy(new_slots,
			       free_slots,
			       sizeof(uint64_t) *
				       registry->free_counts[pool_idx]);
			memory_bfree(
				memory_context,
				free_slots,
				sizeof(uint64_t) *
					registry->free_capacities[pool_idx]
			);
		}

		SET_OFFSET_OF(&registry->free_slots[pool_idx], new_slots);
		registry->free_capacities[pool_idx] = new_capacity;
	}

	uint64_t *free_slots = ADDR_OF(&registry->free_slots[pool_idx]);
	free_slots[registry->free_counts[pool_idx]++] = offset;
	return 0;
}

int
counter_registry_link(
	struct counter_registry *dst,
	struct counter_registry *src,
	yanet_error **err
) {
	if (src == dst) {
		// Re-upserting the identical configuration passes the same
		// registry as its own predecessor: nothing carries over, ages
		// or takes a slot, and aging it would release slots its own
		// configuration still counts into.
		return 0;
	}

	if (src != NULL) {
		for (uint64_t pool_idx = 0; pool_idx < COUNTER_POOL_SIZE;
		     ++pool_idx) {
			dst->counts[pool_idx] = src->counts[pool_idx];

			// A registry that was already linked once holds free
			// slots of its own; dropping them keeps a second link
			// from handing one slot to two names.
			dst->free_counts[pool_idx] = 0;

			// Slots the replaced registry never reused stay free.
			uint64_t *src_free_slots =
				ADDR_OF(&src->free_slots[pool_idx]);
			for (uint64_t free_idx = 0;
			     free_idx < src->free_counts[pool_idx];
			     ++free_idx) {
				if (counter_registry_free_slot_push(
					    dst,
					    pool_idx,
					    src_free_slots[free_idx],
					    err
				    )) {
					return -1;
				}
			}
		}

		for (uint64_t src_idx = 0; src_idx < src->count; ++src_idx) {
			struct counter *src_name =
				ADDR_OF(&src->names) + src_idx;

			uint64_t dst_idx = counter_registry_lookup_index(
				dst, src_name->name
			);
			if (dst_idx != (uint64_t)-1) {
				struct counter *dst_name =
					ADDR_OF(&dst->names) + dst_idx;
				if (dst_name->size != src_name->size) {
					yanet_error_add(
						err,
						"counter redeclared with "
						"different size"
					);
					return -1;
				}
				dst_name->offset = src_name->offset;
				continue;
			}

			// A name whose carrying age ran out leaves the
			// registry; the generation that last counted into it
			// has been installed and replaced, so its slot may
			// serve a new name that starts from zero.
			if (src_name->age >= COUNTER_MAX_AGE) {
				if (counter_registry_free_slot_push(
					    dst,
					    uint64_log_up(src_name->size),
					    src_name->offset,
					    err
				    )) {
					return -1;
				}
				continue;
			}

			dst_idx = counter_registry_insert(
				dst,
				src_name->name,
				src_name->size,
				src_name->age + 1,
				err
			);
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
		if (dst->free_counts[pool_idx] > 0) {
			uint64_t *free_slots =
				ADDR_OF(&dst->free_slots[pool_idx]);
			dst_name->offset =
				free_slots[--dst->free_counts[pool_idx]];
			dst_name->flags |= COUNTER_FLAG_CLEAR_SLOT;
		} else {
			dst_name->offset =
				dst->counts[pool_idx]++ * (8 << pool_idx);
		}
	}

	return 0;
}

// Allocate one zeroed counter storage page from the given memory context.
//
// Each block now backs a single instance's counters; the per-instance page
// striding was removed together with the counter storage allocator.
static struct counter_storage_page *
counter_storage_new_page(struct memory_context *memory_context) {
	struct counter_storage_page *page =
		(struct counter_storage_page *)memory_balloc(
			memory_context, sizeof(struct counter_storage_page)
		);
	if (page == NULL) {
		return NULL;
	}
	memset(page, 0, sizeof(struct counter_storage_page));
	return page;
}

static void
counter_storage_init(
	struct memory_context *memory_context,
	struct counter_storage *storage,
	struct counter_registry *registry
) {
	memset(storage, 0, sizeof(*storage));
	SET_OFFSET_OF(&storage->memory_context, memory_context);
	SET_OFFSET_OF(&storage->registry, registry);
}

struct counter_storage *
counter_storage_spawn(
	struct memory_context *memory_context,
	struct counter_storage *old_counter_storage,
	struct counter_registry *counter_registry
) {
	// Fast path: if the old generation's storage was built for the same
	// counter_registry (same pointer — the registry item was shared by
	// refcounting across generations), the counter layout is identical.
	// Bump the refcount and return the old storage directly, skipping the
	// wrapper rebuild, pool copy, and handle resolution.
	if (old_counter_storage != NULL &&
	    ADDR_OF(&old_counter_storage->registry) == counter_registry) {
		// Gains only ever happen while building under the
		// configuration lock, but drops run from a retired
		// generation's unlocked teardown, so the shared count moves
		// with atomics.
		__atomic_add_fetch(
			&old_counter_storage->refcnt, 1, __ATOMIC_RELAXED
		);
		return old_counter_storage;
	}

	struct counter_storage *new_counter_storage = (struct counter_storage *)
		memory_balloc(memory_context, sizeof(struct counter_storage));
	if (new_counter_storage == NULL) {
		return NULL;
	}

	counter_storage_init(
		memory_context, new_counter_storage, counter_registry
	);

	new_counter_storage->refcnt = 1;

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
				__atomic_fetch_add(
					&block->refcnt, 1, __ATOMIC_RELAXED
				);
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
			if (block == NULL) {
				goto error;
			}
			memset(block, 0, sizeof(*block));
			block->refcnt = 1;
			SET_OFFSET_OF(new_blocks + idx, block);

			struct counter_storage_page *pages =
				counter_storage_new_page(memory_context);
			if (pages == NULL) {
				goto error;
			}
			SET_OFFSET_OF(&block->pages, pages);

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

		if (name->flags & COUNTER_FLAG_CLEAR_SLOT) {
			// The slot last served a name that left the registry a
			// replacement ago, so no worker of a published
			// generation writes it; the new name starts from zero.
			memset(base + offset, 0, name->size * sizeof(uint64_t));
		}

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
counter_storage_pool_fini(
	struct counter_storage *storage, struct counter_storage_pool *pool
) {
	if (ADDR_OF(&pool->blocks) == NULL) {
		return;
	}

	struct memory_context *memory_context =
		ADDR_OF(&storage->memory_context);
	for (uint64_t idx = 0; idx < pool->block_count; ++idx) {
		struct counter_storage_block *block =
			ADDR_OF(ADDR_OF(&pool->blocks) + idx);
		if (block == NULL) {
			continue;
		}

		// The release that reaches zero is the sole owner of the
		// pages: every earlier sharer's reads order before its own
		// drop by the acquire-release exchange, and new shares only
		// appear under the configuration lock.
		if (__atomic_sub_fetch(&block->refcnt, 1, __ATOMIC_ACQ_REL) ==
		    0) {
			memory_bfree(
				memory_context,
				ADDR_OF(&block->pages),
				sizeof(struct counter_storage_page)
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
	if (storage == NULL) {
		return;
	}

	// The sole zero-transition owns the teardown, ordered against all
	// earlier sharers by the acquire-release exchange; racing builds
	// only add references under the configuration lock.
	if (__atomic_sub_fetch(&storage->refcnt, 1, __ATOMIC_ACQ_REL) > 0) {
		return;
	}

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
		counter_storage_pool_fini(storage, pool);
	}

	memory_bfree(memory_context, storage, sizeof(struct counter_storage));
}
