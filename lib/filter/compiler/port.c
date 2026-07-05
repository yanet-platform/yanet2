#include "common/memory.h"
#include "common/radix.h"
#include "common/registry.h"
#include "common/value.h"
#include "declare.h"
#include "lib/filter/rule.h"

#include <stdint.h>

typedef void (*action_get_port_range_func)(
	const struct filter_rule *action,
	struct filter_port_range **ranges,
	uint32_t *count
);

static inline int
build_port_range_info(
	struct memory_context *memory_context,
	const struct filter_rule **actions,
	uint32_t action_count,
	action_get_port_range_func get_port_ranges,

	struct radix *port_range_radix,

	struct filter_port_range **port_ranges,
	uint64_t *port_range_count,

	struct value_range **port_range_groups,
	uint32_t *port_range_group_count
)
{
	*port_ranges = NULL;
	*port_range_count = 0;

	*port_range_groups = NULL;
	*port_range_group_count = 0;

	radix_init(port_range_radix, memory_context);

	for (const struct filter_rule **action_ptr = actions;
	     action_ptr < actions + action_count;
	     ++action_ptr) {

		if (*action_ptr == NULL)
			continue;
		const struct filter_rule *action = *action_ptr;

		struct filter_port_range *rule_port_ranges;
		uint32_t rule_port_range_count;
		get_port_ranges(action, &rule_port_ranges, &rule_port_range_count);

		for (struct filter_port_range *rule_port_range = rule_port_ranges;
		     rule_port_range < rule_port_ranges + rule_port_range_count;
		     ++rule_port_range) {
			struct filter_port_range port_range = *rule_port_range;

			if (radix_lookup(port_range_radix, 4, (uint8_t *)&port_range) !=
			    RADIX_VALUE_INVALID)
				continue;

			radix_insert(port_range_radix, 4, (uint8_t *)&port_range, *port_range_count);
			if (mem_array_expand_exp(
				    memory_context,
				    (void **)port_ranges,
				    sizeof(**port_ranges),
				    port_range_count
			    )) {
				goto error_port_ranges;
			}
			(*port_ranges)[*port_range_count - 1] = port_range;
		}
	}

	if (*port_range_count == 0) {
		return 0;
	}

	struct value_table port_range_table;
	if (value_table_init(&port_range_table, memory_context, 1, *port_range_count)) {
		goto error_port_ranges;
	}

	struct remap_table port_range_remap;
	if (remap_table_init(&port_range_remap, memory_context, *port_range_count)) {
		goto error_table;
	}

	for (const struct filter_rule **action_ptr = actions;
	     action_ptr < actions + action_count;
	     ++action_ptr) {

		if (*action_ptr == NULL)
			continue;
		const struct filter_rule *action = *action_ptr;

		remap_table_new_gen(&port_range_remap);

		struct filter_port_range *rule_port_ranges;
		uint32_t rule_port_range_count;
		get_port_ranges(action, &rule_port_ranges, &rule_port_range_count);

		for (struct filter_port_range *rule_port_range = rule_port_ranges;
		     rule_port_range < rule_port_ranges + rule_port_range_count;
		     ++rule_port_range) {
			struct filter_port_range port_range = *rule_port_range;

			uint32_t port_range_idx =
				radix_lookup(port_range_radix, 4, (uint8_t *)&port_range);
			uint32_t *v =
				value_table_get_ptr(&port_range_table, 0, port_range_idx);
			if (remap_table_touch(&port_range_remap, *v, v) < 0) {
				goto error_touch;
			}
		}
	}

	remap_table_compact(&port_range_remap);
	value_table_compact(&port_range_table, &port_range_remap);
	remap_table_free(&port_range_remap);

	for (uint32_t idx = 0; idx < *port_range_count; ++idx)
		if (value_table_get(&port_range_table, 0, idx) >= *port_range_group_count)
			*port_range_group_count =
				value_table_get(&port_range_table, 0, idx) + 1;
	*port_range_groups = (struct value_range *)memory_balloc(
		memory_context, sizeof(struct value_range) * *port_range_group_count
	);
	if (*port_range_groups == NULL)
		goto error_table;

	memset(*port_range_groups, 0, sizeof(struct value_range) * *port_range_group_count);
	for (uint32_t port_range_idx = 0; port_range_idx < *port_range_count; ++port_range_idx) {
		if (value_range_append(
			    memory_context,
			    *port_range_groups +
				    value_table_get(&port_range_table, 0, port_range_idx),
			    port_range_idx
		    )) {
			goto error_append;
		}
	}

	value_table_free(&port_range_table);



	return 0;

error_append:
	for (uint32_t idx = 0; idx < *port_range_group_count; ++idx) {
		struct value_range *range = *port_range_groups + idx;
		mem_array_free_exp(
			memory_context,
			ADDR_OF(&range->values),
			sizeof(uint32_t),
			range->count
		);
	}

	memory_bfree(
		memory_context,
		*port_range_groups,
		sizeof(struct value_range) * *port_range_group_count
	);

	*port_range_groups = NULL;
	*port_range_group_count = 0;

	goto error_table;

error_touch:
	remap_table_free(&port_range_remap);

error_table:
	value_table_free(&port_range_table);


error_port_ranges:
	mem_array_free_exp(memory_context, *port_ranges, sizeof(**port_ranges), *port_range_count);
	*port_ranges = NULL;
	*port_range_count = 0;

	radix_free(port_range_radix);

	return -1;
}

