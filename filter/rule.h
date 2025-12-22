#pragma once

#include <stdbool.h>
#include <stdint.h>

#include "common/network.h"

////////////////////////////////////////////////////////////////////////////////

#define ACTION_MASK ((uint32_t)0xFFFF)
#define ACTION_NON_TERMINATE ((uint32_t)0x8000)
#define CATEGORY_SHIFT ((uint32_t)16)
#define MAKE_ACTION_CATEGORY_MASK(category_mask)                               \
	((uint32_t)(category_mask) << CATEGORY_SHIFT)

#define ACL_DEVICE_NAME_LEN 80

////////////////////////////////////////////////////////////////////////////////

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

struct filter_net6s {
	struct net6 *items;
	uint32_t count;
};

struct filter_net4s {
	struct net4 *items;
	uint32_t count;
};

struct filter_port_range {
	uint16_t from;
	uint16_t to;
};

#define PROTO_UNSPEC ((uint8_t)-1)

struct filter_proto {
	uint8_t proto;	       // 1 ICMP, 6 TCP, 17 UDP
	uint16_t enable_bits;  // only for TCP
	uint16_t disable_bits; // only for TCP
};

struct filter_proto_range {
	uint16_t from;
	uint16_t to;
};

struct filter_transport {
	uint16_t proto_count;
	struct filter_proto_range *protos;

	// deprecated
	struct filter_proto proto;

	uint16_t src_count;
	struct filter_port_range *srcs;

	uint16_t dst_count;
	struct filter_port_range *dsts;
};

struct filter_device {
	char name[ACL_DEVICE_NAME_LEN];
	uint64_t id;
};

struct filter_devices {
	struct filter_device *items;
	uint32_t count;
};

struct filter_vlan_range {
	uint16_t from;
	uint16_t to;
};

struct filter_vlan_ranges {
	struct filter_vlan_range *items;
	uint32_t count;
};

struct filter_proto_ranges {
	struct filter_proto_range *items;
	uint32_t count;
};

struct filter_port_ranges {
	struct filter_port_range *items;
	uint32_t count;
};

#define VLAN_UNSPEC ((uint16_t)-1)

struct filter_rule {
	struct filter_net6 net6;
	struct filter_net4 net4;
	struct filter_transport transport;
	uint16_t device_count;
	struct filter_device *devices;

	uint16_t vlan_range_count;
	struct filter_vlan_range *vlan_ranges;

	uint16_t vlan;

	// first 15 bits are for user action
	// 16th bit is for terminate flag
	// the oldest 16 bits are for category mask,
	// which is 0 if rule is for all categories.
	uint32_t action;
};

////////////////////////////////////////////////////////////////////////////////

#define FILTER_ACTION_CATEGORY_MASK(action)                                    \
	((uint16_t)((action) >> CATEGORY_SHIFT))
#define FILTER_ACTION_TERMINATE(action) (((action) >> (15)) == 0)

////////////////////////////////////////////////////////////////////////////////

static inline uint32_t
filter_action_create(
	uint16_t category_mask, bool non_terminate_flag, uint16_t user_action
) {
	return ((uint32_t)category_mask) << CATEGORY_SHIFT |
	       ((uint32_t)non_terminate_flag) << 15 | user_action;
}
