#pragma once

/*
 * Rectangular value table allowing one to touch each key pair using
 * remap table.
 */

#include <stdint.h>
#include <stdlib.h>

#include "common/memory.h"
#include "remap.h"

struct value_table {
	struct memory_context *memory_context;
	struct remap_table remap_table;
	uint32_t h_dim;
	uint32_t v_dim;
	uint32_t *values;
};

static inline int
value_table_init(
	struct value_table *value_table,
	struct memory_context *memory_context,
	uint32_t h_dim,
	uint32_t v_dim
) {
	value_table->memory_context = OFFSET_OF(value_table, memory_context);

	if (remap_table_init(
		    &value_table->remap_table, memory_context, h_dim * v_dim
	    )) {
		return -1;
	}

	uint32_t *values = (uint32_t *)memory_balloc(
		memory_context, h_dim * v_dim * sizeof(uint32_t)
	);
	memset(values, 0, h_dim * v_dim * sizeof(uint32_t));

	if (values == NULL) {
		remap_table_free(&value_table->remap_table);
		return -1;
	}

	value_table->values = OFFSET_OF(value_table, values);

	value_table->h_dim = h_dim;
	value_table->v_dim = v_dim;

	return 0;
}

static inline void
value_table_free(struct value_table *value_table) {
	remap_table_free(&value_table->remap_table);

	struct memory_context *memory_context =
		ADDR_OF(value_table, value_table->memory_context);
	uint32_t *values = ADDR_OF(value_table, value_table->values);
	memory_bfree(
		memory_context,
		values,
		value_table->h_dim * value_table->v_dim * sizeof(uint32_t)
	);
}

static inline void
value_table_new_gen(struct value_table *value_table) {
	remap_table_new_gen(&value_table->remap_table);
}

static inline uint32_t
value_table_get(
	struct value_table *value_table, uint32_t h_idx, uint32_t v_idx
) {
	uint32_t *values = ADDR_OF(value_table, value_table->values);
	return values[(v_idx * value_table->h_dim) + h_idx];
}

typedef int (*value_table_touch_func)(uint32_t *value, void *data);

static inline int
value_table_touch(
	struct value_table *value_table, uint32_t h_idx, uint32_t v_idx
) {
	uint32_t *values = ADDR_OF(value_table, value_table->values);
	uint32_t *value = values + (v_idx * value_table->h_dim) + h_idx;
	return remap_table_touch(&value_table->remap_table, *value, value);
}

static inline void
value_table_compact(struct value_table *value_table) {
	remap_table_compact(&value_table->remap_table);

	uint32_t *values = ADDR_OF(value_table, value_table->values);

	for (uint32_t vidx = 0; vidx < value_table->h_dim * value_table->v_dim;
	     ++vidx) {
		values[vidx] = remap_table_compacted(
			&value_table->remap_table, values[vidx]
		);
	}
}
