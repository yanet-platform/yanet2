#pragma once

#include "lib/classify/classify.h"
#include "lib/classify/compiler/attribute.h"
#include "lib/classify/compiler/declare.h"
#include "lib/classify/compiler/helper.h"

#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"
#include <assert.h>

/*
 * A built classifier tree node.
 *
 * The node carries its evaluation tape - the attribute lookups in
 * evaluation order and the join tables with their input value slots -
 * and the class space of the node: the registry of class values with
 * the rule to group mapping of the ruleset the tree was built over.
 * A leaf node owns its attribute classifier, a joined node borrows the
 * tapes of its two children and owns its own join alone; the tape
 * slots address the values a lookup writes, the output of a joint
 * occupying the slot after its inputs.
 */
struct classifier {
	struct classify_query_attr **attrs;
	const struct classify_attr_handlers **leaf_handlers;
	uint32_t leaf_count;

	struct value_table *joints;
	uint32_t *joint_sides;
	uint32_t joint_count;
	uint32_t slot_count;

	struct value_registry registry;
	uint32_t *rule_groups;
	uint32_t rule_count;

	// The join holds its own references to the inputs, so a shared
	// classifier - the acl v6 core joined into two filters - stays
	// alive until every joined parent built from it goes away.
	uint32_t refs;
	struct classifier *left;
	struct classifier *right;

	struct memory_context memory_context;
};

static inline void
classify_free(struct classifier *cls);

/*
 * Builds the leaf classifier of an attribute over the ruleset: the
 * attribute groups the rules by their value, splits its definition
 * area into regions and holds the region classifier of a packet.
 */
static inline struct classifier *
classify_leaf(
	struct memory_context *memory_context,
	const struct classify_attr_handlers *handlers,
	const struct filter_rule **rules,
	uint32_t rule_count
) {
	struct classifier *cls = (struct classifier *)memory_balloc(
		memory_context, sizeof(struct classifier)
	);
	if (cls == NULL) {
		return NULL;
	}
	memset(cls, 0, sizeof(struct classifier));

	if (memory_context_init_from(
		    &cls->memory_context, memory_context, "classify:leaf"
	    )) {
		memory_bfree(memory_context, cls, sizeof(struct classifier));
		return NULL;
	}
	memory_context = &cls->memory_context;

	cls->rule_count = rule_count;
	cls->leaf_count = 1;
	cls->slot_count = 1;
	cls->refs = 1;

	struct classify_query_attr **attrs =
		(struct classify_query_attr **)memory_balloc(
			memory_context, sizeof(struct classify_query_attr *)
		);
	const struct classify_attr_handlers **leaf_handlers =
		(const struct classify_attr_handlers **)memory_balloc(
			memory_context, sizeof(struct classify_attr_handlers *)
		);
	uint32_t rule_alloc = rule_count ? rule_count : 1;
	uint32_t *rule_groups = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * rule_alloc
	);
	if (attrs == NULL || leaf_handlers == NULL || rule_groups == NULL) {
		if (leaf_handlers != NULL) {
			memory_bfree(
				memory_context,
				leaf_handlers,
				sizeof(struct classify_attr_handlers *)
			);
		}
		if (rule_groups != NULL) {
			memory_bfree(
				memory_context,
				rule_groups,
				sizeof(uint32_t) * rule_alloc
			);
		}
		if (attrs != NULL) {
			memory_bfree(
				memory_context,
				attrs,
				sizeof(struct classify_query_attr *)
			);
		}
		goto error_ctx;
	}
	cls->attrs = attrs;
	cls->leaf_handlers = leaf_handlers;
	cls->rule_groups = rule_groups;

	// The registry is initialized by the attribute build itself.
	{
		struct classify_query_attr *attr = classify_attr_build(
			handlers,
			&cls->registry,
			rules,
			rule_count,
			rule_groups,
			memory_context
		);
		if (attr == NULL) {
			goto error;
		}
		attrs[0] = attr;
		leaf_handlers[0] = handlers;
	}

	return cls;