static inline int
touch_port_range_groups(
	struct memory_context *memory_context,
	struct filter_port_range *all_port_ranges,
	struct value_range *port_range_groups,
	uint32_t port_range_group_count,
	struct value_table *value_table
) {
	struct remap_table remap_table;
	if (remap_table_init(
		    &remap_table,
		    memory_context,
		    65536
	    )) {
		return -1;
	}

	for (uint32_t port_range_group_idx = 0; port_range_group_idx < port_range_group_count;
	     ++port_range_group_idx) {
		remap_table_new_gen(&remap_table);

		uint32_t *values = ADDR_OF(&port_range_groups[port_range_group_idx].values);
		for (uint32_t idx = 0; idx < port_range_groups[port_range_group_idx].count;
		     ++idx) {
			struct filter_port_range port_range = all_port_ranges[values[idx]];

			// Skip `any` port_range
			if (port_range.from == 0 &&
			    port_range.to == 65535)
				continue;

			for (uint32_t idx = port_range.from; idx <= port_range.to; ++idx) {
				uint32_t *value = value_table_get_ptr(
					value_table,
					0,
					idx
				);
				if (remap_table_touch(
					    &remap_table, *value, value
				    ) < 0) {
					goto error;
				}
			}
		}
	}

	remap_table_compact(&remap_table);
	value_table_compact(value_table, &remap_table);
	remap_table_free(&remap_table);

	return 0;

error:
	remap_table_free(&remap_table);
	return -1;

}

static inline int
collect_port_range_values(
	struct filter_port_range *all_port_ranges,
	uint32_t all_port_range_count,
	struct value_table *value_table,
	struct value_registry *registry
) {
	for (uint32_t port_range_idx = 0; port_range_idx < all_port_range_count; ++port_range_idx) {
		struct filter_port_range port_range = all_port_ranges[port_range_idx];

		value_registry_start(registry);

		for (uint32_t idx = port_range.from; idx <= port_range.to; ++idx) {
			if (value_registry_collect(
				    registry,
				    value_table_get(
					    value_table,
					    0,
					    idx
				    )
			)) {
				return -1;
			}
		}
	}

	return 0;
}


