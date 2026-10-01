#pragma once

/*
 * Rectangular value table allowing one to touch each key pair using
 * remap table.
 *
 * The values are chunked by v index: the values array holds one chunk
 * pointer per v index, and each chunk stores that row's h_dim entries,
 * so a lookup indexes the chunk by v_idx directly and never multiplies
 * or divides.
 */

#include <stdbool.h>
#include <stdint.h>
#include <string.h>

#include "memory.h"
#include "remap.h"

// Beside the dense form a table may also live in sparse mode: a
// chunked open-addressing hash over the touched (v_idx, h_idx) cells
// only, with untouched cells reading back empty_value. Large join
// outputs are typically a small fraction of the dense rectangle, so
// tables above VALUE_TABLE_DENSE_MAX_CELLS pick the sparse form;
// writes go through value_table_touch, reads through value_table_get
// as everywhere else.

// Tables at or below this many cells stay dense; larger ones go
// sparse. Sparse costs ~17 bytes per touched cell (12 B entry at max
// load 0.7), so it only wins when the dense rectangle is huge and
// sparsely filled.
#ifndef VALUE_TABLE_DENSE_MAX_CELLS
#define VALUE_TABLE_DENSE_MAX_CELLS (1u << 29)
#endif

// Sparse hash entries live in fixed-size chunks: a single balloc is
// capped by the block allocator at 2^26 bytes.
#ifndef VALUE_TABLE_SPARSE_CHUNK_BITS
#define VALUE_TABLE_SPARSE_CHUNK_BITS 16
#endif
#define VALUE_TABLE_SPARSE_CHUNK_SIZE (1u << VALUE_TABLE_SPARSE_CHUNK_BITS)

struct value_table {
	struct memory_context *memory_context;
	uint32_t v_dim;
	uint32_t h_dim;
	// Offset pointer to an array of v_dim chunk pointers, one chunk
	// of h_dim entries per v index.
	uint32_t **values;
	// Sparse mode state; meaningful only when sparse is set.
	bool sparse;
	uint64_t **sparse_key_chunks;
	uint32_t **sparse_val_chunks;
	uint32_t sparse_cap;
	uint32_t sparse_count;
	// The value untouched cells read back: the compacted zero the
	// dense form would hold.
	uint32_t empty_value;
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

	if (value_table->sparse) {
		uint32_t chunk_count =
			value_table->sparse_cap == 0
				? 0
				: ((value_table->sparse_cap - 1) >>
				   VALUE_TABLE_SPARSE_CHUNK_BITS) +
					  1;
		uint64_t **key_chunks =
			ADDR_OF(&value_table->sparse_key_chunks);
		uint32_t **val_chunks =
			ADDR_OF(&value_table->sparse_val_chunks);
		for (uint32_t chunk = 0; chunk < chunk_count; ++chunk) {
			uint64_t *keys = ADDR_OF(&key_chunks[chunk]);
			if (keys != NULL) {
				memory_bfree(
					memory_context,
					keys,
					VALUE_TABLE_SPARSE_CHUNK_SIZE *
						sizeof(uint64_t)
				);
			}
		}
		for (uint32_t chunk = 0; chunk < chunk_count; ++chunk) {
			uint32_t *vals = ADDR_OF(&val_chunks[chunk]);
			if (vals != NULL) {
				memory_bfree(
					memory_context,
					vals,
					VALUE_TABLE_SPARSE_CHUNK_SIZE *
						sizeof(uint32_t)
				);
			}
		}
		if (key_chunks != NULL) {
			memory_bfree(
				memory_context,
				key_chunks,
				chunk_count * sizeof(uint64_t *)
			);
			SET_OFFSET_OF(&value_table->sparse_key_chunks, NULL);
		}
		if (val_chunks != NULL) {
			memory_bfree(
				memory_context,
				val_chunks,
				chunk_count * sizeof(uint32_t *)
			);
			SET_OFFSET_OF(&value_table->sparse_val_chunks, NULL);
		}
		value_table->sparse_cap = 0;
		value_table->sparse_count = 0;
	}