error:
	value_registry_fini(&cls->registry);
	memory_bfree(
		memory_context, cls->rule_groups, sizeof(uint32_t) * rule_alloc
	);
	memory_bfree(
		memory_context,
		cls->leaf_handlers,
		sizeof(struct classify_attr_handlers *)
	);
	memory_bfree(
		memory_context, cls->attrs, sizeof(struct classify_query_attr *)
	);

error_ctx:
	memory_context_fini(&cls->memory_context);
	memory_bfree(memory_context, cls, sizeof(struct classifier));
	return NULL;
}

/*
 * Builds the classifier joining two built classifiers: the output
 * class space holds one class per combination of the input classes
 * reachable by the ruleset, and the evaluation tape of the output is
 * the tapes of both inputs followed by the new join.
 *
 * Both inputs must be built over the same ruleset array; the output
 * borrows the tapes of the inputs, so the inputs are freed after the
 * output.
 */
static inline struct classifier *
classify_join(
	struct memory_context *memory_context,
	struct classifier *left,
	struct classifier *right
) {
	assert(left->rule_count == right->rule_count);

	struct classifier *cls = (struct classifier *)memory_balloc(
		memory_context, sizeof(struct classifier)
	);
	if (cls == NULL) {
		return NULL;
	}
	memset(cls, 0, sizeof(struct classifier));

	struct memory_context *parent_context = memory_context;
	if (memory_context_init_from(
		    &cls->memory_context, memory_context, "classify:joint"
	    )) {
		memory_bfree(memory_context, cls, sizeof(struct classifier));
		return NULL;
	}
	memory_context = &cls->memory_context;

	cls->rule_count = left->rule_count;
	cls->leaf_count = left->leaf_count + right->leaf_count;
	cls->joint_count = left->joint_count + right->joint_count + 1;
	cls->slot_count = left->slot_count + right->slot_count + 1;
	cls->refs = 1;
	uint32_t rule_alloc = cls->rule_count ? cls->rule_count : 1;

	struct classify_query_attr **attrs =
		(struct classify_query_attr **)memory_balloc(
			memory_context,
			sizeof(struct classify_query_attr *) * cls->leaf_count
		);
	const struct classify_attr_handlers **leaf_handlers =
		(const struct classify_attr_handlers **)memory_balloc(
			memory_context,
			sizeof(struct classify_attr_handlers *) *
				cls->leaf_count
		);
	struct value_table *joints = (struct value_table *)memory_balloc(
		memory_context, sizeof(struct value_table) * cls->joint_count
	);
	uint32_t *joint_sides = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * cls->joint_count * 2
	);
	uint32_t *rule_groups = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * rule_alloc
	);
	if (attrs == NULL || leaf_handlers == NULL || joints == NULL ||
	    joint_sides == NULL || rule_groups == NULL) {
		if (attrs != NULL) {
			memory_bfree(
				memory_context,
				attrs,
				sizeof(struct classify_query_attr *) *
					cls->leaf_count
			);
		}
		if (leaf_handlers != NULL) {
			memory_bfree(
				memory_context,
				leaf_handlers,
				sizeof(struct classify_attr_handlers *) *
					cls->leaf_count
			);
		}
		if (joints != NULL) {
			memory_bfree(
				memory_context,
				joints,
				sizeof(struct value_table) * cls->joint_count
			);
		}
		if (joint_sides != NULL) {
			memory_bfree(
				memory_context,
				joint_sides,
				sizeof(uint32_t) * cls->joint_count * 2
			);
		}
		if (rule_groups != NULL) {
			memory_bfree(
				memory_context,
				rule_groups,
				sizeof(uint32_t) * rule_alloc
			);
		}
		goto error_ctx;
	}
	memset(joints, 0, sizeof(struct value_table) * cls->joint_count);
	cls->attrs = attrs;
	cls->leaf_handlers = leaf_handlers;
	cls->joints = joints;
	cls->joint_sides = joint_sides;
	cls->rule_groups = rule_groups;

	// The registry is initialized by the join collection itself.

	// The leaf tapes concatenate: the entries borrow the attribute
	// classifiers of the inputs.
	for (uint32_t idx = 0; idx < left->leaf_count; ++idx) {
		attrs[idx] = left->attrs[idx];
		leaf_handlers[idx] = left->leaf_handlers[idx];
	}
	for (uint32_t idx = 0; idx < right->leaf_count; ++idx) {
		attrs[left->leaf_count + idx] = right->attrs[idx];
		leaf_handlers[left->leaf_count + idx] =
			right->leaf_handlers[idx];
	}

	/*
	 * The join tables of the inputs are borrowed as small struct
	 * copies with their inner relative pointers rebased, so the
	 * backing value arrays stay shared and the lookup reads one
	 * contiguous joint tape. The sides of the right input shift by
	 * the slot count of the left one.
	 */
	{
		struct value_table *left_joints = left->joints;
		struct value_table *right_joints = right->joints;
		const uint32_t *left_sides = left->joint_sides;
		const uint32_t *right_sides = right->joint_sides;

		/*
		 * The tape addresses all attributes of both inputs first
		 * and the joint outputs after them in tape order, so a
		 * side of an input holding one of its joint outputs moves
		 * by the attribute count inserted before the joints: every
		 * side of the right input, and the joint output sides of
		 * the left one.
		 */
		for (uint32_t idx = 0; idx < left->joint_count; ++idx) {
			struct value_table *src = left_joints + idx;
			struct value_table *dst = joints + idx;
			dst->v_dim = src->v_dim;
			dst->h_dim = src->h_dim;
			SET_OFFSET_OF(&dst->values, ADDR_OF(&src->values));
			SET_OFFSET_OF(
				&dst->memory_context,
				ADDR_OF(&src->memory_context)
			);
			for (uint32_t side = 0; side < 2; ++side) {
				uint32_t s = left_sides[idx * 2 + side];
				joint_sides[idx * 2 + side] =
					s < left->leaf_count
						? s
						: s + right->leaf_count;
			}
		}

		for (uint32_t idx = 0; idx < right->joint_count; ++idx) {
			struct value_table *src = right_joints + idx;
			struct value_table *dst =
				joints + left->joint_count + idx;
			dst->v_dim = src->v_dim;
			dst->h_dim = src->h_dim;
			SET_OFFSET_OF(&dst->values, ADDR_OF(&src->values));
			SET_OFFSET_OF(
				&dst->memory_context,
				ADDR_OF(&src->memory_context)
			);
			for (uint32_t side = 0; side < 2; ++side) {
				uint32_t s = right_sides[idx * 2 + side];
				joint_sides[(left->joint_count + idx) * 2 + side] =
					s < right->leaf_count
						? s + left->leaf_count
						: cls->leaf_count +
							  left->joint_count +
							  (s - right->leaf_count
							  );
			}
		}

		uint32_t left_final =
			left->joint_count > 0
				? cls->leaf_count + left->joint_count - 1
				: left->leaf_count - 1;
		uint32_t right_final = right->joint_count > 0
					       ? cls->leaf_count +
							 left->joint_count +
							 right->joint_count - 1
					       : cls->leaf_count - 1;
		joint_sides[(cls->joint_count - 1) * 2] = left_final;
		joint_sides[(cls->joint_count - 1) * 2 + 1] = right_final;
	}

	if (classify_merge_and_collect(
		    memory_context,
		    &left->registry,
		    left->rule_groups,
		    &right->registry,
		    right->rule_groups,
		    cls->rule_count,
		    joints + (cls->joint_count - 1),
		    &cls->registry,
		    rule_groups
	    )) {
		goto error;
	}

	++left->refs;
	++right->refs;
	cls->left = left;
	cls->right = right;

	return cls;

