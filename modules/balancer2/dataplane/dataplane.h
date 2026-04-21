#pragma once

#include <stddef.h>

#include "lib/controlplane/config/cp_module.h"

#include "types/session.h"

struct balancer_session_table;
struct balancer_vs;
struct filter;

struct module *
new_module_balancer2();

struct balancer_config {
	struct cp_module cp_module;

	uint64_t common_counter_id;
	uint64_t l4_counter_id;

	struct balancer_session_table *session_table;

	struct filter *ipv4_vs_matcher;
	struct filter *ipv6_vs_matcher;

	struct balancer_vs *vs;
	uint32_t vs_count;

	struct balancer_session_timeouts session_timeouts;

	/*
	 * RCU guard for the inner atomic changes on the config,
	 * including changes on reals ring of virtual services.
	 */
	rcu_t rcu;
};

struct dp_worker;
struct packet_front;

void
balancer2_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
);
