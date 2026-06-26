#pragma once

/*
 * Rectangular value table allowing one to touch each key pair using
 * remap table.
 */

#include <stdint.h>

#include "memory.h"
#include "remap.h"

#define VALUE_TABLE_CHUNK_SIZE 16384

struct value_table {
	struct memory_context *memory_context;
	uint32_t v_dim;
	uint32_t h_dim;
	uint32_t **values;
};

static inline void
value_table_free(struct value_table *value_table) {
	struct memory_context *memory_context =
		ADDR_OF(&value_table->memory_context);

	uint64_t value_count = value_table->v_dim;
	value_count *= value_table->h_dim;
	uint32_t chunk_count = (value_count + VALUE_TABLE_CHUNK_SIZE - 1) /
			       VALUE_TABLE_CHUNK_SIZE;

	uint32_t **values = ADDR_OF(&value_table->values);
	for (uint32_t chunk_idx = 0; chunk_idx < chunk_count; ++chunk_idx) {
		uint32_t *chunk = ADDR_OF(values + chunk_idx);
		if (chunk == NULL)
			continue;
		memory_bfree(
			memory_context,
			chunk,
			VALUE_TABLE_CHUNK_SIZE * sizeof(uint32_t)
		);
		SET_OFFSET_OF(values + chunk_idx, NULL);
	}
	memory_bfree(memory_context, values, chunk_count * sizeof(uint32_t *));
	SET_OFFSET_OF(&value_table->values, NULL);
}

static inline int
value_table_init(
	struct value_table *value_table,
	struct memory_context *memory_context,
	uint32_t v_dim,
	uint32_t h_dim
) {
	SET_OFFSET_OF(&value_table->memory_context, memory_context);

	value_table->v_dim = v_dim;
	value_table->h_dim = h_dim;

	uint64_t value_count = v_dim;
	value_count *= h_dim;

	uint32_t chunk_count = (value_count + VALUE_TABLE_CHUNK_SIZE - 1) /
			       VALUE_TABLE_CHUNK_SIZE;

	uint32_t **values = (uint32_t **)memory_balloc(
		memory_context, chunk_count * sizeof(uint32_t *)
	);
	if (values == NULL)
		return -1;

	memset(values, 0, chunk_count * sizeof(uint32_t *));
	SET_OFFSET_OF(&value_table->values, values);

	for (uint32_t chunk_idx = 0; chunk_idx < chunk_count; ++chunk_idx) {
		uint32_t *chunk = (uint32_t *)memory_balloc(
			memory_context,
			VALUE_TABLE_CHUNK_SIZE * sizeof(uint32_t)
		);
		if (chunk == NULL) {
			value_table_free(value_table);
			return -1;
		}
		memset(chunk, 0, VALUE_TABLE_CHUNK_SIZE * sizeof(uint32_t));
		SET_OFFSET_OF(values + chunk_idx, chunk);
	}

	return 0;
}

static inline uint32_t *
value_table_get_ptr(
	struct value_table *value_table, uint32_t v_idx, uint32_t h_idx
) {
	// values and the chunk pointers are set at init and cleared only by
	// value_table_free, which never races a lookup — so on the query path
	// they are never NULL and the NULL test in ADDR_OF is pure per-lookup
	// overhead.
	uint32_t **values = ADDR_OF_NONNULL(&value_table->values);
	uint64_t idx = v_idx;
	idx *= value_table->h_dim;
	idx += h_idx;

	return ADDR_OF_NONNULL(values + idx / VALUE_TABLE_CHUNK_SIZE) +
	       idx % VALUE_TABLE_CHUNK_SIZE;
}

static inline uint32_t
value_table_get(
	struct value_table *value_table, uint32_t v_idx, uint32_t h_idx
) {
	return *value_table_get_ptr(value_table, v_idx, h_idx);
}

static inline void
value_table_compact(
	struct value_table *value_table, struct remap_table *remap_table
) {
	for (uint32_t v_idx = 0; v_idx < value_table->v_dim; ++v_idx) {
		for (uint32_t h_idx = 0; h_idx < value_table->h_dim; ++h_idx) {
			uint32_t *value =
				value_table_get_ptr(value_table, v_idx, h_idx);

			*value = remap_table_compacted(remap_table, *value);
		}
	}
}
