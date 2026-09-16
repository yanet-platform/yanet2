#pragma once

#include "lib/filter2/compiler/attribute.h"
#include "lib/filter2/compiler/declare.h"
#include "lib/filter2/compiler/helper.h"
#include "lib/filter2/filter.h"

#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"
#include <assert.h>

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
 * Filter compilation routine.
 *
 * Filter logic implies the following logic:
 * - rules with the exact same attribute value form a group per attribute
 * - each attribute definition area is split into regions with unique set of
 *   group set corresponding to the region
 * - each group gets a list of region identifiers for the attribute
 * - combine pairs of attributes into sets of next level identifiers
 * - the last stage produces a final class identifier for each rule set
 *   combination and a map of the final class identifiers to rule indices
 *
 * So in case of 5 attributes compilation schema for a rule looks like
 * a0, a1, a2, a3, a4 -> sets of range identifiers matching to the rule
 * produce a5 = a0 * a1, a6 = a2 * a3, a7 = a4 * a5 where ai * aj if s full
 * join of containing values
 * after then each value of a6 * a7 is a final class identifier mapped to
 * a rule index.
 *
 * The logic consist of three stages
 * - initialize all the attributes and touch regions for all groups. The
 *   stage result in enumerate all regions with values defining of unique
 *   group set identifier. Also we collect region values for each group
 *   into registries along with the rule to group mappings.
 * - merge pairs of values into a new ones
 * - map the final class identifiers produced by the last merge to rule
 *   indices
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
	SET_OFFSET_OF(&filter->rule_map, NULL);

	/*
	 * A single attribute filter has no joints: its attribute registry
	 * is the final one and feeds the rule map directly.
	 */
	uint32_t joint_count = attr_handler_count - 1;
	uint32_t registry_count = attr_handler_count + joint_count;
	uint32_t rule_alloc_count = rule_count ? rule_count : 1;

	struct filter_query_attr **query_attrs =
		(struct filter_query_attr **)memory_balloc(
			memory_context,
			sizeof(struct filter_query_attr *) * attr_handler_count
		);
	if (query_attrs == NULL) {
		goto error;
	}
	memset(query_attrs,
	       0,
	       sizeof(struct filter_query_attr *) * attr_handler_count);
	SET_OFFSET_OF(&filter->attrs, query_attrs);

	struct value_registry *registries =
		(struct value_registry *)memory_balloc(
			memory_context,
			sizeof(struct value_registry) * registry_count
		);
	memset(registries, 0, sizeof(struct value_registry) * registry_count);

	/*
	 * Rule to group mappings, one per registry: attributes fill their
	 * own, the joints produce one per distinct group pair. The mapping
	 * of registry idx lives at rule_groups + idx * rule_alloc_count.
	 */
	uint32_t *rule_groups = (uint32_t *)memory_balloc(
		memory_context,
		sizeof(uint32_t) * registry_count * rule_alloc_count
	);
	if (rule_groups == NULL) {
		goto error_free_registries;
	}

	struct value_table *joints = NULL;
	if (joint_count > 0) {
		joints = (struct value_table *)memory_balloc(
			memory_context, sizeof(struct value_table) * joint_count
		);
		if (joints == NULL) {
			goto error_free_attrs;
		}
		memset(joints, 0, sizeof(struct value_table) * joint_count);
	}
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
				rule_groups + attr_idx * rule_alloc_count,
				memory_context
			);
		if (query_attr == NULL) {
			goto error_free_attrs;
		}
		SET_OFFSET_OF(query_attrs + attr_idx, query_attr);
	}

	for (uint32_t joint_idx = 0; joint_idx < joint_count; ++joint_idx) {
		/*
		 * Join the rule values pair and produce a new set of
		 * derivative values
		 */
		if (merge_and_collect_registry(
			    memory_context,
			    registries + joint_idx * 2,
			    rule_groups + joint_idx * 2 * rule_alloc_count,
			    registries + joint_idx * 2 + 1,
			    rule_groups +
				    (joint_idx * 2 + 1) * rule_alloc_count,
			    rule_count,
			    joints + joint_idx,
			    registries + attr_handler_count + joint_idx,
			    rule_groups + (attr_handler_count + joint_idx) *
						  rule_alloc_count
		    )) {
			goto error_free_attrs;
		}
	}

	/*
	 * The final registry - the last joint output, or the attribute
	 * registry itself for a single attribute filter - holds the final
	 * class identifiers; the map translates them into rule indices.
	 */
	struct vline *rule_map = (struct vline *)memory_balloc(
		memory_context, sizeof(struct vline)
	);
	if (rule_map == NULL) {
		goto error_free_attrs;
	}
	SET_OFFSET_OF(&filter->rule_map, rule_map);

	if (collect_rule_map(
		    memory_context,
		    registries + registry_count - 1,
		    rule_groups + (registry_count - 1) * rule_alloc_count,
		    rule_count,
		    rule_map
	    )) {
		memory_bfree(memory_context, rule_map, sizeof(struct vline));
		SET_OFFSET_OF(&filter->rule_map, NULL);
		goto error_free_attrs;
	}

	for (uint32_t idx = 0; idx < registry_count; ++idx) {
		value_registry_fini(registries + idx);
	}
	memory_bfree(
		memory_context,
		rule_groups,
		sizeof(uint32_t) * registry_count * rule_alloc_count
	);

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
		if (query_attr == NULL) {
			continue;
		}
		attr_handlers[attr_idx]->free_query(memory_context, query_attr);
	}

	for (uint32_t joint_idx = 0; joint_idx < joint_count; ++joint_idx) {
		value_table_free(joints + joint_idx);
	}

	memory_bfree(
		memory_context, joints, sizeof(struct value_table) * joint_count
	);

	memory_bfree(
		memory_context,
		rule_groups,
		sizeof(uint32_t) * registry_count * rule_alloc_count
	);

error_free_registries:
	for (uint32_t idx = 0; idx < registry_count; ++idx) {
		value_registry_fini(registries + idx);
	}
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
			if (query_attr == NULL) {
				continue;
			}
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

	struct vline *rule_map = ADDR_OF(&filter->rule_map);
	if (rule_map != NULL) {
		vline_free(rule_map);
		memory_bfree(memory_context, rule_map, sizeof(struct vline));
	}

	// Unlink the embedded context from the parent tree before the storage
	// goes away: a dangling child link would corrupt later tree walks.
	memory_context_fini(memory_context);
	memset(filter, 0, sizeof(struct filter));
}

#define filter_init(filter, sign, rules, count, mctx)                          \
	filter_compile(                                                        \
		filter, mctx, rules, count, sign, sizeof(sign) / sizeof(*sign) \
	)

#define filter_free(filter, sign)                                              \
	filter_destroy(filter, sign, sizeof(sign) / sizeof(*sign))
