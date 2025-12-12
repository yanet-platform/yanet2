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
	struct remap_table remap_table;
	uint32_t h_dim;
	uint32_t v_dim;
	uint32_t **values;
};

static inline int
value_table_init(
	struct value_table *value_table,
	struct memory_context *memory_context,
	uint32_t h_dim,
	uint32_t v_dim
) {
	SET_OFFSET_OF(&value_table->memory_context, memory_context);

	if (remap_table_init(
		    &value_table->remap_table, memory_context, h_dim * v_dim
	    )) {
		return -1;
	}

	uint32_t chunk_count = (h_dim * v_dim + VALUE_TABLE_CHUNK_SIZE - 1) /
			       VALUE_TABLE_CHUNK_SIZE;

	uint32_t **values = (uint32_t **)memory_balloc(
		memory_context, chunk_count * sizeof(uint32_t *)
	);

	if (values == NULL) {
		remap_table_free(&value_table->remap_table);
		return -1;
	}

	for (uint32_t chunk_idx = 0; chunk_idx < chunk_count; ++chunk_idx) {
		uint32_t *chunk = (uint32_t *)memory_balloc(
			memory_context,
			VALUE_TABLE_CHUNK_SIZE * sizeof(uint32_t)
		);
		// FIXME check result
		memset(chunk, 0, VALUE_TABLE_CHUNK_SIZE * sizeof(uint32_t));
		SET_OFFSET_OF(values + chunk_idx, chunk);
	}

	SET_OFFSET_OF(&value_table->values, values);

	value_table->h_dim = h_dim;
	value_table->v_dim = v_dim;

	return 0;
}

static inline void
value_table_free(struct value_table *value_table) {
	remap_table_free(&value_table->remap_table);
	struct memory_context *memory_context =
		ADDR_OF(&value_table->memory_context);

	uint32_t chunk_count = (value_table->h_dim * value_table->v_dim +
				VALUE_TABLE_CHUNK_SIZE - 1) /
			       VALUE_TABLE_CHUNK_SIZE;

	uint32_t **values = ADDR_OF(&value_table->values);
	for (uint32_t chunk_idx = 0; chunk_idx < chunk_count; ++chunk_idx) {
		uint32_t *chunk = ADDR_OF(values + chunk_idx);
		memory_bfree(
			memory_context,
			chunk,
			VALUE_TABLE_CHUNK_SIZE * sizeof(uint32_t)
		);
	}
	memory_bfree(memory_context, values, chunk_count * sizeof(uint32_t *));
}

static inline void
value_table_new_gen(struct value_table *value_table) {
	remap_table_new_gen(&value_table->remap_table);
}

static inline uint32_t *
value_table_get_ptr(
	struct value_table *value_table, uint32_t h_idx, uint32_t v_idx
) {
	uint32_t **values = ADDR_OF(&value_table->values);
	uint64_t idx = (v_idx * value_table->h_dim) + h_idx;

	return ADDR_OF(values + idx / VALUE_TABLE_CHUNK_SIZE) +
	       idx % VALUE_TABLE_CHUNK_SIZE;
}

static inline uint32_t
value_table_get(
	struct value_table *value_table, uint32_t h_idx, uint32_t v_idx
) {
	uint32_t **values = ADDR_OF(&value_table->values);
	uint64_t idx = (v_idx * value_table->h_dim) + h_idx;

	return ADDR_OF(
		values + idx / VALUE_TABLE_CHUNK_SIZE
	)[idx % VALUE_TABLE_CHUNK_SIZE];
}

static inline int
value_table_touch(
	struct value_table *value_table, uint32_t h_idx, uint32_t v_idx
) {
	uint32_t **values = ADDR_OF(&value_table->values);
	uint64_t idx = (v_idx * value_table->h_dim) + h_idx;

	uint32_t *value = ADDR_OF(values + idx / VALUE_TABLE_CHUNK_SIZE) +
			  (idx % VALUE_TABLE_CHUNK_SIZE);
	return remap_table_touch(&value_table->remap_table, *value, value);
}

static inline void
value_table_compact(struct value_table *value_table) {
	remap_table_compact(&value_table->remap_table);

	uint32_t **values = ADDR_OF(&value_table->values);

	for (uint64_t idx = 0; idx < value_table->h_dim * value_table->v_dim;
	     ++idx) {
		uint32_t *value =
			ADDR_OF(values + idx / VALUE_TABLE_CHUNK_SIZE) +
			(idx % VALUE_TABLE_CHUNK_SIZE);

		*value = remap_table_compacted(
			&value_table->remap_table, *value
		);
	}
}
