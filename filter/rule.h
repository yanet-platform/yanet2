#pragma once

#include <stdint.h>

#include "common/network.h"

#define ACTION_MASK ((uint32_t)0xffff)
#define ACTION_NON_TERMINATE ((uint32_t)0x8000)
#define CATEGORY_SHIFT ((uint32_t)16)
#define MAKE_ACTION_CATEGORY_MASK(category_mask)                               \
	((uint32_t)(category_mask) << CATEGORY_SHIFT)

struct filter_net6 {
	uint32_t src_count;
	uint32_t dst_count;

	// IPv6 + mask in little-endian
	// both masks must be prefix-consecutive
	// (like 11..100..0)
	struct net6 *srcs;
	struct net6 *dsts;
};

struct filter_net4 {
	uint32_t src_count;
	uint32_t dst_count;

	// IPv4 + mask in little-endian
	// mask must be prefix-consecutive
	// (like 11..100..0)
	struct net4 *srcs;
	struct net4 *dsts;
};

struct filter_port_range {
	// Range start in little-endian, inclusive
	uint16_t from;

	// Range end in little-endian, inclusive
	uint16_t to;
};

#define PROTO_UNSPEC ((uint8_t)-1)

struct filter_proto {
	uint8_t proto;	       // 1 ICMP, 16 TCP, 6 UDP
	uint16_t enable_bits;  // only for TCP
	uint16_t disable_bits; // only for TCP
};

struct filter_transport {
	struct filter_proto proto;
	uint16_t src_count;
	uint16_t dst_count;
	struct filter_port_range *srcs;
	struct filter_port_range *dsts;
};

#define VLAN_UNSPEC ((uint16_t)-1)

struct filter_rule {
	struct filter_net6 net6;
	struct filter_net4 net4;
	struct filter_transport transport;
	uint16_t vlan;

	// first 15 bits are for user action
	// 16th bit is for terminate flag
	// the oldest 16 bits are for category mask,
	// which is 0 if rule is for all categories.
	uint32_t action;
};