error:
	value_registry_fini(&cls->registry);
	memory_bfree(
		memory_context, cls->rule_groups, sizeof(uint32_t) * rule_alloc
	);
	memory_bfree(
		memory_context,
		cls->joint_sides,
		sizeof(uint32_t) * cls->joint_count * 2
	);
	memory_bfree(
		memory_context,
		cls->joints,
		sizeof(struct value_table) * cls->joint_count
	);
	memory_bfree(
		memory_context,
		cls->leaf_handlers,
		sizeof(struct classify_attr_handlers *) * cls->leaf_count
	);
	memory_bfree(
		memory_context,
		cls->attrs,
		sizeof(struct classify_query_attr *) * cls->leaf_count
	);

error_ctx:
	memory_context_fini(&cls->memory_context);
	// The node block belongs to the parent of its embedded context;
	// freeing it through the just finished child would walk a zeroed
	// allocator.
	memory_bfree(parent_context, cls, sizeof(struct classifier));
	return NULL;
}

/*
 * Builds a classifier over a flat attribute signature as a pairwise
 * reductive tree: adjacent attributes join first and the results
 * combine level by level, in the signature order.
 *
 * The join cost is the product of the per rule ranges of both sides,
 * and a range grows with every attribute joined into its subtree. The
 * pairwise reduction joins the big region attributes of a signature
 * leaf to leaf - the region cross happens once, between sides of
 * comparable, not yet multiplied size - while every later level
 * multiplies the already compacted class spaces. A left spine or an
 * even split of the same signature instead crosses a region attribute
 * with a subtree that already carries the small attributes, paying the
 * region product against the multiplied range.
 */
