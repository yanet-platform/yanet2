#pragma once

#include "session.h"
#include <controlplane/config/cp_module.h>

#include <filter/filter.h>

////////////////////////////////////////////////////////////////////////////////

struct balancer_module_config {
	struct cp_module cp_module;

	// src_ip v4 + port
	// struct lpm v4_service_lookup;
	struct filter v4_service_lookup;

	// src ip v6 + port
	// struct lpm v6_service_lookup;
	struct filter v6_service_lookup;

	struct balancer_session_timeouts timeouts;

	// Relative pointer, externally configured
	struct balancer_state *state;

	uint64_t vs_count;
	struct balancer_vs **services;

	uint64_t real_count;
	struct balancer_rs *reals;
};
