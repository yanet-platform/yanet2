#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"

#include "filter.h"
#include "ipfw.h"
#include "tree.h"

struct value_set_ctx {
	const struct filter_action *actions;
	struct value_table *table;
	struct value_registry *registry;
};

static int
action_list_is_term(struct value_registry *registry, uint32_t range_idx) {
	struct value_range *range = ADDR_OF(&registry->ranges) + range_idx;
	if (range->count == 0)
		return 0;

	uint32_t action_id =
		ADDR_OF(&registry->values)[range->from + range->count - 1];
	return !(action_id & ACTION_NON_TERMINATE);
}

static int
value_table_set_action(uint32_t v1, uint32_t v2, uint32_t idx, void *data) {
	struct value_set_ctx *set_ctx = (struct value_set_ctx *)data;
	uint32_t prev_value = value_table_get(set_ctx->table, v1, v2);

	if (!action_list_is_term(set_ctx->registry, prev_value)) {
		/*
		 * FIXME: we assume value table produces increasing sequence
		 * of values - this is important for value registry handling.
		 */
		int res = value_table_touch(set_ctx->table, v1, v2);

		if (res <= 0)
			return res;

		if (value_registry_start(set_ctx->registry))
			return -1;

		struct value_range *copy_range =
			ADDR_OF(&set_ctx->registry->ranges) + prev_value;

		for (uint32_t ridx = copy_range->from;
		     ridx < copy_range->from + copy_range->count;
		     ++ridx) {
			value_registry_collect(
				set_ctx->registry,
				ADDR_OF(&set_ctx->registry->values)[ridx]
			);
		}

		value_registry_collect(
			set_ctx->registry, set_ctx->actions[idx].action
		);
	}

	return 0;
}

static int
set_registry_values(
	struct memory_context *memory_context,
	const struct filter_action *actions,
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table,
	struct value_registry *registry
) {
	if (value_table_init(
		    table,
		    memory_context,
		    value_registry_capacity(registry1),
		    value_registry_capacity(registry2)
	    )) {
		return -1;
	}

	if (value_registry_init(registry, memory_context)) {
		goto error_registry;
	}

	if (value_registry_start(registry))
		return -1;

	struct value_set_ctx set_ctx;
	set_ctx.actions = actions;
	set_ctx.table = table;
	set_ctx.registry = registry;

	for (uint32_t range_idx = 0; range_idx < registry1->range_count;
	     ++range_idx) {
		value_table_new_gen(table);
		if (value_registry_join_range(
			    registry1,
			    registry2,
			    range_idx,
			    value_table_set_action,
			    &set_ctx
		    ))
			goto error_merge;
	}

	return 0;

error_merge:
	value_registry_free(registry);

error_registry:
	value_table_free(table);

	return 0;
}

static int
value_table_touch_action(uint32_t v1, uint32_t v2, uint32_t idx, void *data) {
	(void)idx;
	struct value_table *table = (struct value_table *)data;
	if (value_table_touch(table, v1, v2) < 0)
		return -1;
	return 0;
}

static int
merge_registry_values(
	struct memory_context *memory_context,
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table
) {
	if (value_table_init(
		    table,
		    memory_context,
		    value_registry_capacity(registry1),
		    value_registry_capacity(registry2)
	    )) {
		return -1;
	}

	for (uint32_t range_idx = 0; range_idx < registry1->range_count;
	     ++range_idx) {
		value_table_new_gen(table);
		value_registry_join_range(
			registry1,
			registry2,
			range_idx,
			value_table_touch_action,
			table
		);
	}

	value_table_compact(table);

	return 0;
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

	return 0;
}

static int
collect_registry_values(
	struct memory_context *memory_context,
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table,
	struct value_registry *registry
) {
	if (value_registry_init(registry, memory_context)) {
		return -1;
	}

	struct value_collect_ctx collect_ctx;
	collect_ctx.table = table;
	collect_ctx.registry = registry;

	for (uint32_t range_idx = 0; range_idx < registry1->range_count;
	     ++range_idx) {
		value_registry_start(registry);
		value_registry_join_range(
			registry1,
			registry2,
			range_idx,
			value_table_collect_action,
			&collect_ctx
		);
	}

	return 0;
}

static int
merge_and_collect_registry(
	struct memory_context *memory_context,
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table,
	struct value_registry *registry
) {
	if (merge_registry_values(
		    memory_context, registry1, registry2, table
	    )) {
		return -1;
	}

	if (collect_registry_values(
		    memory_context, registry1, registry2, table, registry
	    )) {
		value_table_free(table);
		return -1;
	}

	return 0;
}

