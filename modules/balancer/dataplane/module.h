#pragma once

#include "controlplane/config/cp_module.h"
#include "controlplane/config/econtext.h"
#include "counters/counters.h"
#include "filter/filter.h"
#include <assert.h>

////////////////////////////////////////////////////////////////////////////////

struct session_table;
struct virtual_service;
struct real;

////////////////////////////////////////////////////////////////////////////////

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

	uint64_t counter_id;
};

////////////////////////////////////////////////////////////////////////////////

static inline struct module_config_counter *
balancer_module_config_counter(
	struct balancer_module_config *config,
	size_t worker,
	struct counter_storage *storage
) {
	uint64_t *counter =
		counter_get_address(config->counter_id, worker, storage);
	return (struct module_config_counter *)counter;
}