#pragma once

/*
 * Rectangular value table allowing one to touch each key pair using
 * remap table.
 *
 * The values are indexed by v: the values array holds one row
 * pointer per v index, and each row stores h_dim entries,
 * so a lookup indexes the chunk by v_idx directly and never multiplies
 * or divides.
 */

#include <stdint.h>

#include "memory.h"
#include "remap.h"

#define VALUE_TABLE_SLAB_TARGET_SIZE (1u << 20)

struct value_table_slab {
	struct value_table_slab *next;
	uint32_t *data;
	size_t size;
	uint32_t used;
	uint32_t capacity;
};

struct value_table {
	struct memory_context *memory_context;
	uint32_t v_dim;
	uint32_t h_dim;
	// Row pointers may alias after immutable final-row canonicalization.
	uint32_t **values;
	struct value_table_slab *slabs;
	uint32_t row_count;
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
		memory_bfree(
			memory_context,
			values,
			value_table->v_dim * sizeof(uint32_t *)
		);
		SET_OFFSET_OF(&value_table->values, NULL);
	}
	struct value_table_slab *slab = ADDR_OF(&value_table->slabs);
	while (slab != NULL) {
		struct value_table_slab *next = ADDR_OF(&slab->next);
		memory_bfree(memory_context, ADDR_OF(&slab->data), slab->size);
		memory_bfree(memory_context, slab, sizeof(*slab));
		slab = next;
	}
	SET_OFFSET_OF(&value_table->slabs, NULL);
	value_table->row_count = 0;

	// The memory_context was balloc'd out of its parent in
	// value_table_init, so it is released the same way. Read the parent
	// before fini, which memsets memory_context and destroys that link.
	struct memory_context *parent = ADDR_OF(&memory_context->parent);
	memory_context_fini(memory_context);
	memory_bfree(parent, memory_context, sizeof(*memory_context));
	SET_OFFSET_OF(&value_table->memory_context, NULL);
}

static inline int
value_table_init_rows(
	struct value_table *value_table,
	struct memory_context *parent_context,
	const char *name,
	uint32_t v_dim,
	uint32_t h_dim
) {
	memset(value_table, 0, sizeof(*value_table));
	if (v_dim == 0 || h_dim == 0) {
		return -1;
	}
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

	uint32_t **values = (uint32_t **)memory_balloc(
		memory_context, v_dim * sizeof(uint32_t *)
	);
	if (values == NULL) {
		value_table_free(value_table);
		return -1;
	}

	memset(values, 0, v_dim * sizeof(uint32_t *));
	SET_OFFSET_OF(&value_table->values, values);

	return 0;
}

// Allocates an owned row without changing the lookup directory.
//
// Slabs pack whole rows; row pointers never own interior allocations.
static inline uint32_t *
value_table_alloc_row(struct value_table *table) {
	struct memory_context *ctx = ADDR_OF(&table->memory_context);
	struct value_table_slab *slab = ADDR_OF(&table->slabs);
	size_t row_size = (size_t)table->h_dim * sizeof(uint32_t);
	if (slab == NULL || slab->used == slab->capacity) {
		size_t size = row_size * (table->v_dim - table->row_count);
		// Keep allocator red zones within the 1 MiB size class.
		size_t limit = VALUE_TABLE_SLAB_TARGET_SIZE - 2 * ASAN_RED_ZONE;
		if (row_size > limit) {
			limit = row_size;
		}
		if (size > limit) {
			size = limit;
		}
		struct value_table_slab *next =
			memory_balloc(ctx, sizeof(*next));
		if (next == NULL) {
			return NULL;
		}
		memset(next, 0, sizeof(*next));
		uint32_t *data = memory_balloc(ctx, size);
		if (data == NULL) {
			memory_bfree(ctx, next, sizeof(*next));
			return NULL;
		}
		SET_OFFSET_OF(&next->next, slab);
		SET_OFFSET_OF(&next->data, data);
		next->size = size;
		next->capacity = size / row_size;
		SET_OFFSET_OF(&table->slabs, next);
		slab = next;
	}
	uint32_t *row =
		ADDR_OF(&slab->data) + (size_t)slab->used * table->h_dim;
	++slab->used;
	++table->row_count;
	return row;
}

