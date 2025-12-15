#pragma once

#include "memory.h"
#include "memory_address.h"

/*
 * Remap table allows to remap one unsinged into another and intended to spare
 * unsigned value set size.
 *
 * From the start is filled with only one zero-value with known reference
 * count. One may touch any value (key) and get corresponding remapped (value)
 * with following attributes:
 *  - if the key was not touched while the current generation then one
 *    receives an unused value with reference count 1, reference count of the
 *    key is decremented
 *  - if the key was touched while current generation then one receives the
 *    value returned earlier while the generation. Also reference count of
 *    the value incremented and the key reference is decremented
 *  - returned value could be usaed as new key just from the return
 *  - any value (including zero) may be reused if it's reference count is
 *    zero
 *  - generation may be increased at any time
 */

#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>

#define REMAP_TABLE_CHUNK_SIZE 4096
#define REMAP_TABLE_INVALID 0xffffffff

/*
 * Remap table item contains:
 * - reference count
 * - last tocuh generation
 * - remap value valid if the item generation is equal to the current one
 */
struct remap_item {
	uint32_t count;
	uint32_t gen;
	uint32_t value;
	uint32_t pad;
};

/*
 * Remap table contains:
 *  - current generation
 *  - count of allocated key set
 *  - list of free items
 *  - remaps items organized into chunks
 */
struct remap_table {
	struct memory_context *memory_context;
	uint32_t gen;
	uint32_t count;
	uint32_t free_list;
	struct remap_item **keys;
};

static inline int
remap_table_init(
	struct remap_table *table,
	struct memory_context *memory_context,
	uint32_t capacity
) {
	table->memory_context = memory_context;
	table->gen = 1;
	table->count = 1;
	struct remap_item **keys = (struct remap_item **)memory_balloc(
		table->memory_context, sizeof(struct remap_item *)
	);

	if (keys == NULL)
		return -1;

	struct remap_item *chunk = (struct remap_item *)memory_balloc(
		table->memory_context,
		sizeof(struct remap_item) * REMAP_TABLE_CHUNK_SIZE
	);
	if (chunk == NULL) {
		memory_bfree(
			table->memory_context, keys, sizeof(struct remap_item *)
		);
		return -1;
	}

	chunk[0] = (struct remap_item){capacity, 0, 0, 0};

	SET_OFFSET_OF(&keys[0], chunk);
	SET_OFFSET_OF(&table->keys, keys);

	table->free_list = REMAP_TABLE_INVALID;
	return 0;
}

static inline void
remap_table_free(struct remap_table *table) {
	struct remap_item **keys = ADDR_OF(&table->keys);

	uint32_t chunk_count = (table->count + REMAP_TABLE_CHUNK_SIZE - 1) /
			       REMAP_TABLE_CHUNK_SIZE;

	for (uint32_t chunk_idx = 0; chunk_idx < chunk_count; ++chunk_idx) {
		struct remap_item *chunk = ADDR_OF(&keys[chunk_idx]);
		if (chunk != NULL) {
			memory_bfree(
				table->memory_context,
				chunk,
				sizeof(struct remap_item) *
					REMAP_TABLE_CHUNK_SIZE
			);
			SET_OFFSET_OF(keys + chunk_idx, NULL);
		}
	}
	memory_bfree(
		table->memory_context,
		keys,
		chunk_count * sizeof(struct remap_item *)
	);
	SET_OFFSET_OF(&table->keys, NULL);
}

static inline void
remap_table_new_gen(struct remap_table *table) {
	++(table->gen);
}

static inline struct remap_item *
remap_table_item(struct remap_table *table, uint32_t key) {
	struct remap_item **keys = ADDR_OF(&table->keys);
	struct remap_item *chunk = ADDR_OF(&keys[key / REMAP_TABLE_CHUNK_SIZE]);
	return chunk + key % REMAP_TABLE_CHUNK_SIZE;
}

