#pragma once

#include "common/hash_index.h"
#include "common/registry.h"
#include "common/remap.h"

#include "common/for_each.h"

#include "lib/filter2/filter.h"

#define FILTER_ATTR_COMPILE(name) &filter_compile_attr_##name.attr_handlers

#define FILTER_COMPILER_DECLARE(tag, ...)                                      \
	static const struct filter_compile_attr_handlers *tag[] = {            \
		FOR_EACH(FILTER_ATTR_COMPILE, __VA_ARGS__),                    \
	};

/*
 * There are following key concepts of filter compilation:
 *  - filter consists of a set of attributes each attribute is responsible
 *    for one of rule conditions - source network, protocol, vlan, e.t.c.
 *  - each attribute has some definition area where it works on
 *  - attribute splits the definition ares into regions with the following
 *    requirements:
 *    - all points of a region have the same set of rules matching
 *    - each region has it's own identifier (value)
 *    to the point as any other point of the region
 *  - attribute provides following interfaces:
 *    - size retrieves count of regions known to the attribute
 *    - iterate over the whole definition area
 *    - iterate over a rule matching regions
 *    - check if a rule covers the whole definition area - used in sake of
 *      efficiency
 *    - commit produces a classifier suitable for obtaining matching region
 *      value for a packet. Commit detaches the classifier from attribute
 *      and frees resources assigned to the attribute in case of success
 *    - free releases all associated resources and should not be called in
 *      case of commit success
 */

/*
 * Filter rule forward declaration.
 *
 * The structure is used to obtain specific rule attribute like network,
 * port range or protocol.
 */
struct filter_rule;

/*
 * Compile attribute forward declaration - compilation API handler.
 *
 * The structure used to store attribute specific temporary compilation data.
 */
struct filter_compile_attr {};
/*
 * Compilation attribute virtual table forward declaration.
 *
 * The structure also used as compilation API handler.
 */
struct filter_compile_attr_handlers;

/*
 * Attribute creation routine builds a set of regions for provided ruleset
 * and initializes each region value to zero.
 */
typedef struct filter_compile_attr *(*filter_compile_attr_create_func)(
	struct memory_context *memory_context,
	const struct filter_compile_attr_handlers *handlers,
	const struct filter_rule **rules,
	uint32_t rule_count
);

/*
 * Retrieves count of regions of an attribute
 */
typedef uint32_t (*filter_compile_attr_size_func)(
	const struct filter_compile_attr *attr
);

/*
 * Checks if a rule covers the whole definition area
 */
typedef int (*filter_compile_attr_rule_is_any_func)(
	const struct filter_compile_attr *attr,
	const struct filter_compile_attr_handlers *handlers,
	const struct filter_rule *rule
);

/*
 * Hash of the rule attribute value - the part of the rule the attribute
 * is responsible for. Rules with equal values fall into the same group.
 */
typedef uint32_t (*filter_compile_attr_rule_hash_func)(
	const struct filter_compile_attr_handlers *handlers,
	const struct filter_rule *rule
);

/*
 * Compares the attribute values of two rules, zero means the values are
 * the same and the rules belong to the same group.
 */
typedef int (*filter_compile_attr_rule_compare_func)(
	const struct filter_compile_attr_handlers *handlers,
	const struct filter_rule *first,
	const struct filter_rule *second
);

/*
 * A callback with pointer to a region value and associated custom pointer
 */
typedef int (*filter_compile_attr_iter_cb_func)(uint32_t *value, void *data);

/*
 * Iterates over an attribute definition area with a callback for the each
 * region value
 */
typedef int (*filter_compile_attr_iter_func)(
	struct filter_compile_attr *attr,
	const struct filter_compile_attr_handlers *handlers,
	filter_compile_attr_iter_cb_func iter_cb_func,
	void *cb_func_data
);

/*
 * Iterates over a rule regions with a callback for the each region value
 */
typedef int (*filter_compile_attr_rule_iter_func)(
	struct filter_compile_attr *attr,
	const struct filter_compile_attr_handlers *handlers,
	const struct filter_rule *rule,
	filter_compile_attr_iter_cb_func iter_cb_func,
	void *cb_func_data
);

/*
 * Produces a classifier and frees the attribute in case of success
 */
typedef struct filter_query_attr *(*filter_compile_attr_commit_func)(
	struct memory_context *memory_context, struct filter_compile_attr *attr
);

/*
 * Frees attribute compilation data
 */
typedef void (*filter_compile_attr_free_compile_func)(
	struct memory_context *memory_context, struct filter_compile_attr *attr
);

/*
 * Frees attribute classifier data
 */
typedef void (*filter_compile_attr_free_query_func)(
	struct memory_context *memory_context, struct filter_query_attr *attr
);

/*
 * Compilation attribute virtual table. The table may be incorporated into
 * a structure providing additional routines, for example obtaining specific
 * conditions - networks, protocols and etc
 */
struct filter_compile_attr_handlers {
	filter_compile_attr_create_func create;
	filter_compile_attr_size_func size;
	filter_compile_attr_rule_is_any_func rule_is_any;
	filter_compile_attr_rule_hash_func hash;
	filter_compile_attr_rule_compare_func compare;
	filter_compile_attr_rule_iter_func rule_iter;
	filter_compile_attr_iter_func iter;
	filter_compile_attr_commit_func commit;
	filter_compile_attr_free_compile_func free_compile;
	filter_compile_attr_free_query_func free_query;
};

