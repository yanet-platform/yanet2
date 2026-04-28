#pragma once

#include "filter/filter.h"

#include "lib/controlplane/config/cp_module.h"

#include "types/session.h"

#include "common/rcu.h"

struct balancer_session_table_chain;
struct virtual_service;

struct balancer_module_config {
	struct cp_module cp_module;

	uint64_t common_counter_id;
	uint64_t l4_counter_id;

	struct filter vs_matcher_ip4;
	struct filter vs_matcher_ip6;

	struct virtual_service *vs;
	uint32_t vs_count;

	struct balancer_session_timeouts session_timeouts;

	struct balancer_session_table_chain *st_chain;

	/*
	 * RCU guard for the inner atomic changes on the config,
	 * including changes on reals ring of virtual services.
	 */
	rcu_t rcu;
};