#pragma once

#include "common/registry.h"
#include "lib/dataplane/packet/packet.h"

#include "attribute/net4.h"
#include "attribute/net6.h"
#include "attribute/port.h"
#include "attribute/proto.h"
#include "attribute/vlan.h"
#include "rule.h"

////////////////////////////////////////////////////////////////////////////////

#define MAX_ATTRIBUTES 10

////////////////////////////////////////////////////////////////////////////////

// This function is provided by user.
// It should initialize user-defined data-structure for
// classifying packet attribute and initialize value registry
// according to the following rules:
// 	1. i-th registry range corresponds to the i-th rule;
// 	2. values for the i-th range corresponds to the classifiers from i-th
// rule.
//
// This function returns value should be negative in case of error,
// and zero on success.
typedef int (*attr_init_func)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *rules,
	size_t rule_count,
	struct memory_context *memory_context
);

// This function is provided by user and should classify packet attribute.
// It takes packet and user data initialized in `attr_init_func`.
// Returns classifier of the packet attribute.
typedef uint32_t (*attr_query_func)(struct packet *packet, void *data);

// This function allows to free user data, initialzed in `attr_init_func`.
typedef void (*attr_free_func)(
	void *data, struct memory_context *memory_context
);

// Corresponds to the attribute of the packet
// (transport protocol, IPv4 source address, etc).
struct filter_attribute {
	attr_init_func init_func;
	attr_query_func query_func;
	attr_free_func free_func;
};

////////////////////////////////////////////////////////////////////////////////
// Transport
////////////////////////////////////////////////////////////////////////////////

// Source port
static const struct filter_attribute attribute_port_src = {
	init_port_src, lookup_port_src, free_port
};

// Destination port
static const struct filter_attribute attribute_port_dst = {
	init_port_dst, lookup_port_dst, free_port
};

// Packet protocol and flags
static const struct filter_attribute attribute_proto = {
	init_proto, lookup_proto, free_proto
};

////////////////////////////////////////////////////////////////////////////////
// IPv4
////////////////////////////////////////////////////////////////////////////////

// IPv4 source address
static const struct filter_attribute attribute_net4_src = {
	init_net4_src, lookup_net4_src, free_net4
};

// IPv4 destination address
static const struct filter_attribute attribute_net4_dst = {
	init_net4_dst, lookup_net4_dst, free_net4
};

////////////////////////////////////////////////////////////////////////////////
// IPv6
////////////////////////////////////////////////////////////////////////////////

// IPv6 source address
static const struct filter_attribute attribute_net6_src = {
	init_net6_src, lookup_net6_src, free_net6
};

// IPv6 destination address
static const struct filter_attribute attribute_net6_dst = {
	init_net6_dst, lookup_net6_dst, free_net6
};

////////////////////////////////////////////////////////////////////////////////
// VLAN
////////////////////////////////////////////////////////////////////////////////

static const struct filter_attribute attribute_vlan = {
	init_vlan, lookup_vlan, free_vlan
};