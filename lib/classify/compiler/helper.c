#include "lib/classify/compiler/helper.h"
#include "common/hash_index.h"
#include "common/registry.h"
#include "lib/classify/classify.h"
#include "lib/classify/compiler/declare.h"
#include <stdlib.h>

int
classify_decode(
	struct memory_context *memory_context,
	const struct classifier *cls,
	const struct classifier_rule *const *rules,
	uint32_t rule_count,
	struct vline *rule_map
) {
	const struct value_registry *registry = &cls->registry;
	const uint32_t *rule_to_group = cls->rule_groups;
	if (vline_init(
		    rule_map,
		    memory_context,
		    "filter:rules",
		    value_registry_capacity(registry)
	    )) {
		return -1;
	}

	for (uint32_t idx = 0; idx < rule_map->size; ++idx) {
		*vline_get_ptr(rule_map, idx) = FILTER_RULE_INVALID;
	}

	/*
	 * The same class may be produced by several rules, the first one
	 * wins to preserve the rule ordering.
	 */
	struct value_range *ranges = ADDR_OF(&registry->ranges);
	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		// A rule absent from the ruleset this map belongs to holds no
		// group even when the classes were enumerated over a wider
		// shared ruleset.
		if (rules[rule_idx] == NULL) {
			continue;
		}

		uint32_t group_idx = rule_to_group[rule_idx];
		if (group_idx == FILTER_GROUP_INVALID) {
			continue;
		}

		struct value_range *range = ranges + group_idx;
		uint32_t *values = ADDR_OF(&range->values);

		for (uint32_t idx = 0; idx < range->count; ++idx) {
			uint32_t *rule_idx_ptr =
				vline_get_ptr(rule_map, values[idx]);
			if (*rule_idx_ptr == FILTER_RULE_INVALID) {
				*rule_idx_ptr = rule_idx;
			}
		}
	}

	return 0;
}

//////////////////////////////////////////////////////////////////////////////

struct touch_ctx {
	struct value_table *value_table;
	struct remap_table *remap_table;
};

static int
value_table_touch_action(uint32_t v1, uint32_t v2, uint32_t idx, void *data) {
	(void)idx;
	struct touch_ctx *touch_ctx = (struct touch_ctx *)data;

	uint32_t *value = value_table_get_ptr(touch_ctx->value_table, v1, v2);
	if (remap_table_touch(touch_ctx->remap_table, *value, value) < 0) {
		return -1;
	}
	return 0;
}

static inline uint32_t
merge_group_pair_hash(uint32_t left, uint32_t right) {
	uint32_t hash = left * 2654435761u;
	hash ^= right * 2246822519u;
	return hash * 3266489917u;
}

struct merge_pair_ctx {
	const uint32_t *pair_group1;
	const uint32_t *pair_group2;
	uint32_t group1;
	uint32_t group2;
};

static int
merge_pair_eq(uint32_t value, const void *data) {
	const struct merge_pair_ctx *pair_ctx =
		(const struct merge_pair_ctx *)data;
	return pair_ctx->pair_group1[value] != pair_ctx->group1 ||
	       pair_ctx->pair_group2[value] != pair_ctx->group2;
}

struct value_collect_ctx {
	struct value_table *table;
	struct value_registry *registry;
};

static int
value_table_collect_action(uint32_t v1, uint32_t v2, uint32_t idx, void *data) {
	(void)idx;
	struct value_collect_ctx *collect_ctx =
		(struct value_collect_ctx *)data;
	return value_registry_collect(
		collect_ctx->registry,
		value_table_get(collect_ctx->table, v1, v2)
	);
}

/*
 * Joins two group registries into a joint stage of the classification.
 *
 * Rules referencing the same group pair on both sides produce the same
 * touched cell joins, so the stage operates on distinct pairs instead of
 * all rules. On success the routine initializes the value table holding
 * the join results, the registry with one range per pair holding the
 * pair matching class identifiers, and fills the rule to pair mapping;
 * a rule without a value on either side keeps the invalid group mark.
 */
