#pragma once

#include "filter/compiler/attribute.h"
#include "filter/compiler/declare.h"
#include "filter/compiler/helper.h"
#include "filter/filter.h"

#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"
#include <assert.h>

static inline int
filter_set_cb(uint32_t *value, void *data) {
	uint32_t *rule_idx = (uint32_t *)data;
	*value = *rule_idx;
	return 0;
}

static inline int
filter_set_rule_cb(uint32_t *value, void *data) {
	if (*value != FILTER_RULE_INVALID)
		return 0;

	uint32_t *rule_idx = (uint32_t *)data;
	*value = *rule_idx;
	return 0;
}

static inline int
filter_remap_cb(uint32_t *value, void *data) {
	struct remap_table *remap_table = (struct remap_table *)data;
	return remap_table_touch(remap_table, *value, value);
}

static inline int
filter_compact_cb(uint32_t *value, void *data) {
	struct remap_table *remap_table = (struct remap_table *)data;
	*value = remap_table_compacted(remap_table, *value);
	return 0;
}

static inline int
filter_collect_cb(uint32_t *value, void *data) {
	struct value_registry *registry = (struct value_registry *)data;
	return value_registry_collect(registry, *value);
}

/*
 * Special handler for single attribute filter, the case implies the attribute
 * range lookup returns the rule index instead of identifier of a rule
 * combination.
 */
static inline int
filter_compile_single_attr(
	struct filter *filter,
	struct memory_context *memory_context,
	const struct filter_rule **rules,
	uint32_t rule_count,
	const struct filter_compile_attr_handlers *attr_handlers
) {
	struct filter_query_attr **query_attrs =
		(struct filter_query_attr **)memory_balloc(
			memory_context, sizeof(struct filter_query_attr *)
		);
	if (query_attrs == NULL)
		return -1;
	memset(query_attrs, 0, sizeof(struct filter_query_attr *));
	SET_OFFSET_OF(&filter->attrs, query_attrs);

	struct filter_compile_attr *attr = attr_handlers->create(
		memory_context, attr_handlers, rules, rule_count
	);

	if (attr == NULL) {
		goto error_free_attrs;
	}

	/*
	 * Set default value for all existing ranges in the attribute
	 * definition are.
	 */
	uint32_t invalid = FILTER_RULE_INVALID;
	attr_handlers->iter(attr, attr_handlers, filter_set_cb, &invalid);

	/*
	 * Iterate over all rules and set rule index for each corresponding
	 * to a rule regions.
	 */
	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		const struct filter_rule *rule = rules[rule_idx];
		if (rule == NULL)
			continue;

		attr_handlers->rule_iter(
			attr, attr_handlers, rule, filter_set_rule_cb, &rule_idx
		);
	}

	struct filter_query_attr *query_attr =
		attr_handlers->commit(memory_context, attr);

	if (query_attr == NULL) {
		attr_handlers->free_compile(memory_context, attr);
		goto error_free_attrs;
	}
	SET_OFFSET_OF(query_attrs, query_attr);

	return 0;

error_free_attrs:
	memory_bfree(
		memory_context, query_attrs, sizeof(struct filter_query_attr *)
	);
	SET_OFFSET_OF(&filter->attrs, NULL);

	return -1;
}

/*
 * Filter compilation routine.
 *
 * Filter logic implies the following logic:
 * - each attribute definition area is split into regions with unique set of
 *   rule set corresponding to the region
 * - each rule gets a list of region identifiers for each attribute
 * - combine pairs of attributes into sets of next level identifiers
 * - the last stage resolves corresponding rule index
 *
 * So in case of 5 attributes compilation schema for a rule looks like
 * a0, a1, a2, a3, a4 -> sets of range identifiers matching to the rule
 * produce a5 = a0 * a1, a6 = a2 * a3, a7 = a4 * a5 where ai * aj if s full
 * join of containing values
 * after then we set rule index for each pair in set of a6 * a7.
 *
 * The logic consist of three stages
 * - initialize all the attributes and touch regions for all rules. The stage
 *   result in enumerate all regions with values defining of unique rule set
 *   identifier. Also we collect region values for each rule into registries.
 * - merge pairs of values into a new ones
 * - after only two set of identifiers remained - the last one merge will set
 *   a rule index into corresponding table.
 */

