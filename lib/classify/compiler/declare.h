#pragma once

#include "common/hash_index.h"
#include "common/registry.h"
#include "common/remap.h"

#include "lib/classify/classify.h"
#include "lib/classify/rule.h"

/*
 * Compilation contracts shared by every attribute variant.
 *
 * The library is rule format agnostic: a ruleset is an array of module
 * defined rules, and every attribute variant stores the module authored
 * getter of its values in its compile scratch, so the callbacks below
 * resolve a rule through the scratch without knowing its type. Each
 * attribute is responsible for one rule condition - source network,
 * protocol, vlan, e.t.c. Each attribute has some definition area where
 * it works on, and splits the definition area into regions with the
 * following requirements:
 *  - all points of a region have the same set of rules matching
 *  - each region has its own identifier (value)
 *
 * An attribute variant provides its callbacks as an ops table to the
 * shared compile driver below:
 *  - size retrieves the count of regions known to the attribute
 *  - iterate over the whole definition area, or over the regions a
 *    rule matches
 *  - check if a rule covers the whole definition area - used for the
 *    sake of efficiency
 *  - commit fills the consumer's embedded classifier and frees the
 *    compile scratch in case of success
 *  - free_compile releases all compile scratch and is not called
 *    after a successful commit
 */

/*
 * A ruleset: an array of module defined rules, where a NULL entry is
 * absent from the projection the array spells.
 */

struct classify_attr_ops {
	uint32_t (*size)(const void *compile);
	int (*rule_is_any)(
		const void *compile, const struct classifier_rule *rule
	);
	uint32_t (*hash)(
		const void *compile, const struct classifier_rule *rule
	);
	int (*compare)(
		const void *compile,
		const struct classifier_rule *first,
		const struct classifier_rule *second
	);
	int (*iter)(
		void *compile,
		int (*iter_cb_func)(uint32_t *value, void *data),
		void *cb_func_data
	);
	int (*rule_iter)(
		void *compile,
		const struct classifier_rule *rule,
		int (*iter_cb_func)(uint32_t *value, void *data),
		void *cb_func_data
	);
	// Fills the consumer's embedded classifier and frees the compile
	// scratch on success; on failure the scratch is freed by the
	// driver through free_compile below.
	int (*commit)(
		struct memory_context *memory_context, void *compile, void *attr
	);
	void (*free_compile)(
		struct memory_context *memory_context, void *compile
	);
};

static inline int
classify_attr_touch(uint32_t *value, void *data) {
	struct remap_table *remap_table = (struct remap_table *)data;
	return remap_table_touch(remap_table, *value, value);
}

static inline int
classify_attr_collect(uint32_t *value, void *data) {
	struct value_registry *value_registry = (struct value_registry *)data;
	return value_registry_collect(value_registry, *value);
}

static inline int
classify_attr_compact(uint32_t *value, void *data) {
	struct remap_table *remap_table = (struct remap_table *)data;
	*value = remap_table_compacted(remap_table, *value);
	return 0;
}

struct classify_attr_group_ctx {
	const struct classify_attr_ops *ops;
	const void *compile;
	const struct classifier_rule *const *rules;
	const uint32_t *group_first_rule;
	const struct classifier_rule *rule;
};

static inline int
classify_attr_group_eq(uint32_t value, const void *data) {
	const struct classify_attr_group_ctx *group_ctx =
		(const struct classify_attr_group_ctx *)data;
	return group_ctx->ops->compare(
		group_ctx->compile,
		group_ctx->rules[group_ctx->group_first_rule[value]],
		group_ctx->rule
	);
}

// Initializes a stage: the registry arrives zeroed, the rule group
// row is a standalone allocation.
static inline int
classifier_init(
	struct classifier *cls,
	struct memory_context *memory_context,
	uint32_t rule_count
) {
	memset(&cls->registry, 0, sizeof(cls->registry));

	uint32_t rule_alloc = rule_count ? rule_count : 1;
	cls->rule_groups = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * rule_alloc
	);
	return cls->rule_groups == NULL ? -1 : 0;
}

/*
 * Runs the shared region enumeration of an attribute over the ruleset.
 *
 * Rules holding the exact same attribute value are grouped together,
 * so the region enumeration and the registry ranges operate on
 * distinct values instead of all rules. On success the routine fills
 * the consumer's embedded classifier through the commit callback and
 * the stage of the attribute: the registry holds one range per group
 * with the group matching region values, and the rule group row maps
 * every rule to its group - a rule without a value (a NULL rule)
 * keeps the invalid group mark. On failure every partial state is
 * freed, the embedded classifier and the stage are left zeroed.
 */
