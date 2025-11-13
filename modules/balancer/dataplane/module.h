#pragma once

#include "controlplane/config/cp_module.h"
#include "controlplane/config/econtext.h"
#include "counters/counters.h"
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

	uint64_t packets_counter_id;
	uint64_t bytes_counter_id;
};

////////////////////////////////////////////////////////////////////////////////

struct module_config_packets_counter {
	uint64_t in;
	uint64_t select_vs_failed;
	uint64_t invalid_packet;
	uint64_t select_real_failed;
	uint64_t tunnel_failed;
	uint64_t out;
};

#define BALANCER_MODULE_PACKETS_COUNTER_SIZE                                   \
	(sizeof(struct module_config_packets_counter) / sizeof(uint64_t))

static inline struct module_config_packets_counter *
balancer_module_config_packets_counter(
	struct balancer_module_config *config,
	size_t worker,
	struct counter_storage *storage
) {
	uint64_t *counters = counter_get_address(
		config->packets_counter_id, worker, storage
	);
	return (struct module_config_packets_counter *)counters;
}

////////////////////////////////////////////////////////////////////////////////

struct module_config_bytes_counter {
	uint64_t in;
	uint64_t out;
};

#define BALANCER_MODULE_BYTES_COUNTER_SIZE                                     \
	(sizeof(struct module_config_bytes_counter) / sizeof(uint64_t))

static inline struct module_config_bytes_counter *
balancer_module_config_bytes_counter(
	struct balancer_module_config *config,
	size_t worker,
	struct counter_storage *storage
) {
	uint64_t *counters =
		counter_get_address(config->bytes_counter_id, worker, storage);
	return (struct module_config_bytes_counter *)counters;
}
