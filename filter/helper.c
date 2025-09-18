#include "helper.h"
#include "common/registry.h"
#include "rule.h"

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
			return res;
		}
		res = value_registry_collect(registry, 0);
		if (res < 0) {
			return res;
		}
	}
	return 0;
}

////////////////////////////////////////////////////////////////////////////////

struct value_set_ctx {
	const struct filter_rule *rules;
	struct value_table *table;
	struct value_registry *registry;
};

#define CAN_TERMINATE(action) (((action) >> 15) == 0)
#define CATEGORY(action) ((action) >> 16)

static int
action_list_is_term(struct value_registry *registry, uint32_t range_idx) {
	struct value_range *range = ADDR_OF(&registry->ranges) + range_idx;
	if (range->count == 0)
		return 0;

	uint32_t action = ADDR_OF(&range->values)[range->count - 1];
	return CAN_TERMINATE(action);
}

uint32_t
find_actions_with_category(
	uint32_t *actions, uint32_t count, uint16_t category
) {
	uint32_t count_category = 0;

	for (uint32_t i = 0; i < count; ++i) {
		uint32_t action = actions[i];
		uint16_t cat = CATEGORY(action);

		// check if action corresponds to category
		if (cat == 0 || (cat & (1 << category))) {
			actions[count_category++] = action;
		} else {
			// if no, we skip this action
			// even if it is terminal, it does not matter
			continue;
		}

		// here, action corresponds to category

		// if non-terminate flag is off,
		// we need terminate.
		if (!(action & ACTION_NON_TERMINATE)) {
			break;
		}
	}

	return count_category;
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

		for (uint32_t ridx = 0; ridx < copy_range->count; ++ridx) {
			value_registry_collect(
				set_ctx->registry,
				ADDR_OF(&copy_range->values)[ridx]
			);
		}

		value_registry_collect(
			set_ctx->registry, set_ctx->rules[idx].action
		);
	}

	return 0;
}

int
merge_and_set_registry_values(
	struct memory_context *memory_context,
	const struct filter_rule *rules,
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
	set_ctx.rules = rules;
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