static inline int
classify_attr_compile(
	struct memory_context *memory_context,
	const struct classify_attr_ops *ops,
	void *compile,
	const struct classifier_rule *const *rules,
	uint32_t rule_count,
	void *attr,
	struct classifier *cls
) {
	struct value_registry *registry = &cls->registry;

	if (classifier_init(cls, memory_context, rule_count)) {
		goto error_free_compile;
	}
	uint32_t *rule_to_group = cls->rule_groups;
	/*
	 * Collect the distinct attribute values into groups: the first rule
	 * a group was seen at and a hash index over group indices.
	 */
	uint32_t group_alloc_count = rule_count ? rule_count : 1;
	uint32_t *group_first_rule = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * group_alloc_count
	);
	if (group_first_rule == NULL) {
		goto error_free_compile;
	}

	struct hash_index group_index;
	if (hash_index_init(&group_index, memory_context, rule_count)) {
		goto error_free_group_first_rule;
	}

	struct classify_attr_group_ctx group_ctx = {
		ops,
		compile,
		rules,
		group_first_rule,
		NULL,
	};

	uint32_t group_count = 0;
	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		rule_to_group[rule_idx] = FILTER_GROUP_INVALID;

		const struct classifier_rule *rule = rules[rule_idx];
		if (rule == NULL) {
			continue;
		}

		group_ctx.rule = rule;
		uint32_t hash = ops->hash(compile, rule);
		uint32_t group = hash_index_lookup(
			&group_index, hash, classify_attr_group_eq, &group_ctx
		);
		if (group == HASH_INDEX_INVALID) {
			group = group_count;
			if (hash_index_insert(&group_index, hash, group)) {
				goto error;
			}
			group_first_rule[group] = rule_idx;
			++group_count;
		}

		rule_to_group[rule_idx] = group;
	}

	/*
	 * `remap_table is used to enumerate regions - each group touch its
	 * regions so each region gains a value specific to matching set of
	 * groups.
	 */
	struct remap_table remap_table;
	if (remap_table_init(
		    &remap_table, memory_context, ops->size(compile)
	    )) {
		goto error;
	}

	for (uint32_t group_idx = 0; group_idx < group_count; ++group_idx) {
		const struct classifier_rule *rule =
			rules[group_first_rule[group_idx]];
		/*
		 * If a group covers the whole area there is no meaning to
		 * touch values - all of them just gain update without any
		 * distinction result.
		 */
		if (ops->rule_is_any(compile, rule)) {
			continue;
		}

		remap_table_new_gen(&remap_table);

		// Touch the group matching values
		if (ops->rule_iter(
			    compile, rule, classify_attr_touch, &remap_table
		    )) {
			remap_table_free(&remap_table);
			goto error;
		}
	}

	/*
	 * Remap table compaction removes gaps of unused values and makes the
	 * resulting set of values smaller
	 */

	remap_table_compact(&remap_table);

	/*
	 * Now reassign compacted values to each region defined for the
	 * attribute.
	 */

	if (ops->iter(compile, classify_attr_compact, &remap_table)) {
		remap_table_free(&remap_table);
		goto error;
	}

	remap_table_free(&remap_table);

	if (value_registry_init(registry, memory_context, "filter:registry")) {
		goto error;
	}

	/*
	 * Collect the group matching values into the registry - there are
	 * two options:
	 *  - collect all known values in case if the group matches any
	 *  - collect the group specific values
	 */
	for (uint32_t group_idx = 0; group_idx < group_count; ++group_idx) {
		if (value_registry_start(registry)) {
			goto error;
		}

		const struct classifier_rule *rule =
			rules[group_first_rule[group_idx]];
		if (ops->rule_is_any(compile, rule)) {
			if (ops->iter(
				    compile, classify_attr_collect, registry
			    )) {
				goto error;
			}

		} else {
			if (ops->rule_iter(
				    compile,
				    rule,
				    classify_attr_collect,
				    registry
			    )) {
				goto error;
			}
		}
	}

	if (ops->commit(memory_context, compile, attr)) {
		goto error;
	}

	hash_index_fini(&group_index);
	memory_bfree(
		memory_context,
		group_first_rule,
		sizeof(uint32_t) * group_alloc_count
	);

	return 0;

error:
	value_registry_fini(registry);
	memset(registry, 0, sizeof(*registry));
	memory_bfree(
		memory_context,
		cls->rule_groups,
		sizeof(uint32_t) * (rule_count ? rule_count : 1)
	);
	cls->rule_groups = NULL;
	hash_index_fini(&group_index);

error_free_group_first_rule:
	memory_bfree(
		memory_context,
		group_first_rule,
		sizeof(uint32_t) * group_alloc_count
	);

error_free_compile:
	ops->free_compile(memory_context, compile);
	return -1;
}