static struct value_registry *
vertex_get_registry(struct filter *filter, size_t vertex) {
	if (tree_is_vertex_leaf(vertex, filter->attributes_count)) { // leaf
		size_t idx = tree_leaf_idx(vertex, filter->attributes_count);
		return &(filter->leaves + idx)->registry;
	} else { // vertex
		size_t idx = tree_vertex_idx(vertex);
		return &(filter->vertices + idx)->registry;
	}
}

static int
filter_build_vertex(struct filter *filter, size_t vertex) {
	size_t idx = tree_vertex_idx(vertex);
	struct filter_vertex *filter_vertex = &filter->vertices[idx];

	size_t left = tree_vertex_left(vertex);
	struct value_registry *left_registry =
		vertex_get_registry(filter, left);

	size_t right = tree_vertex_right(vertex);
	struct value_registry *right_registry =
		vertex_get_registry(filter, right);

	return merge_and_collect_registry(
		&filter->memory_context,
		left_registry,
		right_registry,
		&filter_vertex->table,
		&filter_vertex->registry
	);
}

static int
filter_build_root(struct filter *filter, const struct filter_action *actions) {
	struct filter_vertex *root = &filter->vertices[1];
	struct value_registry *left =
		vertex_get_registry(filter, tree_vertex_left(1));
	struct value_registry *right =
		vertex_get_registry(filter, tree_vertex_right(1));
	return set_registry_values(
		&filter->memory_context,
		actions,
		left,
		right,
		&root->table,
		&root->registry
	);
}

static int
filter_build(
	struct filter *filter,
	const struct filter_action *actions,
	uint32_t actions_count
) {
	for (size_t i = 0; i < filter->attributes_count; ++i) {
		struct filter_leaf *leaf = &filter->leaves[i];
		int res = leaf->init_func(
			&leaf->registry,
			&leaf->data,
			actions,
			actions_count,
			&filter->memory_context
		);
		if (res < 0) {
			return res;
		}
	}
	for (size_t vertex = filter->attributes_count - 1; vertex >= 2;
	     --vertex) {
		int res = filter_build_vertex(filter, vertex);
		if (res < 0) {
			return res;
		}
	}
	return filter_build_root(filter, actions);
}

int
filter_init(
	struct filter *filter,
	const struct filter_attribute *attributes,
	uint32_t attributes_count,
	const struct filter_action *actions,
	uint32_t actions_count,
	struct memory_context *memory_context
) {
	if (attributes_count == 0) {
		return -1;
	}
	int res = memory_context_init_from(
		&filter->memory_context, memory_context, "filter"
	);
	if (res < 0) {
		return res;
	}
	filter->attributes_count = attributes_count;
	for (size_t i = 0; i < filter->attributes_count; ++i) {
		struct filter_leaf *leaf = &filter->leaves[i];
		leaf->init_func = attributes[i].init_func;
		leaf->lookup_func = attributes[i].lookup_func;
	}
	for (size_t i = 1; i < filter->attributes_count; ++i) {
		struct filter_vertex *vertex = &filter->vertices[i];
		vertex->slots[0] = vertex->slots[1] = -1;
	}
	return filter_build(filter, actions, actions_count);
}

int
filter_query(
	struct filter *filter,
	struct packet_info packet_view,
	uint32_t **actions,
	uint32_t *count
) {
	for (size_t i = 0; i < filter->attributes_count; ++i) {
		size_t vertex = tree_vertex_attr(i, filter->attributes_count);

		size_t parent = tree_vertex_parent(vertex);
		size_t parent_idx = tree_vertex_idx(parent);

		struct filter_leaf *leaf = &filter->leaves[i];
		filter->vertices[parent_idx].slots[vertex & 1] =
			leaf->lookup_func(packet_view, leaf->data);
	}
	for (size_t vertex = filter->attributes_count - 1; vertex >= 2;
	     --vertex) {
		// here both slots must be calculated already
		size_t vertex_idx = tree_vertex_idx(vertex);
		struct filter_vertex *filter_vertex =
			&filter->vertices[vertex_idx];
		size_t parent = tree_vertex_parent(vertex);
		size_t parent_idx = tree_vertex_idx(parent);
		filter->vertices[parent_idx].slots[vertex & 1] =
			value_table_get(
				&filter_vertex->table,
				filter_vertex->slots[0],
				filter_vertex->slots[1]
			);
	}

	// get result
	struct filter_vertex *root = &filter->vertices[1];
	uint32_t result =
		value_table_get(&root->table, root->slots[0], root->slots[1]);
	struct value_range *range = ADDR_OF(&root->registry.ranges) + result;
	*actions = ADDR_OF(&root->registry.values) + range->from;
	*count = range->count;

	return 0;
}