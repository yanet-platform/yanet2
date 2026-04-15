#include "filter/compiler/helper.h"
#include "common/registry.h"
#include "filter/rule.h"

int
init_dummy_registry(
	struct memory_context *memory_context,
	uint32_t actions,
	struct value_registry *registry
) {
	int res = value_registry_init(registry, memory_context);
	if (res < 0) {
		return res;
	}
	for (uint32_t i = 0; i < actions; ++i) {
		res = value_registry_start(registry);
		if (res < 0) {
			value_registry_free(registry);
			return res;
		}
		res = value_registry_collect(registry, 0);
		if (res < 0) {
			value_registry_free(registry);
			return res;
		}
	}
	return 0;
}

////////////////////////////////////////////////////////////////////////////////

struct value_set_ctx {
	struct value_table *table;
	struct value_registry *registry;
	struct remap_table remap_table;
};

static int
value_table_set_action(uint32_t v1, uint32_t v2, uint32_t idx, void *data) {
	struct value_set_ctx *set_ctx = (struct value_set_ctx *)data;
	uint32_t *value = value_table_get_ptr(set_ctx->table, v1, v2);
	if (*value)
		return 0;
	*value = idx + 1;

	return 0;
}

int
merge_and_set_registry_values(
	struct memory_context *memory_context,
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
		goto error_merge;

	struct value_set_ctx set_ctx;
	set_ctx.table = table;
	set_ctx.registry = registry;

	for (uint32_t range_idx = 0; range_idx < registry1->range_count;
	     ++range_idx) {
		if (value_registry_start(registry) ||
		    value_registry_collect(registry, range_idx))
			goto error_join;
		if (value_registry_join_range(
			    registry1,
			    registry2,
			    range_idx,
			    value_table_set_action,
			    &set_ctx
		    ))
			goto error_join;
	}

	return 0;

error_join:

error_merge:
	value_registry_free(registry);

error_registry:
	value_table_free(table);

	return -1;
}

struct collect_ctx {
	struct value_table *value_table;
	struct remap_table remap_table;
};

static int
value_table_touch_action(uint32_t v1, uint32_t v2, uint32_t idx, void *data) {
	(void)idx;
	struct collect_ctx *collect_ctx = (struct collect_ctx *)data;

	uint32_t *value = value_table_get_ptr(collect_ctx->value_table, v1, v2);
	if (remap_table_touch(&collect_ctx->remap_table, *value, value))
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

	struct collect_ctx collect_ctx;
	collect_ctx.value_table = table;
	if (remap_table_init(
		    &collect_ctx.remap_table,
		    memory_context,
		    value_registry_capacity(registry1) *
			    value_registry_capacity(registry2)
	    )) {
		goto error_remap_table;
	}

	for (uint32_t range_idx = 0; range_idx < registry1->range_count;
	     ++range_idx) {
		remap_table_new_gen(&collect_ctx.remap_table);
		value_registry_join_range(
			registry1,
			registry2,
			range_idx,
			value_table_touch_action,
			&collect_ctx
		);
	}

	remap_table_compact(&collect_ctx.remap_table);
	value_table_compact(table, &collect_ctx.remap_table);
	remap_table_free(&collect_ctx.remap_table);

	return 0;

error_remap_table:
	value_table_free(table);

	return -1;
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

int
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
