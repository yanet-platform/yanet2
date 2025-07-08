#include "../attribute.h"

typedef void (*action_get_port_range_func)(
	const struct filter_action *action,
	struct filter_port_range **ranges,
	uint32_t *count
);

static inline int
collect_port_values(
	struct memory_context *memory_context,
	const struct filter_action *actions,
	uint32_t count,
	action_get_port_range_func get_port_range,
	struct value_table *table,
	struct value_registry *registry
) {
	if (value_table_init(table, memory_context, 1, 65536))
		return -1;

	for (const struct filter_action *action = actions;
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
			for (uint32_t port = be16toh(ports->from);
			     port <= be16toh(ports->to);
			     ++port) {
				value_table_touch(table, 0, htobe16(port));
			}
		}
	}

	value_table_compact(table);

	if (value_registry_init(registry, memory_context))
		goto error_reg;

	for (const struct filter_action *action = actions;
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
	}

	return 0;

error_reg:
	value_table_free(table);
	return -1;
}

void
get_port_range_src(
	const struct filter_action *action,
	struct filter_port_range **ranges,
	uint32_t *count
) {
	*ranges = action->transport.srcs;
	*count = action->transport.src_count;
}

void
get_port_range_dst(
	const struct filter_action *action,
	struct filter_port_range **ranges,
	uint32_t *count
) {
	*ranges = action->transport.dsts;
	*count = action->transport.dst_count;
}

int
init_src_port(
	struct value_registry *registry,
	void **data,
	const struct filter_action *actions,
	size_t actions_count,
	struct memory_context *memory_context
) {
	struct value_table *table =
		memory_balloc(memory_context, sizeof(struct value_table));
	if (table == NULL) {
		return -1;
	}
	*data = (void *)table;
	return collect_port_values(
		memory_context,
		actions,
		actions_count,
		get_port_range_src,
		table,
		registry
	);
}

uint32_t
lookup_src_port(struct packet *packet, void *data) {
	struct value_table *table = data;
	return value_table_get(table, 0, packet_src_port(packet));
}

uint32_t
lookup_dst_port(struct packet *packet, void *data) {
	struct value_table *table = data;
	return value_table_get(table, 0, packet_dst_port(packet));
}

int
init_dst_port(
	struct value_registry *registry,
	void **data,
	const struct filter_action *actions,
	size_t actions_count,
	struct memory_context *memory_context
) {
	struct value_table *table =
		memory_balloc(memory_context, sizeof(struct value_table));
	if (table == NULL) {
		return -1;
	}
	*data = (void *)table;
	return collect_port_values(
		memory_context,
		actions,
		actions_count,
		get_port_range_dst,
		table,
		registry
	);
}

struct filter_attribute attribute_port_src = {init_src_port, lookup_src_port};

struct filter_attribute attribute_port_dst = {init_dst_port, lookup_dst_port};