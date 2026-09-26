/*
 * Rule condition value views shared by the rule compiling libraries:
 * the lists and intervals a module rule addresses its match
 * conditions through, exchanged between the module rules and the
 * attribute compilers.
 *
 * Both the legacy lib/filter rule format and the lib/classify
 * attribute getters consume these views, so the declarations live
 * here once: a translation unit combining module APIs of the two
 * libraries compiles against one definition set.
 */

#pragma once

#include <stdint.h>

#include "common/network.h"

#define ACL_DEVICE_NAME_LEN 80

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

struct filter_proto_range {
	uint16_t from;
	uint16_t to;
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

// IP fragmentation constraint of a rule.
//
// ANY matches every packet regardless of fragmentation; NONE matches only
// non-fragmented packets; FRAG matches only fragments (offset > 0).
enum filter_ip_fragment {
	FILTER_IP_FRAG_ANY = 0,
	FILTER_IP_FRAG_NONE = 1,
	FILTER_IP_FRAG_FRAG = 2,
};