static inline int
value_table_init(
	struct value_table *table,
	struct memory_context *parent,
	const char *name,
	uint32_t v_dim,
	uint32_t h_dim
) {
	if (value_table_init_rows(table, parent, name, v_dim, h_dim)) {
		return -1;
	}
	uint32_t **values = ADDR_OF(&table->values);
	for (uint32_t v = 0; v < v_dim; ++v) {
		uint32_t *row = value_table_alloc_row(table);
		if (row == NULL) {
			value_table_free(table);
			return -1;
		}
		memset(row, 0, (size_t)h_dim * sizeof(uint32_t));
		SET_OFFSET_OF(values + v, row);
	}
	return 0;
}

static inline uint32_t *
value_table_get_ptr(
	const struct value_table *value_table, uint32_t v_idx, uint32_t h_idx
) {
	// Cast away const for the offset-pointer access: lookup is a read-only
	// operation and the value table is immutable after compilation.
	struct value_table *table = (struct value_table *)value_table;
	// values and the chunk pointers are set at init and cleared only by
	// value_table_free, which never races a lookup — so on the query path
	// they are never NULL and the NULL test in ADDR_OF is pure per-lookup
	// overhead. The chunk is indexed by v_idx and the h_idx stays
	// in-chunk, so the lookup adds and never multiplies or divides.
	uint32_t **values = ADDR_OF_NONNULL(&table->values);
	return ADDR_OF_NONNULL(values + v_idx) + h_idx;
}

static inline uint32_t
value_table_get(
	const struct value_table *value_table, uint32_t v_idx, uint32_t h_idx
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

/*
 * Single-dimensional value line: a value_table of one row, with the
 * values kept in one contiguous memory chunk instead of the chunked
 * pointer array.
 */
struct vline {
	struct memory_context *memory_context;
	uint32_t size;
	uint32_t *values;
};

// Releases a value line, including its own memory-tree node.
//
// Safe on a zero-initialised line, mirroring value_table_free.
static inline void
vline_free(struct vline *vline) {
	struct memory_context *memory_context = ADDR_OF(&vline->memory_context);
	if (memory_context == NULL) {
		return;
	}

	uint32_t *values = ADDR_OF(&vline->values);
	if (values != NULL) {
		memory_bfree(
			memory_context, values, vline->size * sizeof(uint32_t)
		);
		SET_OFFSET_OF(&vline->values, NULL);
	}

	// The memory_context was balloc'd out of its parent in vline_init,
	// so it is released the same way.
	struct memory_context *parent = ADDR_OF(&memory_context->parent);
	memory_context_fini(memory_context);
	memory_bfree(parent, memory_context, sizeof(*memory_context));
	SET_OFFSET_OF(&vline->memory_context, NULL);
}

static inline int
vline_init(
	struct vline *vline,
	struct memory_context *parent_context,
	const char *name,
	uint32_t size
) {
	// Balloc'd rather than embedded for the same reason as in
	// value_table_init: a line lives inside shared-memory configs the
	// dataplane reads, so an inline context would multiply across
	// every line.
	struct memory_context *memory_context = (struct memory_context *)
		memory_balloc(parent_context, sizeof(*memory_context));
	if (memory_context == NULL) {
		SET_OFFSET_OF(&vline->memory_context, NULL);
		SET_OFFSET_OF(&vline->values, NULL);
		return -1;
	}

	memory_context_init_from(memory_context, parent_context, name);
	SET_OFFSET_OF(&vline->memory_context, memory_context);

	vline->size = size;
	SET_OFFSET_OF(&vline->values, NULL);

	uint32_t *values = (uint32_t *)memory_balloc(
		memory_context, size * sizeof(uint32_t)
	);
	if (values == NULL) {
		vline_free(vline);
		return -1;
	}

	memset(values, 0, size * sizeof(uint32_t));
	SET_OFFSET_OF(&vline->values, values);

	return 0;
}

static inline uint32_t *
vline_get_ptr(struct vline *vline, uint32_t idx) {
	// The values chunk is set at init and cleared only by vline_free,
	// which never races a lookup, so on the query path it is never
	// NULL.
	uint32_t *values = ADDR_OF_NONNULL(&vline->values);
	return values + idx;
}

static inline uint32_t
vline_get(struct vline *vline, uint32_t idx) {
	return *vline_get_ptr(vline, idx);
}

static inline void
vline_compact(struct vline *vline, struct remap_table *remap_table) {
	for (uint32_t idx = 0; idx < vline->size; ++idx) {
		uint32_t *value = vline_get_ptr(vline, idx);
		*value = remap_table_compacted(remap_table, *value);
	}
}
