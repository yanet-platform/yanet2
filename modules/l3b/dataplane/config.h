#pragma once

#include "common/network.h"
#include "controlplane/config/cp_module.h"
#include "filter/filter.h"
#include "filter/rule.h"

enum real_state {
	real_state_disabled = 0,
	real_state_enabled = 1,
};

/*
 * A single backend (real server) reachable through an IP-in-IP tunnel.
 *
 * The address family of the tunnel is selected by type; source_net and
 * destination_addr carry the matching union member.
 */
struct real_server {
	// Outer tunnel address family (ip_family_ip4 or ip_family_ip6).
	enum ip_family type;
	// Source network used to derive the outer source address.
	struct net source_net;
	// Real server destination address used as the outer destination.
	struct net_addr destination_addr;
	// Whether the server is eligible to receive traffic.
	enum real_state state;
};

/*
 * Source-side classification criteria of a virtual service.
 *
 * Each array is a relative pointer into module shared memory and is paired
 * with a matching count field. A packet matches when it falls into any of the
 * listed networks and port ranges.
 */
struct source_filter {
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
struct real_ring {
	uint32_t *server_indexes;
	uint32_t size;
};

/*
 * A virtual service exposed to clients.
 *
 * Incoming traffic that matches one of the per-family filters is dispatched
 * to a real server chosen through real_ring by a hash-derived scheduler.
 *
 * Contract: the controlplane must filter_init both filter_ip4 and filter_ip6
 * before publishing a virtual service; the dataplane queries them directly
 * (value_table_get assumes a non-NULL backing table).
 */
struct virtual_service {
	// Backends available for this service.
	uint32_t real_server_count;
	struct real_server *real_servers;

	// Scheduler index ring over the real_servers array.
	struct real_ring real_ring;

	// Masks applied to the packet hash to derive a ring slot.
	uint32_t scheduler_hash_mask;
	uint32_t scheduler_index_mask;

	// Per-family classification of incoming packets.
	struct filter filter_ip6;
	struct filter filter_ip4;
};

/*
 * Top-level l3b module configuration published into shared memory.
 *
 * The module-level filters classify an incoming packet into a virtual service
 * index; virtual_services holds the services themselves.
 *
 * The filter query returns the index of the matched destination filter rule;
 * virtual_service_indexes maps that rule index to a virtual service index.
 *
 * Contract: as long as virtual_service_count is greater than zero, the
 * controlplane must filter_init both filter_ip6 and filter_ip4 — the
 * dataplane queries them whenever at least one service exists.
 */
struct module_config {
	struct cp_module cp_module;

	uint32_t virtual_service_count;
	// Array of relative pointers, one per service. Each service is
	// allocated independently so a single service can be installed or
	// replaced by swapping its slot without rebuilding the array or
	// touching the module config.
	struct virtual_service **virtual_services;

	// One virtual service index per destination filter rule.
	uint32_t virtual_service_index_count;
	uint32_t *virtual_service_indexes;

	struct filter filter_ip6;
	struct filter filter_ip4;
};
