#include "lib/classify/compiler/helper.h"

#include "common/hash_index.h"
#include "common/registry.h"
#include "lib/classify/classify.h"
#include "lib/classify/compiler/declare.h"
#include <stdint.h>
#include <stdlib.h>

int
classify_decode(
	struct memory_context *memory_context,
	const char *name,
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
		    name,
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

/*
 * The rule coverage classes of a stage's value space over a rule
 * projection.
 *
 * Every value covered by the same set of projected rules gains one
 * class id, numbered from one; zero stays the not covered mark. The
 * map lands in shared memory as a value line over the stage's value
 * space.
 *
 * The stage must be a join output: its classes are numbered from
 * one, so zero never carries coverage. An attribute side may hold
 * class zero inside a range, and this routine would drop that
 * coverage silently.
 */
int
classify_coverage_map(
	struct memory_context *memory_context,
	const struct classifier *stage,
	uint32_t rule_count,
	const uint8_t *rule_mask,
	struct vline *map
) {
	const struct value_registry *registry = &stage->registry;
	const uint32_t *rule_to_group = stage->rule_groups;
	uint32_t value_space = value_registry_capacity(registry);
	const struct value_range *ranges = ADDR_OF(&registry->ranges);

	if (value_space <= 1) {
		return vline_init(map, memory_context, "filter:compact", 1);
	}

	uint32_t *counts = (uint32_t *)calloc(value_space, sizeof(uint32_t));
	uint32_t *gen = (uint32_t *)calloc(value_space, sizeof(uint32_t));
	if (counts == NULL || gen == NULL) {
		free(counts);
		free(gen);
		return -1;
	}

	// Per projected rule coverage marks over the group ranges; the
	// generation stamp keeps a rule covering a value through
	// duplicate range entries counted once, and the ascending rule
	// order sorts the lists.
	uint32_t cur_gen = 0;
	uint32_t *offsets = NULL;
	uint32_t *filled = NULL;
	uint32_t *lists = NULL;
	for (uint32_t pass = 0; pass < 2; ++pass) {
		for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
			uint32_t group = rule_to_group[rule_idx];
			if (group == FILTER_GROUP_INVALID) {
				continue;
			}
			if (rule_mask != NULL && !rule_mask[rule_idx]) {
				continue;
			}
			const struct value_range *range = ranges + group;
			uint32_t *values = ADDR_OF(&range->values);
			++cur_gen;
			for (uint32_t idx = 0; idx < range->count; ++idx) {
				uint32_t value = values[idx];
				if (gen[value] == cur_gen) {
					continue;
				}
				gen[value] = cur_gen;
				if (pass == 0) {
					++counts[value];
				} else {
					uint32_t slot =
						offsets[value] + filled[value];
					lists[slot] = rule_idx;
					++filled[value];
				}
			}
		}
		if (pass == 1) {
			break;
		}
		uint64_t total = 0;
		for (uint32_t value = 0; value < value_space; ++value) {
			total += counts[value];
		}
		if (total > UINT32_MAX) {
			goto error_free;
		}
		offsets = (uint32_t *)calloc(value_space + 1, sizeof(uint32_t));
		filled = (uint32_t *)calloc(value_space, sizeof(uint32_t));
		lists = (uint32_t *)malloc(
			sizeof(uint32_t) * (total ? total : 1)
		);
		if (offsets == NULL || filled == NULL || lists == NULL) {
			goto error_free;
		}
		for (uint32_t value = 0; value < value_space; ++value) {
			offsets[value + 1] = offsets[value] + counts[value];
		}
	}

	if (vline_init(map, memory_context, "filter:compact", value_space)) {
		goto error_free;
	}

	// Hash-cons the sorted rule lists into the class ids.
	uint32_t tbl_cap = 4;
	while (tbl_cap < value_space * 2) {
		tbl_cap <<= 1;
	}
	uint64_t *keys = (uint64_t *)calloc(tbl_cap, sizeof(uint64_t));
	uint32_t *slot_rep = (uint32_t *)calloc(tbl_cap, sizeof(uint32_t));
	if (keys == NULL || slot_rep == NULL) {
		free(keys);
		free(slot_rep);
		goto error_free;
	}

	uint32_t new_count = 0;
	for (uint32_t value = 1; value < value_space; ++value) {
		if (counts[value] == 0) {
			continue;
		}
		const uint32_t *list = lists + offsets[value];
		uint32_t n = counts[value];

		uint64_t hash = 1469598103934665603ULL;
		for (uint32_t idx = 0; idx < n; ++idx) {
			hash ^= list[idx];
			hash *= 1099511628211ULL;
		}
		if (hash == 0) {
			hash = 1;
		}

		uint32_t slot = (uint32_t)hash & (tbl_cap - 1);
		uint32_t match = 0;
		while (keys[slot] != 0) {
			if (keys[slot] == hash) {
				uint32_t other = slot_rep[slot];
				if (counts[other] == n &&
				    memcmp(lists + offsets[other],
					   list,
					   n * sizeof(uint32_t)) == 0) {
					match = other;
					break;
				}
			}
			slot = (slot + 1) & (tbl_cap - 1);
		}
		if (match != 0) {
			*vline_get_ptr(map, value) = *vline_get_ptr(map, match);
			continue;
		}
		keys[slot] = hash;
		slot_rep[slot] = value;
		*vline_get_ptr(map, value) = ++new_count;
	}

	free(keys);
	free(slot_rep);
	free(lists);
	free(offsets);
	free(filled);
	free(counts);
	free(gen);
	return 0;

error_free:
	free(lists);
	free(offsets);
	free(filled);
	free(counts);
	free(gen);
	return -1;
}