	uint32_t **values = ADDR_OF(&value_table->values);
	if (values != NULL) {
		for (uint32_t v_idx = 0; v_idx < value_table->v_dim; ++v_idx) {
			uint32_t *chunk = ADDR_OF(values + v_idx);
			if (chunk == NULL) {
				continue;
			}
			memory_bfree(
				memory_context,
				chunk,
				value_table->h_dim * sizeof(uint32_t)
			);
			SET_OFFSET_OF(values + v_idx, NULL);
		}
		memory_bfree(
			memory_context,
			values,
			value_table->v_dim * sizeof(uint32_t *)
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
	value_table->sparse = false;
	value_table->sparse_cap = 0;
	value_table->sparse_count = 0;
	value_table->empty_value = 0;
	SET_OFFSET_OF(&value_table->sparse_key_chunks, NULL);
	SET_OFFSET_OF(&value_table->sparse_val_chunks, NULL);

	uint32_t **values = (uint32_t **)memory_balloc(
		memory_context, v_dim * sizeof(uint32_t *)
	);
	if (values == NULL) {
		value_table_free(value_table);
		return -1;
	}

	memset(values, 0, v_dim * sizeof(uint32_t *));
	SET_OFFSET_OF(&value_table->values, values);

	for (uint32_t v_idx = 0; v_idx < v_dim; ++v_idx) {
		uint32_t *chunk = (uint32_t *)memory_balloc(
			memory_context, h_dim * sizeof(uint32_t)
		);
		if (chunk == NULL) {
			value_table_free(value_table);
			return -1;
		}
		memset(chunk, 0, h_dim * sizeof(uint32_t));
		SET_OFFSET_OF(values + v_idx, chunk);
	}

	return 0;
}

// The sparse machinery: open addressing with linear probing over the
// touched cells only; keys are stored as key+1 so a zero slot means
// empty (the (0,0) cell remains representable). Slots live in
// fixed-size chunks so no single balloc exceeds the block allocator's
// cap.

static inline uint32_t
value_table_sparse_hash(uint64_t key) {
	key ^= key >> 33;
	key *= 0xff51afd7ed558ccdULL;
	key ^= key >> 33;
	key *= 0xc4ceb9fe1a85ec53ULL;
	key ^= key >> 33;
	return (uint32_t)key;
}

static inline uint64_t
value_table_sparse_key(uint32_t v_idx, uint32_t h_idx) {
	return ((uint64_t)v_idx << 32) | h_idx;
}

static inline uint32_t
value_table_sparse_chunk_count(uint32_t cap) {
	return cap == 0 ? 0 : ((cap - 1) >> VALUE_TABLE_SPARSE_CHUNK_BITS) + 1;
}

static inline uint64_t *
value_table_sparse_key_in(uint64_t **chunks, uint32_t slot) {
	return ADDR_OF(&chunks[slot >> VALUE_TABLE_SPARSE_CHUNK_BITS]) +
	       (slot & (VALUE_TABLE_SPARSE_CHUNK_SIZE - 1));
}

static inline uint32_t *
value_table_sparse_val_in(uint32_t **chunks, uint32_t slot) {
	return ADDR_OF(&chunks[slot >> VALUE_TABLE_SPARSE_CHUNK_BITS]) +
	       (slot & (VALUE_TABLE_SPARSE_CHUNK_SIZE - 1));
}

static inline uint64_t *
value_table_sparse_key_at(struct value_table *value_table, uint32_t slot) {
	return value_table_sparse_key_in(
		ADDR_OF(&value_table->sparse_key_chunks), slot
	);
}

static inline uint32_t *
value_table_sparse_val_at(struct value_table *value_table, uint32_t slot) {
	return value_table_sparse_val_in(
		ADDR_OF(&value_table->sparse_val_chunks), slot
	);
}

static inline int
value_table_sparse_grow(
	struct value_table *value_table, struct memory_context *memory_context
) {
	uint32_t new_cap = value_table->sparse_cap
				   ? value_table->sparse_cap * 2
				   : VALUE_TABLE_SPARSE_CHUNK_SIZE;
	uint32_t new_chunk_count = value_table_sparse_chunk_count(new_cap);
	uint64_t **new_key_chunks = (uint64_t **)memory_balloc(
		memory_context, new_chunk_count * sizeof(uint64_t *)
	);
	uint32_t **new_val_chunks = (uint32_t **)memory_balloc(
		memory_context, new_chunk_count * sizeof(uint32_t *)
	);
	if (new_key_chunks == NULL || new_val_chunks == NULL) {
		if (new_key_chunks != NULL) {
			memory_bfree(
				memory_context,
				new_key_chunks,
				new_chunk_count * sizeof(uint64_t *)
			);
		}
		if (new_val_chunks != NULL) {
			memory_bfree(
				memory_context,
				new_val_chunks,
				new_chunk_count * sizeof(uint32_t *)
			);
		}
		return -1;
	}

	for (uint32_t chunk = 0; chunk < new_chunk_count; ++chunk) {
		uint64_t *keys = (uint64_t *)memory_balloc(
			memory_context,
			VALUE_TABLE_SPARSE_CHUNK_SIZE * sizeof(uint64_t)
		);
		uint32_t *vals = (uint32_t *)memory_balloc(
			memory_context,
			VALUE_TABLE_SPARSE_CHUNK_SIZE * sizeof(uint32_t)
		);
		if (keys == NULL || vals == NULL) {
			if (keys != NULL) {
				memory_bfree(
					memory_context,
					keys,
					VALUE_TABLE_SPARSE_CHUNK_SIZE *
						sizeof(uint64_t)
				);
			}
			if (vals != NULL) {
				memory_bfree(
					memory_context,
					vals,
					VALUE_TABLE_SPARSE_CHUNK_SIZE *
						sizeof(uint32_t)
				);
			}
			for (uint32_t done = 0; done < chunk; ++done) {
				memory_bfree(
					memory_context,
					ADDR_OF(&new_key_chunks[done]),
					VALUE_TABLE_SPARSE_CHUNK_SIZE *
						sizeof(uint64_t)
				);
				memory_bfree(
					memory_context,
					ADDR_OF(&new_val_chunks[done]),
					VALUE_TABLE_SPARSE_CHUNK_SIZE *
						sizeof(uint32_t)
				);
			}
			memory_bfree(
				memory_context,
				new_key_chunks,
				new_chunk_count * sizeof(uint64_t *)
			);
			memory_bfree(
				memory_context,
				new_val_chunks,
				new_chunk_count * sizeof(uint32_t *)
			);
			return -1;
		}
		memset(keys, 0, VALUE_TABLE_SPARSE_CHUNK_SIZE * sizeof(uint64_t)
		);
		SET_OFFSET_OF(&new_key_chunks[chunk], keys);
		SET_OFFSET_OF(&new_val_chunks[chunk], vals);
	}

	for (uint32_t idx = 0; idx < value_table->sparse_cap; ++idx) {
		uint64_t stored = *value_table_sparse_key_at(value_table, idx);
		if (stored == 0) {
			continue;
		}
		uint32_t slot =
			value_table_sparse_hash(stored - 1) & (new_cap - 1);
		while (*value_table_sparse_key_in(new_key_chunks, slot) != 0) {
			slot = (slot + 1) & (new_cap - 1);
		}
		*value_table_sparse_key_in(new_key_chunks, slot) = stored;
		*value_table_sparse_val_in(new_val_chunks, slot) =
			*value_table_sparse_val_at(value_table, idx);
	}

	uint32_t old_chunk_count =
		value_table_sparse_chunk_count(value_table->sparse_cap);
	uint64_t **old_key_chunks = ADDR_OF(&value_table->sparse_key_chunks);
	uint32_t **old_val_chunks = ADDR_OF(&value_table->sparse_val_chunks);
	for (uint32_t chunk = 0; chunk < old_chunk_count; ++chunk) {
		memory_bfree(
			memory_context,
			ADDR_OF(&old_key_chunks[chunk]),
			VALUE_TABLE_SPARSE_CHUNK_SIZE * sizeof(uint64_t)
		);
		memory_bfree(
			memory_context,
			ADDR_OF(&old_val_chunks[chunk]),
			VALUE_TABLE_SPARSE_CHUNK_SIZE * sizeof(uint32_t)
		);
	}
	if (old_key_chunks != NULL) {
		memory_bfree(
			memory_context,
			old_key_chunks,
			old_chunk_count * sizeof(uint64_t *)
		);
	}
	if (old_val_chunks != NULL) {
		memory_bfree(
			memory_context,
			old_val_chunks,
			old_chunk_count * sizeof(uint32_t *)
		);
	}

	SET_OFFSET_OF(&value_table->sparse_key_chunks, new_key_chunks);
	SET_OFFSET_OF(&value_table->sparse_val_chunks, new_val_chunks);
	value_table->sparse_cap = new_cap;
	return 0;
}

// Inserts or updates one cell; a cell is materialized on every touch,
// mirroring the dense form's unconditional write.
static inline int
value_table_sparse_set(
	struct value_table *value_table,
	uint32_t v_idx,
	uint32_t h_idx,
	uint32_t val
) {
	struct memory_context *memory_context =
		ADDR_OF(&value_table->memory_context);
	uint64_t want = value_table_sparse_key(v_idx, h_idx) + 1;

	// An existing key updates in place without touching the load
	// factor; only a fresh insertion may grow, so a busy cell at the
	// load boundary never rehashes for nothing. An empty table has
	// no slots to probe: the grow below allocates the first chunk.
	uint32_t slot = 0;
	if (value_table->sparse_cap != 0) {
		slot = value_table_sparse_hash(want - 1) &
		       (value_table->sparse_cap - 1);
		while (*value_table_sparse_key_at(value_table, slot) != 0) {
			if (*value_table_sparse_key_at(value_table, slot) ==
			    want) {
				*value_table_sparse_val_at(value_table, slot) =
					val;
				return 0;
			}
			slot = (slot + 1) & (value_table->sparse_cap - 1);
		}
	}

	if ((uint64_t)(value_table->sparse_count + 1) * 10 >=
	    (uint64_t)value_table->sparse_cap * 7) {
		if (value_table_sparse_grow(value_table, memory_context)) {
			return -1;
		}
		slot = value_table_sparse_hash(want - 1) &
		       (value_table->sparse_cap - 1);
		while (*value_table_sparse_key_at(value_table, slot) != 0) {
			slot = (slot + 1) & (value_table->sparse_cap - 1);
		}
	}
	*value_table_sparse_key_at(value_table, slot) = want;
	*value_table_sparse_val_at(value_table, slot) = val;
	++value_table->sparse_count;
	return 0;
}

static inline uint32_t
value_table_sparse_get(
	const struct value_table *value_table, uint32_t v_idx, uint32_t h_idx
) {
	if (value_table->sparse_cap == 0) {
		return value_table->empty_value;
	}
	struct value_table *table = (struct value_table *)value_table;
	uint64_t want = value_table_sparse_key(v_idx, h_idx) + 1;
	uint32_t slot = value_table_sparse_hash(want - 1) &
			(value_table->sparse_cap - 1);
	while (*value_table_sparse_key_at(table, slot) != 0) {
		if (*value_table_sparse_key_at(table, slot) == want) {
			return *value_table_sparse_val_at(table, slot);
		}
		slot = (slot + 1) & (value_table->sparse_cap - 1);
	}
	return value_table->empty_value;
}

// A joint over a large class-space product picks the sparse form;
// everything else keeps the dense chunks.
static inline int
value_table_init_auto(
	struct value_table *value_table,
	struct memory_context *parent_context,
	const char *name,
	uint32_t v_dim,
	uint32_t h_dim
) {
	if ((uint64_t)v_dim * (uint64_t)h_dim <= VALUE_TABLE_DENSE_MAX_CELLS) {
		return value_table_init(
			value_table, parent_context, name, v_dim, h_dim
		);
	}

	struct memory_context *memory_context = (struct memory_context *)
		memory_balloc(parent_context, sizeof(*memory_context));
	if (memory_context == NULL) {
		SET_OFFSET_OF(&value_table->memory_context, NULL);
		SET_OFFSET_OF(&value_table->values, NULL);
		value_table->sparse = false;
		return -1;
	}
	memory_context_init_from(memory_context, parent_context, name);
	SET_OFFSET_OF(&value_table->memory_context, memory_context);

	value_table->v_dim = v_dim;
	value_table->h_dim = h_dim;
	SET_OFFSET_OF(&value_table->values, NULL);
	value_table->sparse = true;
	value_table->sparse_cap = 0;
	value_table->sparse_count = 0;
	value_table->empty_value = 0;
	SET_OFFSET_OF(&value_table->sparse_key_chunks, NULL);
	SET_OFFSET_OF(&value_table->sparse_val_chunks, NULL);
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
	if (value_table->sparse) {
		return value_table_sparse_get(value_table, v_idx, h_idx);
	}
	return *value_table_get_ptr(value_table, v_idx, h_idx);
}

static inline void
value_table_compact(
	struct value_table *value_table, struct remap_table *remap_table
) {
	if (value_table->sparse) {
		value_table->empty_value =
			remap_table_compacted(remap_table, 0);
		if (value_table->sparse_cap == 0) {
			return;
		}
		for (uint32_t idx = 0; idx < value_table->sparse_cap; ++idx) {
			if (*value_table_sparse_key_at(value_table, idx) != 0) {
				uint32_t *val = value_table_sparse_val_at(
					value_table, idx
				);
				*val = remap_table_compacted(remap_table, *val);
			}
		}
		return;
	}
	for (uint32_t v_idx = 0; v_idx < value_table->v_dim; ++v_idx) {
		for (uint32_t h_idx = 0; h_idx < value_table->h_dim; ++h_idx) {
			uint32_t *value =
				value_table_get_ptr(value_table, v_idx, h_idx);

			*value = remap_table_compacted(remap_table, *value);
		}
	}
}

// Remaps the current cell value through the remap table: the dense
// form writes through the cell pointer, the sparse form reads,
// remaps and stores back, materializing the cell.
static inline int
value_table_touch(
	struct value_table *value_table,
	uint32_t v_idx,
	uint32_t h_idx,
	struct remap_table *remap_table
) {
	if (!value_table->sparse) {
		uint32_t *value =
			value_table_get_ptr(value_table, v_idx, h_idx);
		if (remap_table_touch(remap_table, *value, value) < 0) {
			return -1;
		}
		return 0;
	}

	uint32_t value = value_table_sparse_get(value_table, v_idx, h_idx);
	uint32_t remapped;
	if (remap_table_touch(remap_table, value, &remapped) < 0) {
		return -1;
	}
	return value_table_sparse_set(value_table, v_idx, h_idx, remapped);
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
