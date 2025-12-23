#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"
#include "declare.h"
#include "filter/rule.h"

#include <stdint.h>

typedef void (*action_get_port_range_func)(
	const struct filter_rule *action,
	struct filter_port_range **ranges,
	uint32_t *count
);

static int
collect_port_values(
	struct memory_context *memory_context,
	const struct filter_rule *actions,
	uint32_t count,
	action_get_port_range_func get_port_range,
	struct value_table *table,
	struct value_registry *registry
) {
	if (value_table_init(table, memory_context, 1, 65536))
		return -1;

	for (const struct filter_rule *action = actions;
	     action < actions + count;
	     ++action) {

		value_table_new_gen(table);

		struct filter_port_range *port_ranges;
		uint32_t port_range_count;
		get_port_range(action, &port_ranges, &port_range_count);
		for (struct filter_port_range *ports = port_ranges;
		     ports < port_ranges + port_range_count;
		     ++ports) {
			if (ports->to - ports->from == 65535)
				continue;
			for (uint32_t port = ports->from; port <= ports->to;
			     ++port) {
				value_table_touch(table, 0, port);
			}
		}
	}

	value_table_compact(table);

	for (const struct filter_rule *action = actions;
	     action < actions + count;
	     ++action) {
		value_registry_start(registry);

		struct filter_port_range *port_ranges;
		uint32_t port_range_count;
		get_port_range(action, &port_ranges, &port_range_count);
		for (struct filter_port_range *ports = port_ranges;
		     ports < port_ranges + port_range_count;
		     ++ports) {
			for (uint32_t port = ports->from; port <= ports->to;
			     ++port) {
				value_registry_collect(
					registry,
					value_table_get(table, 0, port)
				);
			}
		}

		// Handle default - the full range
		if (!port_range_count) {
			for (uint32_t port = 0; port <= 65535; ++port) {
				value_registry_collect(
					registry,
					value_table_get(table, 0, port)
				);
			}
		}
	}

	return 0;
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
	const struct filter_rule *actions,
	size_t actions_count,
	struct memory_context *memory_context
) {
	struct value_table *table =
		memory_balloc(memory_context, sizeof(struct value_table));
	if (table == NULL) {
		return -1;
	}
	SET_OFFSET_OF(data, table);
	return collect_port_values(
		memory_context,
		actions,
		actions_count,
		get_port_range_dst,
		table,
		registry
	);
}

int
FILTER_ATTR_COMPILER_INIT_FUNC(port_src)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *actions,
	size_t actions_count,
	struct memory_context *memory_context
) {
	struct value_table *table =
		memory_balloc(memory_context, sizeof(struct value_table));
	if (table == NULL) {
		return -1;
	}
	SET_OFFSET_OF(data, table);
	return collect_port_values(
		memory_context,
		actions,
		actions_count,
		get_port_range_src,
		table,
		registry
	);
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