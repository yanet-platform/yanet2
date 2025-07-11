#pragma once

#include <stdint.h>

#include "common/network.h"

#define ACTION_NON_TERMINATE 0x80000000

struct filter_net6 {
	uint32_t src_count;
	uint32_t dst_count;

	// IPv6 + mask in little-endian
	struct net6 *srcs;
	struct net6 *dsts;
};

struct filter_net4 {
	uint32_t src_count;
	uint32_t dst_count;

	// IPv4 + mask in little-endian
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
	uint32_t action;
};