static inline struct classifier *
classify_build(
	struct memory_context *memory_context,
	const struct classify_attr_handlers *attr_handlers[],
	uint32_t attr_handler_count,
	const struct filter_rule **rules,
	uint32_t rule_count
) {
	// A signature carries a handful of attributes; the fixed cap keeps
	// the level array a plain stack slot the compiler can bound.
	assert(attr_handler_count <= 16);
	struct classifier *level[16];
	for (uint32_t idx = 0; idx < attr_handler_count; ++idx) {
		level[idx] = classify_leaf(
			memory_context, attr_handlers[idx], rules, rule_count
		);
		if (level[idx] == NULL) {
			while (idx > 0) {
				classify_free(level[--idx]);
			}
			return NULL;
		}
	}

	uint32_t count = attr_handler_count;
	while (count > 1) {
		uint32_t out = 0;
		for (uint32_t idx = 0; idx + 1 < count; idx += 2) {
			struct classifier *joined = classify_join(
				memory_context, level[idx], level[idx + 1]
			);
			// The pair releases its own references; the joined node
			// holds the ones for the lifetime of the tree.
			classify_free(level[idx]);
			classify_free(level[idx + 1]);
			if (joined == NULL) {
				for (uint32_t rest = idx + 2; rest < count;
				     ++rest) {
					classify_free(level[rest]);
				}
				return NULL;
			}
			level[out++] = joined;
		}
		if (count & 1) {
			level[out++] = level[count - 1];
		}
		count = out;
	}

	return level[0];
}

/*
 * Builds the decoder of a classifier over a projection of the ruleset
 * it was built on: the value line maps every final class to the index
 * of the first rule of the projection producing the class. The
 * projection is an array of the original ruleset length holding NULL
 * for every rule absent from the projection.
 */
