#pragma once

#include <stdint.h>

#include "lpm.h"
#include "network.h"
#include "registry.h"

#define ACTION_NON_TERMINATE 0x80000000

struct filter_net6 {
	uint32_t src_count;
	uint32_t dst_count;
	struct net6 *srcs;
	struct net6 *dsts;
};

struct filter_net4 {
	uint32_t src_count;
	uint32_t dst_count;
	struct net4 *srcs;
	struct net4 *dsts;
};

struct filter_port_range {
	uint16_t from;
	uint16_t to;
};

struct filter_transport {
	uint16_t proto_flags;
	uint16_t src_count;
	uint16_t dst_count;
	struct filter_port_range *srcs;
	struct filter_port_range *dsts;
};

struct filter_action {
	struct filter_net6 net6;
	struct filter_net4 net4;
	struct filter_transport transport;
	uint32_t action;
};

struct filter_compiler {
	struct lpm src_net4;
	struct lpm dst_net4;
	struct value_table src_port4;
	struct value_table dst_port4;

	struct lpm src_net6_hi;
	struct lpm src_net6_lo;
	struct lpm dst_net6_hi;
	struct lpm dst_net6_lo;
	struct value_table src_port6;
	struct value_table dst_port6;

	struct {
		struct value_table network;
		struct value_table transport_port;
		struct value_table result;
		struct value_registry result_registry;
	} v4_lookups;

	struct {
		struct value_table network_hi;
		struct value_table network_lo;
		struct value_table network;
		struct value_table transport_port;
		struct value_table result;
		struct value_registry result_registry;
	} v6_lookups;
};

int
filter_compiler_init(
	struct filter_compiler *compiler,
	struct filter_action *actions,
	uint32_t count
);
