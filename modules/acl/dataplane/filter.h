#pragma once

#include <filter/attribute.h>
#include <filter/attribute/net4.h>
#include <filter/attribute/net6.h>
#include <filter/attribute/port.h>
#include <filter/attribute/proto.h>
#include <filter/filter.h>

#include "../api/rule.h"

////////////////////////////////////////////////////////////////////////////////

// Declare filter for IPv4 nets

#define ACL_FILTER_NET4_TAG __ACL_FILTER_NET4_TAG

FILTER_DECLARE(
	ACL_FILTER_NET4_TAG,
	&attribute_proto_range,
	&attribute_port_src,
	&attribute_port_dst,
	&attribute_net4_src,
	&attribute_net4_dst
);

static inline int
net4_filter_query(
	struct filter *filter,
	struct packet *packet,
	uint32_t **actions,
	uint32_t *actions_count
) {
	return FILTER_QUERY(
		filter, ACL_FILTER_NET4_TAG, packet, actions, actions_count
	);
}

////////////////////////////////////////////////////////////////////////////////

// Declare filter for IPv6 nets

#define ACL_FILTER_NET6_TAG __ACL_FILTER_NET6_TAG

FILTER_DECLARE(
	ACL_FILTER_NET6_TAG,
	&attribute_proto_range,
	&attribute_port_src,
	&attribute_port_dst,
	&attribute_net6_src,
	&attribute_net6_dst
);

static inline int
net6_filter_query(
	struct filter *filter,
	struct packet *packet,
	uint32_t **actions,
	uint32_t *actions_count
) {
	return FILTER_QUERY(
		filter, ACL_FILTER_NET6_TAG, packet, actions, actions_count
	);
}

////////////////////////////////////////////////////////////////////////////////

static inline struct filter_rule *
acl_rules_into_filter_rules(acl_rule_t *rule) {
	return (struct filter_rule *)rule;
}