#pragma once

#include "common/network.h"
#include "filter/filter.h"
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

/*
 * Round-robin index ring used by the scheduler to pick a real server.
 *
 * server_indexes is a relative pointer to an array of real_server array
 * indexes; size is the number of entries the ring holds.
 */
struct l3b_real_ring {
	uint32_t *server_indexes;
	uint32_t size;
};

/*
 * A virtual service exposed to clients.
 *
 * Incoming traffic that matches one of the per-family filters is dispatched
 * to a real server chosen through real_ring by a hash-derived scheduler.
 */
struct l3b_virtual_service {
	// Backends available for this service.
	uint32_t real_server_count;
	struct l3b_real_server *real_servers;

	// Scheduler index ring over the real_servers array.
	struct l3b_real_ring real_ring;

	// Masks applied to the packet hash to derive a ring slot.
	uint32_t scheduler_hash_mask;
	uint32_t scheduler_index_mask;

	// Per-family classification of incoming packets.
	struct filter filter_ip6;
	struct filter filter_ip4;
};
