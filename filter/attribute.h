#pragma once

#include "common/registry.h"
#include "lib/dataplane/packet/packet.h"

#include "attribute/net4.h"
#include "attribute/port.h"
#include "attribute/proto.h"
#include "attribute/vlan.h"
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

static const struct filter_attribute attribute_port_src = {
	init_port_src, lookup_port_src, free_port
};

// dst port
static const struct filter_attribute attribute_port_dst = {
	init_port_dst, lookup_port_dst, free_port
};

// proto
static const struct filter_attribute attribute_proto = {
	init_proto, lookup_proto, free_proto
};

// IPv4
static const struct filter_attribute attribute_net4_src = {
	init_net4_src, lookup_net4_src, free_net4
};

static const struct filter_attribute attribute_net4_dst = {
	init_net4_dst, lookup_net4_dst, free_net4
};

// IPv6
// TODO

// vlan
static const struct filter_attribute attribute_vlan = {
	init_vlan, lookup_vlan, free_vlan
};