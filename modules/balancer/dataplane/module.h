#pragma once

#include "controlplane/config/cp_module.h"
#include "filter/filter.h"

#include "../state/state.h"

////////////////////////////////////////////////////////////////////////////////

struct session_table;
struct virtual_service;
struct real;

struct balancer_module_config {
	struct cp_module cp_module;

	// relative pointer to persistent state of the balancer
	struct balancer_state *state;

	// mapping: (address, port, proto) -> vs_id
	struct filter vs_v4_table;
	struct filter vs_v6_table;

	size_t vs_count;
	struct virtual_service *vs;

	size_t real_count;
	struct real *reals;
};