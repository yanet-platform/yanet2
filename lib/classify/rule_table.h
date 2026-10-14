#pragma once

/*
 * Immutable joint of first-match rule rows: the lookup directory holds
 * one row pointer per left class, and the classes whose rows are
 * content-equal share one interned row out of a single packed
 * allocation.
 *
 * The value_table stays the mutable chunked table of the compile
 * stages; this one is written once by the final join and only read
 * afterwards, so the rows need no per-row ownership and the duplicate
 * rows cost nothing but a directory slot.
 */

#include <stdint.h>

#include "common/memory.h"
#include "common/remap.h"

struct rule_table {
	struct memory_context *memory_context;
	uint32_t v_dim;
	uint32_t h_dim;
	// Offset pointer to the directory of v_dim interned row pointers.
	uint32_t **values;
	// Offset pointer to the interned distinct rows, exactly row_count
	// chunks of h_dim cells. The builder fills it after the interning,
	// so a failed build leaves it NULL and the directory all-NULL.
	uint32_t **rows;
	uint32_t row_count;
};

// Releases a rule table, including its own memory-tree node.
//
// Safe on a zero-initialised table and on the partial state of a
// failed build: the rows buffer appears only after the whole directory
// is interned, so the free below never walks interior pointers.
static inline void
rule_table_free(struct rule_table *rule_table) {
	struct memory_context *memory_context =
		ADDR_OF(&rule_table->memory_context);
	if (memory_context == NULL) {
		return;
	}

	uint32_t **rows = ADDR_OF(&rule_table->rows);
	if (rows != NULL) {
		for (uint32_t idx = 0; idx < rule_table->row_count; ++idx) {
			uint32_t *row = ADDR_OF(rows + idx);
			if (row == NULL) {
				continue;
			}
			memory_bfree(
				memory_context,
				row,
				rule_table->h_dim * sizeof(uint32_t)
			);
			SET_OFFSET_OF(rows + idx, NULL);
		}
		memory_bfree(
			memory_context,
			rows,
			rule_table->row_count * sizeof(uint32_t *)
		);
		SET_OFFSET_OF(&rule_table->rows, NULL);
	}

	uint32_t **values = ADDR_OF(&rule_table->values);
	if (values != NULL) {
		memory_bfree(
			memory_context,
			values,
			rule_table->v_dim * sizeof(uint32_t *)
		);
		SET_OFFSET_OF(&rule_table->values, NULL);
	}

	// The memory_context was balloc'd out of its parent in
	// rule_table_init, so it is released the same way. Read the parent
	// before fini, which memsets memory_context and destroys that link.
	struct memory_context *parent = ADDR_OF(&memory_context->parent);
	memory_context_fini(memory_context);
	memory_bfree(parent, memory_context, sizeof(*memory_context));
	SET_OFFSET_OF(&rule_table->memory_context, NULL);
}

// Prepares the directory of a rule table under its own memory context;
// the rows are packed and published by the builder afterwards.
static inline int
rule_table_init(
	struct rule_table *rule_table,
	struct memory_context *parent_context,
	const char *name,
	uint32_t v_dim,
	uint32_t h_dim
) {
	memset(rule_table, 0, sizeof(*rule_table));
	if (v_dim == 0 || h_dim == 0) {
		return -1;
	}
	struct memory_context *memory_context = (struct memory_context *)
		memory_balloc(parent_context, sizeof(*memory_context));
	if (memory_context == NULL) {
		return -1;
	}
	memory_context_init_from(memory_context, parent_context, name);
	SET_OFFSET_OF(&rule_table->memory_context, memory_context);

	rule_table->v_dim = v_dim;
	rule_table->h_dim = h_dim;
	SET_OFFSET_OF(&rule_table->rows, NULL);
	rule_table->row_count = 0;

	uint32_t **values = (uint32_t **)memory_balloc(
		memory_context, v_dim * sizeof(uint32_t *)
	);
	if (values == NULL) {
		rule_table_free(rule_table);
		return -1;
	}
	memset(values, 0, v_dim * sizeof(uint32_t *));
	SET_OFFSET_OF(&rule_table->values, values);

	return 0;
}

static inline uint32_t
rule_table_get(
	const struct rule_table *rule_table, uint32_t v_idx, uint32_t h_idx
) {
	// The directory and the rows are set by the builder and cleared
	// only by rule_table_free, which never races a lookup, so on the
	// query path they are never NULL.
	struct rule_table *table = (struct rule_table *)rule_table;
	uint32_t **values = ADDR_OF_NONNULL(&table->values);
	return ADDR_OF_NONNULL(values + v_idx)[h_idx];
}
