#pragma once

#include <stdint.h>

#include "common/network.h"

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