static inline int
filter_compile_attr_touch(uint32_t *value, void *data) {
	struct remap_table *remap_table = (struct remap_table *)data;
	return remap_table_touch(remap_table, *value, value);
}

static inline int
filter_compile_attr_collect(uint32_t *value, void *data) {
	struct value_registry *value_registry = (struct value_registry *)data;
	return value_registry_collect(value_registry, *value);
}

static inline int
filter_compile_attr_compact(uint32_t *value, void *data) {
	struct remap_table *remap_table = (struct remap_table *)data;
	*value = remap_table_compacted(remap_table, *value);
	return 0;
}

struct filter_compile_attr_group_ctx {
	const struct filter_compile_attr_handlers *attr_handlers;
	const struct filter_rule **rules;
	const uint32_t *group_first_rule;
	const struct filter_rule *rule;
};

static inline int
filter_compile_attr_group_eq(uint32_t value, const void *data) {
	const struct filter_compile_attr_group_ctx *group_ctx =
		(const struct filter_compile_attr_group_ctx *)data;
	return group_ctx->attr_handlers->compare(
		group_ctx->attr_handlers,
		group_ctx->rules[group_ctx->group_first_rule[value]],
		group_ctx->rule
	);
}

/*
 * Builds the attribute classifier for the provided ruleset.
 *
 * Rules holding the exact same attribute value are grouped together, so
 * the region enumeration and the registry ranges operate on distinct
 * values instead of all rules. On success the routine returns the
 * classifier, initializes the registry with one range per group holding
 * the group matching region values, and fills the rule to group mapping;
 * a rule without a value (a NULL rule) keeps the invalid group mark.
 */
static inline struct filter_query_attr *
filter_compile_attr_build(
	const struct filter_compile_attr_handlers *attr_handlers,
	struct value_registry *registry,
	const struct filter_rule **rules,
	uint32_t rule_count,
	uint32_t *rule_to_group,
	struct memory_context *memory_context
) {
	/*
	 * Splits the definition area int regions and initialize all of them
	 * with zero values.
	 */
	struct filter_compile_attr *attr = attr_handlers->create(
		memory_context, attr_handlers, rules, rule_count
	);
	if (attr == NULL) {
		return NULL;
	}

	/*
	 * Collect the distinct attribute values into groups: the first rule
	 * a group was seen at and a hash index over group indices.
	 */
	uint32_t group_alloc_count = rule_count ? rule_count : 1;
	uint32_t *group_first_rule = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * group_alloc_count
	);
	if (group_first_rule == NULL) {
		goto error_free_attr;
	}

	struct hash_index group_index;
	if (hash_index_init(&group_index, memory_context, rule_count)) {
		goto error_free_group_first_rule;
	}

	struct filter_compile_attr_group_ctx group_ctx = {
		attr_handlers,
		rules,
		group_first_rule,
		NULL,
	};

	uint32_t group_count = 0;
	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		rule_to_group[rule_idx] = FILTER_GROUP_INVALID;

		const struct filter_rule *rule = rules[rule_idx];
		if (rule == NULL) {
			continue;
		}

		group_ctx.rule = rule;
		uint32_t hash = attr_handlers->hash(attr_handlers, rule);
		uint32_t group = hash_index_lookup(
			&group_index,
			hash,
			filter_compile_attr_group_eq,
			&group_ctx
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
		    &remap_table, memory_context, attr_handlers->size(attr)
	    )) {
		goto error;
	}

	for (uint32_t group_idx = 0; group_idx < group_count; ++group_idx) {
		const struct filter_rule *rule =
			rules[group_first_rule[group_idx]];
		/*
		 * If a group covers the whole area there is no meaning to
		 * touch values - all of them just gain update without any
		 * distinction result.
		 */
		if (attr_handlers->rule_is_any(attr, attr_handlers, rule)) {
			continue;
		}

		remap_table_new_gen(&remap_table);

		// Touch the group matching values
		if (attr_handlers->rule_iter(
			    attr,
			    attr_handlers,
			    rule,
			    filter_compile_attr_touch,
			    &remap_table
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

	if (attr_handlers->iter(
		    attr,
		    attr_handlers,
		    filter_compile_attr_compact,
		    &remap_table
	    )) {
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

		const struct filter_rule *rule =
			rules[group_first_rule[group_idx]];
		if (attr_handlers->rule_is_any(attr, attr_handlers, rule)) {
			if (attr_handlers->iter(
				    attr,
				    attr_handlers,
				    filter_compile_attr_collect,
				    registry
			    )) {
				goto error;
			}

		} else {
			if (attr_handlers->rule_iter(
				    attr,
				    attr_handlers,
				    rule,
				    filter_compile_attr_collect,
				    registry
			    )) {
				goto error;
			}
		}
	}

	struct filter_query_attr *query_attr =
		attr_handlers->commit(memory_context, attr);
	if (query_attr == NULL) {
		goto error;
	}

	hash_index_fini(&group_index);
	memory_bfree(
		memory_context,
		group_first_rule,
		sizeof(uint32_t) * group_alloc_count
	);

	return query_attr;

error:
	value_registry_fini(registry);
	memset(registry, 0, sizeof(*registry));
	hash_index_fini(&group_index);

error_free_group_first_rule:
	memory_bfree(
		memory_context,
		group_first_rule,
		sizeof(uint32_t) * group_alloc_count
	);

error_free_attr:
	attr_handlers->free_compile(memory_context, attr);
	return NULL;
}