static inline struct vline *
classify_decode(
	struct classifier *cls,
	struct memory_context *memory_context,
	const struct filter_rule **rules
) {
	struct vline *rule_map = (struct vline *)memory_balloc(
		memory_context, sizeof(struct vline)
	);
	if (rule_map == NULL) {
		return NULL;
	}

	if (classify_collect_rule_map(
		    memory_context,
		    &cls->registry,
		    cls->rule_groups,
		    rules,
		    cls->rule_count,
		    rule_map
	    )) {
		memory_bfree(memory_context, rule_map, sizeof(struct vline));
		return NULL;
	}

	return rule_map;
}

/*
 * Freezes a classifier and its decoder into a filter: the tape of the
 * tree and the rule map move into the filter, which borrows the leaf
 * classifiers and join tables of the tree - the classifiers are freed
 * after the filters built from them.
 */
static inline int
classify_filter_init(
	struct classify_filter *filter,
	struct memory_context *memory_context,
	struct classifier *cls,
	struct vline *rule_map
) {
	if (memory_context_init_from(
		    &filter->memory_context, memory_context, "classify:filter"
	    )) {
		return -1;
	}
	memory_context = &filter->memory_context;

	SET_OFFSET_OF(&filter->attrs, NULL);
	SET_OFFSET_OF(&filter->joints, NULL);
	SET_OFFSET_OF(&filter->joint_sides, NULL);
	SET_OFFSET_OF(&filter->rule_map, NULL);
	filter->attr_count = cls->leaf_count;
	filter->joint_count = cls->joint_count;

	struct classify_query_attr **attrs =
		(struct classify_query_attr **)memory_balloc(
			memory_context,
			sizeof(struct classify_query_attr *) * cls->leaf_count
		);
	struct value_table *joints = NULL;
	uint32_t *joint_sides = NULL;
	if (cls->joint_count > 0) {
		joints = (struct value_table *)memory_balloc(
			memory_context,
			sizeof(struct value_table) * cls->joint_count
		);
		joint_sides = (uint32_t *)memory_balloc(
			memory_context, sizeof(uint32_t) * cls->joint_count * 2
		);
	}
	if (attrs == NULL ||
	    (cls->joint_count > 0 && (joints == NULL || joint_sides == NULL))) {
		if (attrs != NULL) {
			memory_bfree(
				memory_context,
				attrs,
				sizeof(struct classify_query_attr *) *
					cls->leaf_count
			);
		}
		if (joints != NULL) {
			memory_bfree(
				memory_context,
				joints,
				sizeof(struct value_table) * cls->joint_count
			);
		}
		if (joint_sides != NULL) {
			memory_bfree(
				memory_context,
				joint_sides,
				sizeof(uint32_t) * cls->joint_count * 2
			);
		}
		memory_context_fini(&filter->memory_context);
		return -1;
	}
	if (joints != NULL) {
		memset(joints, 0, sizeof(struct value_table) * cls->joint_count
		);
	}
	SET_OFFSET_OF(&filter->attrs, attrs);
	SET_OFFSET_OF(&filter->joints, joints);
	SET_OFFSET_OF(&filter->joint_sides, joint_sides);

	for (uint32_t idx = 0; idx < cls->leaf_count; ++idx) {
		SET_OFFSET_OF(attrs + idx, cls->attrs[idx]);
	}

	{
		struct value_table *src_joints = cls->joints;
		const uint32_t *src_sides = cls->joint_sides;
		for (uint32_t idx = 0; idx < cls->joint_count; ++idx) {
			struct value_table *src = src_joints + idx;
			struct value_table *dst = joints + idx;
			dst->v_dim = src->v_dim;
			dst->h_dim = src->h_dim;
			SET_OFFSET_OF(&dst->values, ADDR_OF(&src->values));
			SET_OFFSET_OF(
				&dst->memory_context,
				ADDR_OF(&src->memory_context)
			);
			joint_sides[idx * 2] = src_sides[idx * 2];
			joint_sides[idx * 2 + 1] = src_sides[idx * 2 + 1];
		}
	}

	SET_OFFSET_OF(&filter->rule_map, rule_map);

	return 0;
}

