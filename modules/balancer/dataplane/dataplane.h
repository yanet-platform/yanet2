#pragma once

#include <stddef.h>

#include <filter.h>

#include "lib/controlplane/config/cp_module.h"
#include "lib/dataplane/module/module.h"

#include "common/network.h"

#include "types/session.h"

struct balancer_session_table;
struct balancer_vs;

struct module *
new_module_balancer();

struct balancer_packet_handler {
	struct cp_module cp_module;

	uint64_t common_counter_id;
	uint64_t icmp_v4_counter_id;
	uint64_t icmp_v6_counter_id;
	uint64_t l4_counter_id;

	struct filter *decap_ipv4_filter;
	struct filter *decap_ipv6_filter;

	struct balancer_session_table *session_table;

	struct filter *ipv4_vs_matcher;
	struct filter *ipv6_vs_matcher;

	struct balancer_vs *vs;
	uint32_t vs_count;

	struct balancer_session_timeouts session_timeouts;

	struct net4_addr source_v4;
	struct net6_addr source_v6;

	/*
	 * RCU guard for the inner atomic changes on the packet handler.
	 * It includes changes on reals ring of virtual services.
	 */
	rcu_t rcu;

	/* ---Controlplane data --- */
	/* No padding needed, as rcu has 64 bytes alignment */

	struct net4_addr *decap_v4;
	uint32_t decap_v4_count;

	struct net6_addr *decap_v6;
	uint32_t decap_v6_count;

	uint32_t wlc_power;
	uint32_t wlc_max_weight;
	uint32_t refresh_period_ms;
	float session_table_max_load_factor;
};

void
balancer_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
);