int
classify_join(
	struct memory_context *memory_context,
	const struct classifier *left,
	const struct classifier *right,
	uint32_t rule_count,
	struct value_table *table,
	struct classifier *out
) {
	const struct value_registry *registry1 = &left->registry;
	const uint32_t *rule_to_group1 = left->rule_groups;
	const struct value_registry *registry2 = &right->registry;
	const uint32_t *rule_to_group2 = right->rule_groups;
	struct value_registry *registry = &out->registry;
	uint32_t *rule_to_group;

	if (classifier_init(out, memory_context, rule_count)) {
		return -1;
	}
	rule_to_group = out->rule_groups;

	// Pair collection scratch, emptied here so a failure of the table
	// below unwinds through the common exit.
	uint32_t pair_alloc_count = rule_count ? rule_count : 1;
	uint32_t *pair_group1 = NULL;
	uint32_t *pair_group2 = NULL;
	struct hash_index pair_index = {0};
	bool pair_index_live = false;

	if (value_table_init(
		    table,
		    memory_context,
		    "filter:joint",
		    value_registry_capacity(registry1),
		    value_registry_capacity(registry2)
	    )) {
		goto error_free_pairs;
	}

	/*
	 * Collect the distinct group pairs into a hash index: the group of
	 * the first registry and the group of the second one each rule
	 * references.
	 */
	pair_group1 = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * pair_alloc_count
	);
	if (pair_group1 == NULL) {
		goto error_free_pairs;
	}

	pair_group2 = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * pair_alloc_count
	);
	if (pair_group2 == NULL) {
		goto error_free_pairs;
	}

	if (hash_index_init(&pair_index, memory_context, rule_count)) {
		goto error_free_pairs;
	}
	pair_index_live = true;

	uint32_t pair_count = 0;
	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		uint32_t group1 = rule_to_group1[rule_idx];
		uint32_t group2 = rule_to_group2[rule_idx];
		rule_to_group[rule_idx] = FILTER_GROUP_INVALID;

		if (group1 == FILTER_GROUP_INVALID ||
		    group2 == FILTER_GROUP_INVALID) {
			continue;
		}

		uint32_t hash = merge_group_pair_hash(group1, group2);
		struct merge_pair_ctx pair_ctx = {
			pair_group1,
			pair_group2,
			group1,
			group2,
		};

		uint32_t pair = hash_index_lookup(
			&pair_index, hash, merge_pair_eq, &pair_ctx
		);
		if (pair == HASH_INDEX_INVALID) {
			pair = pair_count;
			if (hash_index_insert(&pair_index, hash, pair)) {
				goto error_free_pairs;
			}
			pair_group1[pair] = group1;
			pair_group2[pair] = group2;
			++pair_count;
		}

		rule_to_group[rule_idx] = pair;
	}

	/*
	 * The remap table is used to enumerate the cell joins - each pair
	 * touches its value joins so each cell gains a value specific to
	 * the matching set of pairs.
	 */
	struct remap_table remap_table;
	if (remap_table_init(
		    &remap_table,
		    memory_context,
		    value_registry_capacity(registry1) *
			    value_registry_capacity(registry2)
	    )) {
		goto error_free_pairs;
	}

	struct touch_ctx touch_ctx;
	touch_ctx.value_table = table;
	touch_ctx.remap_table = &remap_table;

	for (uint32_t pair_idx = 0; pair_idx < pair_count; ++pair_idx) {
		remap_table_new_gen(&remap_table);

		// Touch the pair matching value joins
		if (value_registry_join_ranges(
			    registry1,
			    pair_group1[pair_idx],
			    registry2,
			    pair_group2[pair_idx],
			    value_table_touch_action,
			    &touch_ctx
		    )) {
			remap_table_free(&remap_table);
			goto error_free_pairs;
		}
	}

	/*
	 * Remap table compaction removes gaps of unused values and makes
	 * the resulting set of values smaller
	 */

	remap_table_compact(&remap_table);
	value_table_compact(table, &remap_table);
	remap_table_free(&remap_table);

	if (value_registry_init(registry, memory_context, "filter:joint")) {
		goto error_free_pairs;
	}

	/*
	 * Collect the pair matching class identifiers into the registry,
	 * one range per pair.
	 */
	struct value_collect_ctx collect_ctx;
	collect_ctx.table = table;
	collect_ctx.registry = registry;

	for (uint32_t pair_idx = 0; pair_idx < pair_count; ++pair_idx) {
		if (value_registry_start(registry)) {
			goto error_free_pairs;
		}

		if (value_registry_join_ranges(
			    registry1,
			    pair_group1[pair_idx],
			    registry2,
			    pair_group2[pair_idx],
			    value_table_collect_action,
			    &collect_ctx
		    )) {
			goto error_free_pairs;
		}
	}

	hash_index_fini(&pair_index);
	memory_bfree(
		memory_context, pair_group2, sizeof(uint32_t) * pair_alloc_count
	);
	memory_bfree(
		memory_context, pair_group1, sizeof(uint32_t) * pair_alloc_count
	);

	return 0;

