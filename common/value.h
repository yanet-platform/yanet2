#pragma once

/*
 * Rectangular value table allowing one to touch each key pair using
 * remap table.
 */

#include <stdint.h>

#include "memory.h"
#include "memory_block.h"
#include "remap.h"

#define VALUE_TABLE_CHUNK_SIZE 16384

struct value_table {
	struct memory_context *memory_context;
	uint32_t v_dim;
	uint32_t h_dim;
	// Nonzero when the table lives in one contiguous allocation.
	//
	// In flat mode values is a relative pointer straight to the uint32_t
	// data block, so a lookup costs a single dependent load. In chunked
	// mode values points to a directory of fixed-size chunks.
	uint32_t flat;
	uint32_t **values;
};

static inline void
value_table_free(struct value_table *value_table) {
	struct memory_context *memory_context =
		ADDR_OF(&value_table->memory_context);

	uint64_t value_count = value_table->v_dim;
	value_count *= value_table->h_dim;

	if (value_table->flat) {
		uint32_t *flat_values =
			(uint32_t *)ADDR_OF(&value_table->values);
		memory_bfree(
			memory_context,
			flat_values,
			value_count * sizeof(uint32_t)
		);
		SET_OFFSET_OF(&value_table->values, NULL);
		return;
	}

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
	value_table->flat = 0;

	uint64_t value_count = v_dim;
	value_count *= h_dim;

	// Prefer one contiguous allocation: a flat table spends one dependent
	// load per lookup instead of two. Compare in value units so the byte
	// count cannot overflow.
	if (value_count > 0 &&
	    value_count <= MEMORY_BLOCK_ALLOCATOR_MAX_SIZE / sizeof(uint32_t)) {
		uint32_t *flat_values = (uint32_t *)memory_balloc(
			memory_context, value_count * sizeof(uint32_t)
		);
		if (flat_values != NULL) {
			memset(flat_values, 0, value_count * sizeof(uint32_t));
			SET_OFFSET_OF(
				&value_table->values, (uint32_t **)flat_values
			);
			value_table->flat = 1;
			return 0;
		}
	}

	// The table is too large for a single allocator block or the flat
	// allocation failed: fall back to the chunked directory layout.
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
	uint64_t idx = v_idx;
	idx *= value_table->h_dim;
	idx += h_idx;

	// values (flat base or chunk directory) and the chunk pointers are set
	// at init and cleared only by value_table_free, which never races a
	// lookup — so on the query path they are never NULL and the NULL test
	// in ADDR_OF is pure per-lookup overhead.
	if (value_table->flat) {
		return (uint32_t *)ADDR_OF_NONNULL(&value_table->values) + idx;
	}

	uint32_t **values = ADDR_OF_NONNULL(&value_table->values);
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
