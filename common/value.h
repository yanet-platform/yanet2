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

// Releases a value table, including its own memory-tree node.
//
// Safe on a zero-initialised table: value_table_init nulls memory_context
// itself before returning on any failure, so an external caller sees the
// same all-NULL state whether the table was never inited or failed to init.
static inline void
value_table_free(struct value_table *value_table) {
	struct memory_context *memory_context =
		ADDR_OF(&value_table->memory_context);
	if (memory_context == NULL) {
		return;
	}

	uint32_t **values = ADDR_OF(&value_table->values);
	if (values != NULL) {
		uint64_t value_count = value_table->v_dim;
		value_count *= value_table->h_dim;
		uint32_t chunk_count =
			(value_count + VALUE_TABLE_CHUNK_SIZE - 1) /
			VALUE_TABLE_CHUNK_SIZE;

		for (uint32_t chunk_idx = 0; chunk_idx < chunk_count;
		     ++chunk_idx) {
			uint32_t *chunk = ADDR_OF(values + chunk_idx);
			if (chunk == NULL) {
				continue;
			}
			memory_bfree(
				memory_context,
				chunk,
				VALUE_TABLE_CHUNK_SIZE * sizeof(uint32_t)
			);
			SET_OFFSET_OF(values + chunk_idx, NULL);
		}
		memory_bfree(
			memory_context, values, chunk_count * sizeof(uint32_t *)
		);
		SET_OFFSET_OF(&value_table->values, NULL);
	}

	// The memory_context was balloc'd out of its parent in
	// value_table_init, so it is released the same way. Read the parent
	// before fini, which memsets memory_context and destroys that link.
	struct memory_context *parent = ADDR_OF(&memory_context->parent);
	memory_context_fini(memory_context);
	memory_bfree(parent, memory_context, sizeof(*memory_context));
	SET_OFFSET_OF(&value_table->memory_context, NULL);
}

static inline int
value_table_init(
	struct value_table *value_table,
	struct memory_context *parent_context,
	const char *name,
	uint32_t v_dim,
	uint32_t h_dim
) {
	// Balloc'd rather than embedded: a table lives inside a tree vertex,
	// and that tree is itself embedded by value inside shared-memory
	// configs the dataplane reads, so an inline context here would
	// multiply across every vertex of every tree — an ABI change, not
	// an inspect-tree nicety.
	struct memory_context *memory_context = (struct memory_context *)
		memory_balloc(parent_context, sizeof(*memory_context));
	if (memory_context == NULL) {
		SET_OFFSET_OF(&value_table->memory_context, NULL);
		SET_OFFSET_OF(&value_table->values, NULL);
		return -1;
	}
	memory_context_init_from(memory_context, parent_context, name);
	SET_OFFSET_OF(&value_table->memory_context, memory_context);

	value_table->v_dim = v_dim;
	value_table->h_dim = h_dim;
	SET_OFFSET_OF(&value_table->values, NULL);

	uint64_t value_count = v_dim;
	value_count *= h_dim;

	uint32_t chunk_count = (value_count + VALUE_TABLE_CHUNK_SIZE - 1) /
			       VALUE_TABLE_CHUNK_SIZE;

	uint32_t **values = (uint32_t **)memory_balloc(
		memory_context, chunk_count * sizeof(uint32_t *)
	);
	if (values == NULL) {
		value_table_free(value_table);
		return -1;
	}

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