static inline int
filter_compile(
	struct filter *filter,
	struct memory_context *memory_context,
	const struct filter_rule **rules,
	uint32_t rule_count,
	const struct filter_compile_attr_handlers *attr_handlers[],
	uint32_t attr_handler_count

) {
	if (memory_context_init_from(
		    &filter->memory_context, memory_context, "filter"
	    )) {
		return -1;
	}
	memory_context = &filter->memory_context;

	SET_OFFSET_OF(&filter->attrs, NULL);
	SET_OFFSET_OF(&filter->joints, NULL);

	if (attr_handler_count == 1)
		return filter_compile_single_attr(
			filter,
			memory_context,
			rules,
			rule_count,
			attr_handlers[0]
		);

	struct filter_query_attr **query_attrs =
		(struct filter_query_attr **)memory_balloc(
			memory_context,
			sizeof(struct filter_query_attr *) * attr_handler_count
		);
	if (query_attrs == NULL)
		goto error;
	memset(query_attrs,
	       0,
	       sizeof(struct filter_query_attr *) * attr_handler_count);
	SET_OFFSET_OF(&filter->attrs, query_attrs);

	uint32_t joint_count = attr_handler_count - 1;
	uint32_t registry_count = attr_handler_count + joint_count - 1;
	struct value_registry *registries =
		(struct value_registry *)memory_balloc(
			memory_context,
			sizeof(struct value_registry) * registry_count
		);
	memset(registries, 0, sizeof(struct value_registry) * registry_count);

	/*
	 * Initialize all registries.
	 */
	for (uint32_t idx = 0; idx < registry_count; ++idx) {
		if (value_registry_init(registries + idx, memory_context)) {
			goto error_free_registries;
		}
	}

	struct value_table *joints = (struct value_table *)memory_balloc(
		memory_context, sizeof(struct value_table) * joint_count
	);
	if (joints == NULL) {
		goto error_free_attrs;
	}
	memset(joints, 0, sizeof(struct value_table) * joint_count);
	SET_OFFSET_OF(&filter->joints, joints);

	/*
	 * Process all the attributes assigned to the filter.
	 */
	for (uint32_t attr_idx = 0; attr_idx < attr_handler_count; ++attr_idx) {
		struct filter_query_attr *query_attr =
			filter_compile_attr_build(
				attr_handlers[attr_idx],
				registries + attr_idx,
				rules,
				rule_count,
				memory_context
			);
		if (query_attr == NULL) {
			goto error_free_attrs;
		}
		SET_OFFSET_OF(query_attrs + attr_idx, query_attr);
	}

	for (uint32_t joint_idx = 0; joint_idx < joint_count; ++joint_idx) {
		if (joint_idx < joint_count - 1) {
			/*
			 * Join the rule values pair and produce a new set
			 * of derivative values
			 */
			if (merge_and_collect_registry(
				    memory_context,
				    registries + joint_idx * 2,
				    registries + joint_idx * 2 + 1,
				    joints + joint_idx,
				    registries + attr_handler_count + joint_idx
			    )) {
				goto error_free_attrs;
			}
		} else {
			/*
			 * The last stage - set rule index for the last one
			 * pair of values.
			 */
			if (merge_and_set_registry_values(
				    memory_context,
				    registries + joint_idx * 2,
				    registries + joint_idx * 2 + 1,
				    joints + joint_idx
			    )) {
				goto error_free_attrs;
			}
		}
	}

	for (uint32_t idx = 0; idx < registry_count; ++idx)
		value_registry_free(registries + idx);

	memory_bfree(
		memory_context,
		registries,
		sizeof(struct value_registry) * registry_count
	);

	return 0;

error_free_attrs:
	for (uint32_t attr_idx = 0; attr_idx < attr_handler_count; ++attr_idx) {
		struct filter_query_attr *query_attr =
			ADDR_OF(query_attrs + attr_idx);
		if (query_attr == NULL)
			continue;
		attr_handlers[attr_idx]->free_query(memory_context, query_attr);
	}

	for (uint32_t joint_idx = 0; joint_idx < joint_count; ++joint_idx) {
		value_table_free(joints + joint_idx);
	}

	memory_bfree(
		memory_context, joints, sizeof(struct value_table) * joint_count
	);

error_free_registries:
	for (uint32_t idx = 0; idx < registry_count; ++idx)
		value_registry_free(registries + idx);
	memory_bfree(
		memory_context,
		registries,
		sizeof(struct value_registry) * registry_count
	);

	SET_OFFSET_OF(&filter->attrs, NULL);
	memory_bfree(
		memory_context,
		query_attrs,
		sizeof(struct filter_query_attr *) * attr_handler_count
	);

error:
	SET_OFFSET_OF(&filter->joints, NULL);

	return -1;
}

static inline void
filter_destroy(
	struct filter *filter,
	const struct filter_compile_attr_handlers *attr_handlers[],
	uint32_t attr_handler_count
) {
	struct memory_context *memory_context = &filter->memory_context;

	struct filter_query_attr **query_attrs = ADDR_OF(&filter->attrs);

	if (query_attrs != NULL) {
		for (uint32_t attr_idx = 0; attr_idx < attr_handler_count;
		     ++attr_idx) {
			struct filter_query_attr *query_attr =
				ADDR_OF(query_attrs + attr_idx);
			if (query_attr == NULL)
				continue;
			attr_handlers[attr_idx]->free_query(
				memory_context, query_attr
			);
		}
		memory_bfree(
			memory_context,
			query_attrs,
			sizeof(struct filter_query_attr *) * attr_handler_count
		);
	}

	uint32_t joint_count = attr_handler_count - 1;
	struct value_table *joints = ADDR_OF(&filter->joints);
	if (joints != NULL) {
		for (uint32_t joint_idx = 0; joint_idx < joint_count;
		     ++joint_idx) {
			value_table_free(joints + joint_idx);
		}
		memory_bfree(
			memory_context,
			joints,
			sizeof(struct value_table) * joint_count
		);
	}
	memset(filter, 0, sizeof(struct filter));
}

#define filter_init(filter, sign, rules, count, mctx)                          \
	filter_compile(                                                        \
		filter, mctx, rules, count, sign, sizeof(sign) / sizeof(*sign) \
	)

#define filter_free(filter, sign)                                              \
	filter_destroy(filter, sign, sizeof(sign) / sizeof(*sign))
