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
	SET_OFFSET_OF(&filter->joint_sides, NULL);
	SET_OFFSET_OF(&filter->rule_map, NULL);
	filter->shared_attr_count = 0;
	filter->shared_joint_count = 0;

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
	uint32_t *joint_sides = NULL;
	if (joint_count > 0) {
		joints = (struct value_table *)memory_balloc(
			memory_context, sizeof(struct value_table) * joint_count
		);
		if (joints == NULL) {
			goto error_free_attrs;
		}
		memset(joints, 0, sizeof(struct value_table) * joint_count);

		joint_sides = (uint32_t *)memory_balloc(
			memory_context, sizeof(uint32_t) * joint_count * 2
		);
		if (joint_sides == NULL) {
			memory_bfree(
				memory_context,
				joints,
				sizeof(struct value_table) * joint_count
			);
			goto error_free_attrs;
		}
		for (uint32_t joint_idx = 0; joint_idx < joint_count;
		     ++joint_idx) {
			joint_sides[joint_idx * 2] = joint_idx * 2;
			joint_sides[joint_idx * 2 + 1] = joint_idx * 2 + 1;
		}
	}
	SET_OFFSET_OF(&filter->joints, joints);
	SET_OFFSET_OF(&filter->joint_sides, joint_sides);

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
		    rules,
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
		memory_context, joint_sides, sizeof(uint32_t) * joint_count * 2
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
		for (uint32_t attr_idx = filter->shared_attr_count;
		     attr_idx < attr_handler_count;
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
		for (uint32_t joint_idx = filter->shared_joint_count;
		     joint_idx < joint_count;
		     ++joint_idx) {
			value_table_free(joints + joint_idx);
		}
		memory_bfree(
			memory_context,
			joints,
			sizeof(struct value_table) * joint_count
		);
	}

	uint32_t *joint_sides = ADDR_OF(&filter->joint_sides);
	if (joint_sides != NULL) {
		memory_bfree(
			memory_context,
			joint_sides,
			sizeof(uint32_t) * joint_count * 2
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

/*
 * Shared classification core.
 *
 * Filters sharing their leading attributes - the acl ip6 and ip6 port
 * filters share the network core of device, vlan, both net6 halves and
 * the protocol - compile the core once and derive every filter from
 * it: the expensive classifiers and join tables are built a single
 * time and every derived filter reads them.
 *
 * The core is compiled over the union ruleset of its derived filters.
 * A derivation passes its own projection of that ruleset - an array of
 * the same length holding NULL for every rule absent from the filter -
 * and receives a filter whose extra attributes, their joins and the
 * rule map belong to the projection alone. A derivation without extra
 * attributes is the core plus a mapping, a derivation with extras
 * joins every extra attribute onto the running result of the core.
 * Derived filters must be destroyed before the core they borrow from.
 */
struct filter_core {
	struct filter_query_attr **attrs;
	struct value_table *joints;
	// The registry chain of the core build, kept live as a whole: the
	// derivations join the core joint outputs, so the ranges of the
	// whole chain feed the derived joins until the core goes away.
	struct value_registry *registries;
	uint32_t *rule_groups;

	const struct filter_rule **rules;
	uint32_t rule_count;
	uint32_t rule_alloc_count;
	uint32_t attr_count;

	struct memory_context memory_context;
};

static inline int
filter_core_compile(
	struct filter_core *core,
	struct memory_context *memory_context,
	const struct filter_rule **rules,
	uint32_t rule_count,
	const struct filter_compile_attr_handlers *attr_handlers[],
	uint32_t attr_handler_count
) {
	if (memory_context_init_from(
		    &core->memory_context, memory_context, "filter:core"
	    )) {
		return -1;
	}
	memory_context = &core->memory_context;

	SET_OFFSET_OF(&core->attrs, NULL);
	SET_OFFSET_OF(&core->joints, NULL);

	core->rules = rules;
	core->rule_count = rule_count;
	core->rule_alloc_count = rule_count ? rule_count : 1;
	core->attr_count = attr_handler_count;

	uint32_t joint_count = attr_handler_count - 1;
	uint32_t registry_count = attr_handler_count + joint_count;

	// The declarations sit above every goto: an unwind entered from an
	// earlier allocation failure reads them all.
	struct filter_query_attr **query_attrs = NULL;
	struct value_registry *registries = NULL;
	uint32_t *rule_groups = NULL;
	struct value_table *joints = NULL;

	query_attrs = (struct filter_query_attr **)memory_balloc(
		memory_context,
		sizeof(struct filter_query_attr *) * attr_handler_count
	);
	if (query_attrs == NULL) {
		goto error;
	}
	memset(query_attrs,
	       0,
	       sizeof(struct filter_query_attr *) * attr_handler_count);
	SET_OFFSET_OF(&core->attrs, query_attrs);

	registries = (struct value_registry *)memory_balloc(
		memory_context, sizeof(struct value_registry) * registry_count
	);
	if (registries == NULL) {
		goto error_free_attrs;
	}
	memset(registries, 0, sizeof(struct value_registry) * registry_count);

	rule_groups = (uint32_t *)memory_balloc(
		memory_context,
		sizeof(uint32_t) * registry_count * core->rule_alloc_count
	);
	if (rule_groups == NULL) {
		goto error_free_registries;
	}

	if (joint_count > 0) {
		joints = (struct value_table *)memory_balloc(
			memory_context, sizeof(struct value_table) * joint_count
		);
		if (joints == NULL) {
			goto error_free_rule_groups;
		}
		memset(joints, 0, sizeof(struct value_table) * joint_count);
	}
	SET_OFFSET_OF(&core->joints, joints);

	for (uint32_t attr_idx = 0; attr_idx < attr_handler_count; ++attr_idx) {
		struct filter_query_attr *query_attr =
			filter_compile_attr_build(
				attr_handlers[attr_idx],
				registries + attr_idx,
				rules,
				rule_count,
				rule_groups + attr_idx * core->rule_alloc_count,
				memory_context
			);
		if (query_attr == NULL) {
			goto error_free_attrs;
		}
		SET_OFFSET_OF(query_attrs + attr_idx, query_attr);
	}

	for (uint32_t joint_idx = 0; joint_idx < joint_count; ++joint_idx) {
		if (merge_and_collect_registry(
			    memory_context,
			    registries + joint_idx * 2,
			    rule_groups +
				    joint_idx * 2 * core->rule_alloc_count,
			    registries + joint_idx * 2 + 1,
			    rule_groups + (joint_idx * 2 + 1) *
						  core->rule_alloc_count,
			    rule_count,
			    joints + joint_idx,
			    registries + attr_handler_count + joint_idx,
			    rule_groups + (attr_handler_count + joint_idx) *
						  core->rule_alloc_count
		    )) {
			goto error_free_attrs;
		}
	}

	core->registries = registries;
	core->rule_groups = rule_groups;

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

error_free_rule_groups:
	memory_bfree(
		memory_context,
		rule_groups,
		sizeof(uint32_t) * registry_count * core->rule_alloc_count
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

	SET_OFFSET_OF(&core->attrs, NULL);
	memory_bfree(
		memory_context,
		query_attrs,
		sizeof(struct filter_query_attr *) * attr_handler_count
	);

error:
	SET_OFFSET_OF(&core->joints, NULL);

	return -1;
}

static inline void
filter_core_destroy(
	struct filter_core *core,
	const struct filter_compile_attr_handlers *attr_handlers[],
	uint32_t attr_handler_count
) {
	struct memory_context *memory_context = &core->memory_context;

	struct filter_query_attr **query_attrs = ADDR_OF(&core->attrs);
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
			sizeof(struct filter_query_attr *) * core->attr_count
		);
	}

	uint32_t joint_count = core->attr_count - 1;
	struct value_table *joints = ADDR_OF(&core->joints);
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

	if (core->registries != NULL) {
		uint32_t registry_count = 2 * core->attr_count - 1;
		for (uint32_t idx = 0; idx < registry_count; ++idx) {
			value_registry_fini(core->registries + idx);
		}
		memory_bfree(
			memory_context,
			core->registries,
			sizeof(struct value_registry) * registry_count
		);
		memory_bfree(
			memory_context,
			core->rule_groups,
			sizeof(uint32_t) * registry_count *
				core->rule_alloc_count
		);
	}

	// Unlink the embedded context from the parent tree before the
	// storage goes away: a dangling child link would corrupt later
	// tree walks.
	memory_context_fini(memory_context);
	memset(core, 0, sizeof(struct filter_core));
}

static inline int
filter_derive(
	struct filter *filter,
	struct filter_core *core,
	const struct filter_rule **rules,
	const struct filter_compile_attr_handlers *attr_handlers[],
	uint32_t attr_handler_count
) {
	if (attr_handler_count < core->attr_count) {
		return -1;
	}

	if (memory_context_init_from(
		    &filter->memory_context, &core->memory_context, "filter"
	    )) {
		return -1;
	}
	struct memory_context *memory_context = &filter->memory_context;

	SET_OFFSET_OF(&filter->attrs, NULL);
	SET_OFFSET_OF(&filter->joints, NULL);
	SET_OFFSET_OF(&filter->joint_sides, NULL);
	SET_OFFSET_OF(&filter->rule_map, NULL);
	filter->shared_attr_count = core->attr_count;
	filter->shared_joint_count = core->attr_count - 1;

	uint32_t extra_count = attr_handler_count - core->attr_count;
	uint32_t joint_count = attr_handler_count - 1;
	uint32_t extra_registry_count = extra_count ? 2 * extra_count : 0;
	uint32_t rule_alloc_count = core->rule_alloc_count;

	// The declarations sit above every goto: an unwind entered from an
	// earlier allocation failure reads them all.
	struct filter_query_attr **query_attrs = NULL;
	struct value_table *joints = NULL;
	uint32_t *joint_sides = NULL;
	struct value_registry *registries = NULL;
	uint32_t *rule_groups = NULL;

	query_attrs = (struct filter_query_attr **)memory_balloc(
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

	struct filter_query_attr **core_attrs = ADDR_OF(&core->attrs);
	for (uint32_t attr_idx = 0; attr_idx < core->attr_count; ++attr_idx) {
		SET_OFFSET_OF(
			query_attrs + attr_idx, ADDR_OF(core_attrs + attr_idx)
		);
	}

	if (joint_count > 0) {
		joints = (struct value_table *)memory_balloc(
			memory_context, sizeof(struct value_table) * joint_count
		);
		if (joints == NULL) {
			goto error_free_attrs;
		}
		memset(joints, 0, sizeof(struct value_table) * joint_count);

		joint_sides = (uint32_t *)memory_balloc(
			memory_context, sizeof(uint32_t) * joint_count * 2
		);
		if (joint_sides == NULL) {
			memory_bfree(
				memory_context,
				joints,
				sizeof(struct value_table) * joint_count
			);
			goto error_free_attrs;
		}

		for (uint32_t joint_idx = 0; joint_idx < joint_count;
		     ++joint_idx) {
			uint32_t left;
			uint32_t right;
			if (joint_idx < filter->shared_joint_count) {
				// The core joints keep their semantic inputs;
				// an input holding a core joint output moves
				// to the output slot of the wider layout.
				left = joint_idx * 2;
				right = joint_idx * 2 + 1;
				if (left >= core->attr_count) {
					left = attr_handler_count +
					       (left - core->attr_count);
				}
				if (right >= core->attr_count) {
					right = attr_handler_count +
						(right - core->attr_count);
				}
			} else {
				// Every extra attribute joins the running
				// result of the core, one joint per
				// attribute in turn.
				uint32_t extra_idx =
					joint_idx - filter->shared_joint_count;
				left = attr_handler_count +
				       filter->shared_joint_count + extra_idx -
				       1;
				right = core->attr_count + extra_idx;
			}
			joint_sides[joint_idx * 2] = left;
			joint_sides[joint_idx * 2 + 1] = right;
		}
	}
	SET_OFFSET_OF(&filter->joints, joints);
	SET_OFFSET_OF(&filter->joint_sides, joint_sides);

	// The join tables are borrowed as small struct copies with their
	// inner relative pointers rebased, so the backing value arrays
	// stay shared with the core and the lookup reads one contiguous
	// joint array.
	struct value_table *core_joints = ADDR_OF(&core->joints);
	for (uint32_t joint_idx = 0; joint_idx < filter->shared_joint_count;
	     ++joint_idx) {
		struct value_table *src = core_joints + joint_idx;
		struct value_table *dst = joints + joint_idx;
		dst->v_dim = src->v_dim;
		dst->h_dim = src->h_dim;
		SET_OFFSET_OF(&dst->values, ADDR_OF(&src->values));
		SET_OFFSET_OF(
			&dst->memory_context, ADDR_OF(&src->memory_context)
		);
	}

	if (extra_count > 0) {
		registries = (struct value_registry *)memory_balloc(
			memory_context,
			sizeof(struct value_registry) * extra_registry_count
		);
		if (registries == NULL) {
			goto error_free_joints;
		}
		memset(registries,
		       0,
		       sizeof(struct value_registry) * extra_registry_count);

		rule_groups = (uint32_t *)memory_balloc(
			memory_context,
			sizeof(uint32_t) * extra_registry_count *
				rule_alloc_count
		);
		if (rule_groups == NULL) {
			goto error_free_registries;
		}

		for (uint32_t extra_idx = 0; extra_idx < extra_count;
		     ++extra_idx) {
			struct filter_query_attr *query_attr =
				filter_compile_attr_build(
					attr_handlers
						[core->attr_count + extra_idx],
					registries + extra_idx,
					rules,
					core->rule_count,
					rule_groups +
						extra_idx * rule_alloc_count,
					memory_context
				);
			if (query_attr == NULL) {
				goto error_free_attrs;
			}
			SET_OFFSET_OF(
				query_attrs + core->attr_count + extra_idx,
				query_attr
			);
		}

		/*
		 * Every extra attribute joins the running result of the
		 * core in turn: the first extra joins the core final
		 * registry, every next one joins the output of the
		 * previous join, mirroring the joint sides above.
		 */
		for (uint32_t extra_idx = 0; extra_idx < extra_count;
		     ++extra_idx) {
			struct value_registry *running_registry;
			const uint32_t *running_rule_groups;
			if (extra_idx == 0) {
				running_registry = core->registries +
						   (2 * core->attr_count - 2);
				running_rule_groups = core->rule_groups +
						      (2 * core->attr_count -
						       2) * rule_alloc_count;
			} else {
				running_registry = registries + extra_count +
						   extra_idx - 1;
				running_rule_groups =
					rule_groups + (extra_count + extra_idx -
						       1) * rule_alloc_count;
			}

			if (merge_and_collect_registry(
				    memory_context,
				    running_registry,
				    running_rule_groups,
				    registries + extra_idx,
				    rule_groups + extra_idx * rule_alloc_count,
				    core->rule_count,
				    joints + filter->shared_joint_count +
					    extra_idx,
				    registries + extra_count + extra_idx,
				    rule_groups + (extra_count + extra_idx) *
							  rule_alloc_count
			    )) {
				goto error_free_attrs;
			}
		}
	}

	struct vline *rule_map = (struct vline *)memory_balloc(
		memory_context, sizeof(struct vline)
	);
	if (rule_map == NULL) {
		goto error_free_attrs;
	}
	SET_OFFSET_OF(&filter->rule_map, rule_map);

	// The final registry of the full signature is the output of the
	// last joint, the core final one itself for a derivation without
	// extras.
	uint32_t final_idx = 2 * attr_handler_count - 2;
	struct value_registry *final_registry;
	const uint32_t *final_rule_groups;
	if (final_idx - attr_handler_count < core->attr_count - 1) {
		final_registry = core->registries + final_idx;
		final_rule_groups =
			core->rule_groups + final_idx * rule_alloc_count;
	} else {
		uint32_t j =
			final_idx - attr_handler_count - (core->attr_count - 1);
		final_registry = registries + (extra_count + j);
		final_rule_groups =
			rule_groups + (extra_count + j) * rule_alloc_count;
	}

	if (collect_rule_map(
		    memory_context,
		    final_registry,
		    final_rule_groups,
		    rules,
		    core->rule_count,
		    rule_map
	    )) {
		memory_bfree(memory_context, rule_map, sizeof(struct vline));
		SET_OFFSET_OF(&filter->rule_map, NULL);
		goto error_free_attrs;
	}

	for (uint32_t idx = 0; idx < extra_registry_count; ++idx) {
		value_registry_fini(registries + idx);
	}
	memory_bfree(
		memory_context,
		rule_groups,
		sizeof(uint32_t) * extra_registry_count * rule_alloc_count
	);
	memory_bfree(
		memory_context,
		registries,
		sizeof(struct value_registry) * extra_registry_count
	);

	return 0;

error_free_attrs:
	for (uint32_t attr_idx = core->attr_count;
	     attr_idx < attr_handler_count;
	     ++attr_idx) {
		struct filter_query_attr *query_attr =
			ADDR_OF(query_attrs + attr_idx);
		if (query_attr == NULL) {
			continue;
		}
		attr_handlers[attr_idx]->free_query(memory_context, query_attr);
	}

	for (uint32_t joint_idx = filter->shared_joint_count;
	     joint_idx < joint_count;
	     ++joint_idx) {
		value_table_free(joints + joint_idx);
	}

	memory_bfree(
		memory_context, joints, sizeof(struct value_table) * joint_count
	);
	memory_bfree(
		memory_context, joint_sides, sizeof(uint32_t) * joint_count * 2
	);

error_free_registries:
	for (uint32_t idx = 0; idx < extra_registry_count; ++idx) {
		value_registry_fini(registries + idx);
	}
	memory_bfree(
		memory_context,
		registries,
		sizeof(struct value_registry) * extra_registry_count
	);

error_free_joints:
	SET_OFFSET_OF(&filter->joints, NULL);
	SET_OFFSET_OF(&filter->joint_sides, NULL);

	SET_OFFSET_OF(&filter->attrs, NULL);
	memory_bfree(
		memory_context,
		query_attrs,
		sizeof(struct filter_query_attr *) * attr_handler_count
	);

error:
	SET_OFFSET_OF(&filter->rule_map, NULL);

	return -1;
}

#define filter_core_init(core, sign, rules, count, mctx)                       \
	filter_core_compile(                                                   \
		core, mctx, rules, count, sign, sizeof(sign) / sizeof(*sign)   \
	)

#define filter_core_free(core, sign)                                           \
	filter_core_destroy(core, sign, sizeof(sign) / sizeof(*sign))

#define filter_derive_init(filter, core, rules, sign)                          \
	filter_derive(filter, core, rules, sign, sizeof(sign) / sizeof(*sign))

#define filter_init(filter, sign, rules, count, mctx)                          \
	filter_compile(                                                        \
		filter, mctx, rules, count, sign, sizeof(sign) / sizeof(*sign) \
	)

#define filter_free(filter, sign)                                              \
	filter_destroy(filter, sign, sizeof(sign) / sizeof(*sign))