/*
 * Releases the own allocations of a classifier node: a leaf frees its
 * attribute classifier, a join frees its own join alone and borrows
 * the tapes of its children, which are freed after it.
 */
static inline void
classify_free(struct classifier *cls) {
	if (cls == NULL) {
		return;
	}
	if (--cls->refs != 0) {
		return;
	}

	struct memory_context *memory_context = &cls->memory_context;

	if (cls->leaf_count == 1 && cls->joint_count == 0) {
		struct classify_query_attr *attr =
			cls->attrs ? cls->attrs[0] : NULL;
		if (attr != NULL) {
			cls->leaf_handlers[0]->free_query(memory_context, attr);
		}
	}

	if (cls->joint_count > 0) {
		// Only the last join of the tape belongs to this node; the
		// earlier entries are borrowed copies of the children.
		value_table_free(cls->joints + (cls->joint_count - 1));
	}

	value_registry_fini(&cls->registry);

	memory_bfree(
		memory_context,
		cls->rule_groups,
		sizeof(uint32_t) * (cls->rule_count ? cls->rule_count : 1)
	);
	if (cls->joint_sides != NULL) {
		memory_bfree(
			memory_context,
			cls->joint_sides,
			sizeof(uint32_t) * cls->joint_count * 2
		);
	}
	if (cls->joints != NULL) {
		memory_bfree(
			memory_context,
			cls->joints,
			sizeof(struct value_table) * cls->joint_count
		);
	}
	if (cls->leaf_handlers != NULL) {
		memory_bfree(
			memory_context,
			cls->leaf_handlers,
			sizeof(struct classify_attr_handlers *) *
				cls->leaf_count
		);
	}
	if (cls->attrs != NULL) {
		memory_bfree(
			memory_context,
			cls->attrs,
			sizeof(struct classify_query_attr *) * cls->leaf_count
		);
	}

	// The node block belongs to the parent of its embedded context;
	// the context is unlinked from the parent tree before the storage
	// goes away, so a dangling child link cannot corrupt later tree
	// walks.
	struct memory_context *parent = ADDR_OF(&memory_context->parent);
	memory_context_fini(memory_context);

	struct classifier *left = cls->left;
	struct classifier *right = cls->right;
	memset(cls, 0, sizeof(struct classifier));
	memory_bfree(parent, cls, sizeof(struct classifier));

	classify_free(left);
	classify_free(right);
}

/*
 * Releases the filter with its decoder; the classifier tree it was
 * built from is released separately, after every filter built from it.
 */
static inline void
classify_filter_free(struct classify_filter *filter) {
	struct memory_context *memory_context = &filter->memory_context;

	// The decoder was allocated from the parent of the filter context,
	// so it is released through the same parent.
	struct memory_context *parent = ADDR_OF(&filter->memory_context.parent);
	struct vline *rule_map = ADDR_OF(&filter->rule_map);
	if (rule_map != NULL) {
		vline_free(rule_map);
		memory_bfree(parent, rule_map, sizeof(struct vline));
		SET_OFFSET_OF(&filter->rule_map, NULL);
	}

	if (ADDR_OF(&filter->attrs) != NULL) {
		memory_bfree(
			memory_context,
			ADDR_OF(&filter->attrs),
			sizeof(struct classify_query_attr *) *
				filter->attr_count
		);
	}

	if (ADDR_OF(&filter->joints) != NULL) {
		memory_bfree(
			memory_context,
			ADDR_OF(&filter->joints),
			sizeof(struct value_table) * filter->joint_count
		);
	}

	if (ADDR_OF(&filter->joint_sides) != NULL) {
		memory_bfree(
			memory_context,
			ADDR_OF(&filter->joint_sides),
			sizeof(uint32_t) * filter->joint_count * 2
		);
	}

	memory_context_fini(memory_context);
	memset(filter, 0, sizeof(struct classify_filter));
}
