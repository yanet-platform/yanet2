#pragma once

#include <filter/filter.h>
#include <filter/query.h>

////////////////////////////////////////////////////////////////////////////////

// Declare filter for IPv4 nets

#define ACL_FILTER_NET4_TAG __ACL_FILTER_NET4_TAG

FILTER_QUERY_DECLARE(
	ACL_FILTER_NET4_TAG, proto_range, port_src, port_dst, net4_src, net4_dst
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

FILTER_QUERY_DECLARE(
	ACL_FILTER_NET6_TAG, proto_range, port_src, port_dst, net6_src, net6_dst
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