error_free_pairs:
	// One unwind in reverse allocation order, every release guarded:
	// a failed join leaves no state behind, whatever the stage.
	value_table_free(table);
	value_registry_fini(registry);
	memset(registry, 0, sizeof(*registry));
	memory_bfree(
		memory_context,
		out->rule_groups,
		sizeof(uint32_t) * (rule_count ? rule_count : 1)
	);
	out->rule_groups = NULL;
	if (pair_index_live) {
		hash_index_fini(&pair_index);
	}
	if (pair_group2 != NULL) {
		memory_bfree(
			memory_context,
			pair_group2,
			sizeof(uint32_t) * pair_alloc_count
		);
	}
	if (pair_group1 != NULL) {
		memory_bfree(
			memory_context,
			pair_group1,
			sizeof(uint32_t) * pair_alloc_count
		);
	}

	return -1;
}

struct final_row_ctx {
	const struct value_table *table;
	const uint32_t *row;
};

static int
final_row_eq(uint32_t value, const void *data) {
	const struct final_row_ctx *ctx = data;
	return memcmp(
		value_table_get_ptr(ctx->table, value, 0),
		ctx->row,
		(size_t)ctx->table->h_dim * sizeof(uint32_t)
	);
}

// Builds immutable first-match rows without enumerating final classes.
//
// Only distinct group pairs are inverted onto the left classes. Each row is
// completed in rule order, interned by content, then packed into owned slabs.
// Heap scratch is never published or charged to the shared-memory arena.
int
classify_join_rules(
	struct memory_context *memory_context,
	const struct classifier *left,
	const struct classifier *right,
	const struct classifier_rule *const *rules,
	uint32_t rule_count,
	struct value_table *table
) {
	memset(table, 0, sizeof(*table));
	uint32_t v_dim = value_registry_capacity(&left->registry);
	uint32_t h_dim = value_registry_capacity(&right->registry);
	size_t pair_size =
		(size_t)(rule_count ? rule_count : 1) * sizeof(uint32_t);
	uint32_t *group1 = malloc(pair_size);
	uint32_t *group2 = malloc(pair_size);
	uint32_t *first = malloc(pair_size);
	size_t *offsets = calloc((size_t)v_dim + 1, sizeof(size_t));
	size_t *cursor = malloc((size_t)v_dim * sizeof(size_t));
	uint32_t *row = malloc((size_t)h_dim * sizeof(uint32_t));
	uint32_t *edges = NULL;
	struct hash_index pairs = {0};
	struct hash_index rows = {0};
	int rc = -1;
	if (group1 == NULL || group2 == NULL || first == NULL ||
	    offsets == NULL || cursor == NULL || row == NULL) {
		goto done;
	}
	if (value_table_init_rows(
		    table, memory_context, "filter:rules", v_dim, h_dim
	    ) ||
	    hash_index_init(&pairs, memory_context, rule_count) ||
	    hash_index_init(&rows, memory_context, v_dim)) {
		goto done;
	}
	struct value_range *left_ranges = ADDR_OF(&left->registry.ranges);
	struct value_range *right_ranges = ADDR_OF(&right->registry.ranges);
	uint32_t pair_count = 0;
	for (uint32_t r = 0; r < rule_count; ++r) {
		uint32_t g1 = left->rule_groups[r];
		uint32_t g2 = right->rule_groups[r];
		if (rules[r] == NULL || g1 == FILTER_GROUP_INVALID ||
		    g2 == FILTER_GROUP_INVALID) {
			continue;
		}
		struct merge_pair_ctx ctx = {group1, group2, g1, g2};
		uint32_t hash = merge_group_pair_hash(g1, g2);
		if (hash_index_lookup(&pairs, hash, merge_pair_eq, &ctx) !=
		    HASH_INDEX_INVALID) {
			continue;
		}
		if (hash_index_insert(&pairs, hash, pair_count)) {
			goto done;
		}
		group1[pair_count] = g1;
		group2[pair_count] = g2;
		first[pair_count++] = r;
		const struct value_range *range = left_ranges + g1;
		const uint32_t *values = ADDR_OF(&range->values);
		for (uint64_t i = 0; i < range->count; ++i) {
			++offsets[values[i] + 1];
		}
	}
	for (uint32_t v = 0; v < v_dim; ++v) {
		offsets[v + 1] += offsets[v];
		cursor[v] = offsets[v];
	}
	size_t edge_count = offsets[v_dim];
	if (edge_count > SIZE_MAX / sizeof(uint32_t)) {
		goto done;
	}
	edges = malloc((edge_count ? edge_count : 1) * sizeof(uint32_t));
	if (edges == NULL) {
		goto done;
	}
	for (uint32_t p = 0; p < pair_count; ++p) {
		const struct value_range *range = left_ranges + group1[p];
		const uint32_t *values = ADDR_OF(&range->values);
		for (uint64_t i = 0; i < range->count; ++i) {
			edges[cursor[values[i]]++] = p;
		}
	}
	uint32_t **values = ADDR_OF(&table->values);
	for (uint32_t v = 0; v < v_dim; ++v) {
		memset(row, 0xff, (size_t)h_dim * sizeof(uint32_t));
		uint32_t hash = 0;
		for (size_t e = offsets[v]; e < offsets[v + 1]; ++e) {
			uint32_t p = edges[e];
			const struct value_range *range =
				right_ranges + group2[p];
			const uint32_t *columns = ADDR_OF(&range->values);
			for (uint64_t i = 0; i < range->count; ++i) {
				uint32_t h = columns[i];
				if (row[h] == FILTER_RULE_INVALID) {
					row[h] = first[p];
					hash ^= merge_group_pair_hash(
						h, first[p]
					);
				}
			}
		}
		struct final_row_ctx ctx = {table, row};
		uint32_t canonical =
			hash_index_lookup(&rows, hash, final_row_eq, &ctx);
		uint32_t *stored;
		if (canonical == HASH_INDEX_INVALID) {
			stored = value_table_alloc_row(table);
			if (stored == NULL) {
				goto done;
			}
			memcpy(stored, row, (size_t)h_dim * sizeof(uint32_t));
			SET_OFFSET_OF(values + v, stored);
			if (hash_index_insert(&rows, hash, v)) {
				goto done;
			}
		} else {
			stored = value_table_get_ptr(table, canonical, 0);
			SET_OFFSET_OF(values + v, stored);
		}
	}
	rc = 0;
done:
	hash_index_fini(&rows);
	hash_index_fini(&pairs);
	free(edges);
	free(row);
	free(cursor);
	free(offsets);
	free(first);
	free(group2);
	free(group1);
	if (rc != 0) {
		value_table_free(table);
	}
	return rc;
}