static int
collect_port_values(
	struct memory_context *memory_context,
	const struct filter_rule **actions,
	uint32_t count,
	action_get_port_range_func get_port_range,
	struct value_table *table,
	struct value_registry *registry
) {
	struct filter_port_range *port_ranges;
	uint64_t port_range_count;
	struct radix port_range_radix;

	struct value_range *port_range_groups;
	uint32_t port_range_group_count;

	if (build_port_range_info(
		    memory_context,
		    actions,
		    count,
		    get_port_range,
		    &port_range_radix,
		    &port_ranges,
		    &port_range_count,
		    &port_range_groups,
		    &port_range_group_count

	)) {
		return -1;
	}

	if (value_table_init(table, memory_context, 1, 65536)) {
		goto error_info;
	}

	if (touch_port_range_groups(
		    memory_context,
		    port_ranges,
		    port_range_groups,
		    port_range_group_count,
		    table
	    )) {
		goto error_table;
	}

	struct value_registry port_range_registry;
	value_registry_init(&port_range_registry, memory_context);

	if (collect_port_range_values(
		    port_ranges, port_range_count, table, &port_range_registry
	    )) {
		goto error_port_range_registry;
	}

	for (const struct filter_rule **action_ptr = actions;
	     action_ptr < actions + count;
	     ++action_ptr) {
		// A value range should be created even for empty rules
		value_registry_start(registry);
		if (*action_ptr == NULL)
			continue;
		const struct filter_rule *action = *action_ptr;

		struct filter_port_range *port_ranges;
		uint32_t port_range_count;
		get_port_range(action, &port_ranges, &port_range_count);
		for (struct filter_port_range *ports = port_ranges;
		     ports < port_ranges + port_range_count;
		     ++ports) {
			uint32_t port_range_idx = radix_lookup(
				&port_range_radix,
				4, (uint8_t *)ports
			);

			struct value_range *rng =
				ADDR_OF(&port_range_registry.ranges) + port_range_idx;
			uint32_t *vls = ADDR_OF(&rng->values);
			for (uint32_t idx = 0; idx < rng->count; ++idx) {
				if (value_registry_collect(
					    registry, vls[idx]
				    )) {
					goto error_registry;
				}
			}
		}

		// FIXME: shoud we do that?
		// Handle default - the full range
		if (!port_range_count) {
			for (uint32_t port = 0; port <= 65535; ++port) {
				if (value_registry_collect(
					    registry,
					    value_table_get(table, 0, port)
				    )) {
					goto error_registry;
				}
			}
		}
	}

	radix_free(&port_range_radix);
	value_registry_fini(&port_range_registry);
	for (uint32_t idx = 0; idx < port_range_group_count; ++idx) {
		struct value_range *range = port_range_groups + idx;
		mem_array_free_exp(
			memory_context,
			ADDR_OF(&range->values),
			sizeof(uint32_t),
			range->count
		);
	}

	memory_bfree(
		memory_context,
		port_range_groups,
		sizeof(struct value_range) * port_range_group_count
	);

	mem_array_free_exp(memory_context, port_ranges, sizeof(*port_ranges), port_range_count);

	return 0;

error_registry:
	value_registry_fini(registry);

error_port_range_registry:
	value_registry_fini(&port_range_registry);

error_table:
	value_table_free(table);

error_info:
	for (uint32_t idx = 0; idx < port_range_group_count; ++idx) {
		struct value_range *range = port_range_groups + idx;
		mem_array_free_exp(
			memory_context,
			ADDR_OF(&range->values),
			sizeof(uint32_t),
			range->count
		);
	}


	memory_bfree(
		memory_context,
		port_range_groups,
		sizeof(struct value_range) * port_range_group_count
	);

	mem_array_free_exp(memory_context, port_ranges, sizeof(*port_ranges), port_range_count);

	radix_free(&port_range_radix);
	return -1;
}

static void
get_port_range_src(
	const struct filter_rule *action,
	struct filter_port_range **ranges,
	uint32_t *count
) {
	*ranges = action->transport.srcs;
	*count = action->transport.src_count;
}

static void
get_port_range_dst(
	const struct filter_rule *action,
	struct filter_port_range **ranges,
	uint32_t *count
) {
	*ranges = action->transport.dsts;
	*count = action->transport.dst_count;
}

////////////////////////////////////////////////////////////////////////////////

int
FILTER_ATTR_COMPILER_INIT_FUNC(port_dst)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **actions,
	size_t actions_count,
	struct memory_context *memory_context
) {
	struct value_table *table =
		memory_balloc(memory_context, sizeof(struct value_table));
	if (table == NULL) {
		return -1;
	}
	SET_OFFSET_OF(data, table);
	if (collect_port_values(
		    memory_context,
		    actions,
		    actions_count,
		    get_port_range_dst,
		    table,
		    registry
	    )) {
		SET_OFFSET_OF(data, NULL);
		memory_bfree(memory_context, table, sizeof(struct value_table));
		return -1;
	}
	return 0;
}

int
FILTER_ATTR_COMPILER_INIT_FUNC(port_src)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **actions,
	size_t actions_count,
	struct memory_context *memory_context
) {
	struct value_table *table =
		memory_balloc(memory_context, sizeof(struct value_table));
	if (table == NULL) {
		return -1;
	}
	SET_OFFSET_OF(data, table);
	if (collect_port_values(
		    memory_context,
		    actions,
		    actions_count,
		    get_port_range_src,
		    table,
		    registry
	    )) {
		SET_OFFSET_OF(data, NULL);
		memory_bfree(memory_context, table, sizeof(struct value_table));
		return -1;
	}

	return 0;
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(port_src)(
	void *data, struct memory_context *memory_context
) {
	struct value_table *table = (struct value_table *)data;
	if (table == NULL)
		return;

	value_table_free(table);
	memory_bfree(memory_context, table, sizeof(struct value_table));
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(port_dst)(
	void *data, struct memory_context *memory_context
) {
	struct value_table *table = (struct value_table *)data;
	if (table == NULL)
		return;

	value_table_free(table);
	memory_bfree(memory_context, table, sizeof(struct value_table));
}
