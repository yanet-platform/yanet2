#pragma once

#include "common/registry.h"
#include "lib/dataplane/packet/packet.h"

#include "rule.h"

#define MAX_ATTRIBUTES 10

// This function is provided by user.
// It should initialize user-defined data-structure for
// classifying packet and initialize registry according to
// the following rules:
// 	1. i-th registry range corresponds to the i-th action
// 	2. values for the i-th range corresponds to the classifiers from i-th
// action
typedef int (*attr_init_func)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *actions,
	size_t actions_count,
	struct memory_context *memory_context
);

typedef uint32_t (*attr_lookup_func)(struct packet *packet, void *data);

typedef void (*attr_free_func)(
	void *data, struct memory_context *memory_context
);

struct filter_attribute {
	attr_init_func init_func;
	attr_lookup_func lookup_func;
	attr_free_func free_func;
};

// src port
static inline uint32_t
lookup_port_src(struct packet *packet, void *data) {
	(void)packet;
	struct value_table *table = data;
	return value_table_get(table, 0, packet_src_port(packet));
}

typedef void (*action_get_port_range_func)(
	const struct filter_rule *action,
	struct filter_port_range **ranges,
	uint32_t *count
);

static inline int
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
	}

	return 0;
}

static inline void
get_port_range_src(
	const struct filter_rule *action,
	struct filter_port_range **ranges,
	uint32_t *count
) {
	*ranges = action->transport.srcs;
	*count = action->transport.src_count;
}

static inline void
get_port_range_dst(
	const struct filter_rule *action,
	struct filter_port_range **ranges,
	uint32_t *count
) {
	*ranges = action->transport.dsts;
	*count = action->transport.dst_count;
}

static inline uint32_t
lookup_port_dst(struct packet *packet, void *data) {
	struct value_table *table = data;
	return value_table_get(table, 0, packet_dst_port(packet));
}

static inline int
init_port_dst(
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

static inline int
init_port_src(
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

static inline void
free_port(void *data, struct memory_context *memory_context) {
	(void)memory_context;

	struct value_table *table = (struct value_table *)data;
	value_table_free(table);
}

const static struct filter_attribute attribute_port_src = {
	init_port_src, lookup_port_src, free_port
};

// dst port
const static struct filter_attribute attribute_port_dst = {
	init_port_dst, lookup_port_dst, free_port
};

// proto
extern const struct filter_attribute attribute_proto;

// IPv4
extern const struct filter_attribute attribute_net4_src;
extern const struct filter_attribute attribute_net4_dst;

// IPv6
// TODO

// vlan
extern const struct filter_attribute attribute_vlan;