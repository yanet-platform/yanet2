#include "lib/filter2/compiler/helper.h"
#include "common/hash_index.h"
#include "common/registry.h"
#include "lib/filter2/filter.h"

int
collect_rule_map(
	struct memory_context *memory_context,
	struct value_registry *registry,
	const uint32_t *rule_to_group,
	const struct filter_rule **rules,
	uint32_t rule_count,
	struct vline *rule_map
) {
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
merge_and_collect_registry(
	struct memory_context *memory_context,
	struct value_registry *registry1,
	const uint32_t *rule_to_group1,
	struct value_registry *registry2,
	const uint32_t *rule_to_group2,
	uint32_t rule_count,
	struct value_table *table,
	struct value_registry *registry,
	uint32_t *rule_to_group
) {
	if (value_table_init(
		    table,
		    memory_context,
		    "filter:joint",
		    value_registry_capacity(registry1),
		    value_registry_capacity(registry2)
	    )) {
		return -1;
	}

	/*
	 * Collect the distinct group pairs into a hash index: the group of
	 * the first registry and the group of the second one each rule
	 * references.
	 */
	uint32_t pair_alloc_count = rule_count ? rule_count : 1;
	uint32_t *pair_group1 = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * pair_alloc_count
	);
	if (pair_group1 == NULL) {
		goto error_free_table;
	}

	uint32_t *pair_group2 = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * pair_alloc_count
	);
	if (pair_group2 == NULL) {
		goto error_free_pair_group1;
	}

	struct hash_index pair_index;
	if (hash_index_init(&pair_index, memory_context, rule_count)) {
		goto error_free_pair_group2;
	}

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
			goto error;
		}

		if (value_registry_join_ranges(
			    registry1,
			    pair_group1[pair_idx],
			    registry2,
			    pair_group2[pair_idx],
			    value_table_collect_action,
			    &collect_ctx
		    )) {
			goto error;
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

error:
	value_registry_fini(registry);
	memset(registry, 0, sizeof(*registry));

error_free_pairs:
	hash_index_fini(&pair_index);

error_free_pair_group2:
	memory_bfree(
		memory_context, pair_group2, sizeof(uint32_t) * pair_alloc_count
	);

error_free_pair_group1:
	memory_bfree(
		memory_context, pair_group1, sizeof(uint32_t) * pair_alloc_count
	);

error_free_table:
	value_table_free(table);

	return -1;
}
