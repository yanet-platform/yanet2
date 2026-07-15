#pragma once

#include "common/network.h"
#include "filter/rule.h"

enum l3b_real_state {
	l3b_real_state_disabled = 0,
	l3b_real_state_enabled = 1,
};

/*
 * A single backend (real server) reachable through an IP-in-IP tunnel.
 *
 * The address family of the tunnel is selected by type; source_net and
 * destination_addr carry the matching union member.
 */
struct l3b_real_server {
	// Outer tunnel address family (ip_family_ip4 or ip_family_ip6).
	enum ip_family type;
	// Source network used to derive the outer source address.
	struct net source_net;
	// Real server destination address used as the outer destination.
	struct net_addr destination_addr;
	// Whether the server is eligible to receive traffic.
	enum l3b_real_state state;
};

/*
 * Source-side classification criteria of a virtual service.
 *
 * Each array is a relative pointer into module shared memory and is paired
 * with a matching count field. A packet matches when it falls into any of the
 * listed networks and port ranges.
 */
struct l3b_source_filter {
	uint32_t net6_count;
	struct net6 *net6s;

	uint32_t net4_count;
	struct net4 *net4s;

	uint32_t port_range_count;
	struct filter_port_range *port_ranges;
};
