#pragma once

/*
 * Rectangular value table allowing one to touch each key pair using
 * remap table.
 */

#include <stdint.h>

#include "memory.h"
#include "remap.h"

struct value_table {
	struct memory_context *memory_context;
	uint32_t h_dim;
	uint32_t v_dim;
	uint32_t **values;
};

static inline void
value_table_free(struct value_table *value_table) {
	struct memory_context *memory_context =
		ADDR_OF(&value_table->memory_context);

	uint32_t **values = ADDR_OF(&value_table->values);
	for (uint32_t v_idx = 0; v_idx < value_table->v_dim; ++v_idx) {
		memory_bfree(
			memory_context,
			ADDR_OF(values + v_idx),
			value_table->h_dim * sizeof(uint32_t)
		);
	}

	memory_bfree(
		memory_context, values, value_table->v_dim * sizeof(uint32_t *)
	);

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

	value_table->h_dim = h_dim;
	value_table->v_dim = v_dim;

	uint32_t **values = (uint32_t **)memory_balloc(
		memory_context, v_dim * sizeof(uint32_t *)
	);
	if (values == NULL)
		return -1;

	memset(values, 0, v_dim * sizeof(uint32_t *));
	SET_OFFSET_OF(&value_table->values, values);

	for (uint32_t v_idx = 0; v_idx < v_dim; ++v_idx) {
		uint32_t *line = (uint32_t *)memory_balloc(
			memory_context, h_dim * sizeof(uint32_t)
		);
		if (line == NULL) {
			value_table_free(value_table);
			return -1;
		}
		memset(line, 0, h_dim * sizeof(uint32_t));
		SET_OFFSET_OF(values + v_idx, line);
	}

	return 0;
}

static inline uint32_t *
value_table_get_ptr(
	struct value_table *value_table, uint32_t v_idx, uint32_t h_idx
) {
	if (v_idx >= value_table->v_dim || h_idx >= value_table->h_dim) {
		*(uint64_t *)(0) = v_idx + h_idx;
	}
	uint32_t **values = ADDR_OF_NC(&value_table->values);
	uint32_t *line = ADDR_OF_NC(values + v_idx);
	return line + h_idx;
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
