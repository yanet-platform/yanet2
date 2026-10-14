#pragma once

/*
 * Immutable joint of first-match rule rows over deduplicated left
 * classes: the builder joins the rule projection, interns every
 * distinct row as one compact class, and hands out the map from the
 * original left class space to the compact class list.
 *
 * The value_table stays the mutable chunked table of the compile
 * stages; this one is written once by the final join and only read
 * afterwards, so the duplicate left classes - the distinctions
 * shadowed by earlier rules - cost nothing but a map slot.
 */

#include <stdint.h>

#include "common/memory.h"
#include "common/remap.h"
#include "common/value.h"

struct rule_table {
	struct memory_context *memory_context;
	// The original left class space the map is indexed by.
	uint32_t v_dim;
	uint32_t h_dim;
	// The original left class of the packet resolved to its compact
	// class - the interned row it shares with every class of an equal
	// first-match row.
	struct vline class_map;
	// The compact class rows, exactly class_count chunks of h_dim
	// cells. The builder fills them before the map goes in, so a
	// failed build leaves the map empty and the free below safe.
	uint32_t **rows;
	uint32_t class_count;
};

// Releases a rule table, including its own memory-tree node.
//
// Safe on a zero-initialised table and on the partial state of a
// failed build: the map is published only after every row is in.
static inline void
rule_table_free(struct rule_table *rule_table) {
	struct memory_context *memory_context =
		ADDR_OF(&rule_table->memory_context);
	if (memory_context == NULL) {
		return;
	}

	uint32_t **rows = ADDR_OF(&rule_table->rows);
	if (rows != NULL) {
		for (uint32_t idx = 0; idx < rule_table->class_count; ++idx) {
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
			rule_table->class_count * sizeof(uint32_t *)
		);
		SET_OFFSET_OF(&rule_table->rows, NULL);
	}
	rule_table->class_count = 0;
	vline_free(&rule_table->class_map);

	// The memory_context was balloc'd out of its parent in
	// rule_table_init, so it is released the same way. Read the parent
	// before fini, which memsets memory_context and destroys that link.
	struct memory_context *parent = ADDR_OF(&memory_context->parent);
	memory_context_fini(memory_context);
	memory_bfree(parent, memory_context, sizeof(*memory_context));
	SET_OFFSET_OF(&rule_table->memory_context, NULL);
}

// Prepares the map holder of a rule table under its own memory context;
// the class rows and the map itself are filled by the builder.
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
	rule_table->class_count = 0;
	memset(&rule_table->class_map, 0, sizeof(rule_table->class_map));

	return 0;
}

static inline uint32_t
rule_table_get(
	const struct rule_table *rule_table, uint32_t v_idx, uint32_t h_idx
) {
	// The map and the rows are set by the builder and cleared only by
	// rule_table_free, which never races a lookup, so on the query
	// path they are never NULL.
	struct rule_table *table = (struct rule_table *)rule_table;
	uint32_t compact = vline_get(&table->class_map, v_idx);
	uint32_t **rows = ADDR_OF_NONNULL(&table->rows);
	uint32_t *row = ADDR_OF_NONNULL(rows + compact);
	return row[h_idx];
}