/*
 * Join with the left classes compacted by a coverage map.
 *
 * The left registry is cloned with its range values mapped through
 * the map, the joint table spans the compacted classes (plus the
 * zero row for the uncovered rest), and the map is handed out for
 * the runtime class translation.
 */
int
classify_join_compact(
	struct memory_context *memory_context,
	const char *name,
	const struct classifier *left,
	const struct classifier *right,
	uint32_t rule_count,
	const struct vline *compact_map,
	struct value_table *table,
	struct classifier *out
) {
	const struct value_registry *registry1 = &left->registry;
	uint32_t group_count = registry1->range_count;

	struct classifier left_compact = {0};
	struct value_registry *compact_registry = &left_compact.registry;
	int rc = -1;

	uint32_t *compact_groups = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * (rule_count ? rule_count : 1)
	);
	if (compact_groups == NULL) {
		return -1;
	}
	memcpy(compact_groups, left->rule_groups, sizeof(uint32_t) * rule_count
	);
	left_compact.rule_groups = compact_groups;

	if (value_registry_init(
		    compact_registry, memory_context, "filter:compact"
	    )) {
		memory_bfree(
			memory_context,
			compact_groups,
			sizeof(uint32_t) * (rule_count ? rule_count : 1)
		);
		return -1;
	}

	const struct value_range *ranges = ADDR_OF(&registry1->ranges);
	for (uint32_t group_idx = 0; group_idx < group_count; ++group_idx) {
		if (value_registry_start(compact_registry)) {
			goto error_free_compact;
		}
		const uint32_t *values = ADDR_OF(&ranges[group_idx].values);
		for (uint32_t idx = 0; idx < ranges[group_idx].count; ++idx) {
			uint32_t compact_value = vline_get(
				(struct vline *)compact_map, values[idx]
			);
			if (compact_value == 0) {
				continue;
			}
			if (value_registry_collect(
				    compact_registry, compact_value
			    )) {
				goto error_free_compact;
			}
		}
	}

	if (classify_join(
		    memory_context,
		    name,
		    &left_compact,
		    right,
		    rule_count,
		    table,
		    out
	    )) {
		goto error_free_compact;
	}
	rc = 0;

error_free_compact:
	value_registry_fini(compact_registry);
	memory_bfree(
		memory_context,
		left_compact.rule_groups,
		sizeof(uint32_t) * (rule_count ? rule_count : 1)
	);
	return rc;
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
 * The name scopes the memory contexts of the join - the value table and
 * the stage registry - each titled with the name and its own artifact
 * leaf, so every context of one join of one classifier composition is
 * identified in the memory tree.
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
	const char *name,
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

	char table_name[MEMORY_CONTEXT_NAME_SIZE];
	classify_leaf_name(table_name, sizeof(table_name), name, "table");
	if (value_table_init(
		    table,
		    memory_context,
		    table_name,
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

	char registry_name[MEMORY_CONTEXT_NAME_SIZE];
	classify_leaf_name(
		registry_name, sizeof(registry_name), name, "registry"
	);
	if (value_registry_init(registry, memory_context, registry_name)) {
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