/*
 * The routine return an unused key. If there are any free items then the
 * the first one is returned. In the opposite case the routine allocate new
 * chunk if required and returns first available key.
 */
static inline int
remap_table_new_key(struct remap_table *table, uint32_t *key) {
	/*		if (table->free_list != REMAP_TABLE_INVALID) {
				*key = table->free_list;
				struct remap_item *free_item =
	   remap_table_item(table, *key); table->free_list = free_item->value;

				*free_item = (struct remap_item){0, 0, 0, 0};
				return 0;
			}
	*/
	if (!(table->count % REMAP_TABLE_CHUNK_SIZE)) {
		struct remap_item *new_chunk =
			(struct remap_item *)memory_balloc(
				table->memory_context,
				sizeof(struct remap_item) *
					REMAP_TABLE_CHUNK_SIZE
			);

		if (new_chunk == NULL)
			return -1;

		uint32_t old_chunk_count =
			table->count / REMAP_TABLE_CHUNK_SIZE;
		uint32_t new_chunk_count = old_chunk_count + 1;

		struct remap_item **old_keys = ADDR_OF(&table->keys);

		struct remap_item **new_keys =
			(struct remap_item **)memory_balloc(
				table->memory_context,
				new_chunk_count * sizeof(struct remap_item *)
			);
		if (new_keys == NULL) {
			memory_bfree(
				table->memory_context,
				new_chunk,
				sizeof(struct remap_item) *
					REMAP_TABLE_CHUNK_SIZE
			);
			return -1;
		}

		for (uint64_t chunk_idx = 0; chunk_idx < old_chunk_count;
		     ++chunk_idx) {
			EQUATE_OFFSET(
				new_keys + chunk_idx, old_keys + chunk_idx
			);
		}

		SET_OFFSET_OF(&new_keys[new_chunk_count - 1], new_chunk);
		SET_OFFSET_OF(&table->keys, new_keys);

		memory_bfree(
			table->memory_context,
			old_keys,
			old_chunk_count * sizeof(struct remap_item *)
		);
	}

	struct remap_item *item = remap_table_item(table, table->count);
	*item = (struct remap_item){0, 0, 0, 0};
	*key = table->count++;
	return 0;
}

static inline int
remap_table_touch(struct remap_table *table, uint32_t key, uint32_t *value) {
	int res = 0;
	struct remap_item *item = remap_table_item(table, key);

	if (item->gen != table->gen) {
		// Allocate new key and update generation
		uint32_t new_key;
		if (remap_table_new_key(table, &new_key))
			return -1;
		item->gen = table->gen;
		item->value = new_key;
		res = 1;
	}

	struct remap_item *new_item = remap_table_item(table, item->value);
	new_item->value = item->value;
	new_item->gen = table->gen;
	// Update reference count
	new_item->count++;
	item->count--;
	*value = item->value;

	if (item->count == 0) {
		// Move zero-referenced value into free item chain
		item->value = table->free_list;
		table->free_list = key;
	}

	return res;
}

/*
 * Despite the fact of tracking zero-referenced keys and reusing them later
 * there could be cases when remap table contains gaps. So the routine rebuilds
 * the remap table to eliminate all gaps.
 *
 * NOTE: Touching keys is not legal after compaction.
 */
static inline void
remap_table_compact(struct remap_table *table) {
	uint32_t new_key = 0;

	for (uint32_t low_idx = 0; low_idx < table->count; ++low_idx) {
		struct remap_item *low_item = remap_table_item(table, low_idx);
		if (low_item->count) {
			low_item->value = new_key++;
		} else {
			low_item->value = REMAP_TABLE_INVALID;
		}
	}
}

/*
 * The routine returns compacted view of remap table.
 */
static inline uint32_t
remap_table_compacted(struct remap_table *table, uint32_t key) {
	struct remap_item *item = remap_table_item(table, key);
	return item->value;
}
