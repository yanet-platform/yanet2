#include "attribute.h"
#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"

#include "filter.h"
#include "ipfw.h"

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
	return &filter->v[vertex].registry;
}

static int
filter_build(
	struct filter *filter,
	const struct filter_action *actions,
	uint32_t actions_count
) {
	// build leaves
	for (size_t i = 0; i < filter->n; ++i) {
		struct filter_attribute *attr = &filter->attr[i];
		struct filter_vertex *v = &filter->v[filter->n + i];

		int res = attr->init_func(
			&v->registry,
			&v->data,
			actions,
			actions_count,
			&filter->memory_context
		);
		if (res < 0) {
			return res;
		}
	}

	// build the rest vertices except root
	for (size_t idx = filter->n - 1; idx >= 2; --idx) {
		int res = merge_and_collect_registry(
			&filter->memory_context,
			vertex_get_registry(filter, 2 * idx),
			vertex_get_registry(filter, 2 * idx + 1),
			&filter->v[idx].table,
			&filter->v[idx].registry
		);
		if (res < 0) {
			return res;
		}
	}

	// build root
	return set_registry_values(
		&filter->memory_context,
		actions,
		vertex_get_registry(filter, 2 * 1),
		vertex_get_registry(filter, 2 * 1 + 1),
		&filter->v[1].table,
		&filter->v[1].registry
	);
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
	filter->n = attributes_count;
	memcpy(filter->attr,
	       attributes,
	       attributes_count * sizeof(struct filter_attribute));
	return filter_build(filter, actions, actions_count);
}

int
filter_query(
	struct filter *filter,
	struct packet_info packet,
	uint32_t **actions,
	uint32_t *count
) {
	// calculate classifiers for attributes
	for (size_t attr_idx = 0; attr_idx < filter->n; ++attr_idx) {
		size_t vertex = filter->n + attr_idx;

		struct filter_attribute *attr = &filter->attr[attr_idx];
		struct filter_vertex *v = &filter->v[vertex];

		// store calculated classifier in the parent vertex
		filter->v[vertex / 2].slots[vertex & 1] =
			attr->lookup_func(packet, v->data);
	}

	// calculate classifiers for the rest vertices except root
	for (size_t vertex = filter->n - 1; vertex >= 2; --vertex) {
		// here both slots must be calculated already
		struct filter_vertex *v = &filter->v[vertex];

		// store calculated classifier in the parent vertex
		filter->v[vertex / 2].slots[vertex & 1] =
			value_table_get(&v->table, v->slots[0], v->slots[1]);
	}

	// get result from root
	struct filter_vertex *r = &filter->v[1];
	uint32_t result = value_table_get(&r->table, r->slots[0], r->slots[1]);
	struct value_range *range = ADDR_OF(&r->registry.ranges) + result;
	*actions = ADDR_OF(&r->registry.values) + range->from;
	*count = range->count;

	return 0;
}