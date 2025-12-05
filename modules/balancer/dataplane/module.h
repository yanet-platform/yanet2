#pragma once

#include "controlplane/config/cp_module.h"
#include "controlplane/config/econtext.h"
#include "counters/counters.h"
#include "filter/filter.h"
#include <assert.h>
#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////

struct session_table;
struct virtual_service;
struct real;

////////////////////////////////////////////////////////////////////////////////

struct balancer_module_config {
	// hook for the controlplane
	struct cp_module cp_module;

	// relative pointer to persistent state of the balancer
	struct balancer_state *state;

	// mapping: (address, port, proto) -> vs_id
	struct filter vs_v4_table;
	struct filter vs_v6_table;

	// virtual services
	size_t vs_count;
	struct virtual_service *vs;

	// reals
	size_t real_count;
	struct real *reals;

	// counter index
	uint64_t counter_id;

	// icmp counter id
	uint64_t icmp_counter_id;

	// l4 packet counter id
	uint64_t l4_counter_id;

	// if packet destination id is from decap list,
	// then we make decap
	struct lpm decap_filter_v4;
	struct lpm decap_filter_v6;

	// source address of the balancer
	uint8_t source_ip[NET4_LEN];
	uint8_t source_ip_v6[NET6_LEN];
};

////////////////////////////////////////////////////////////////////////////////

static inline struct balancer_common_module_stats *
get_module_counter(
	struct balancer_module_config *config,
	size_t worker,
	struct counter_storage *storage
) {
	uint64_t *counter =
		counter_get_address(config->counter_id, worker, storage);
	return (struct balancer_common_module_stats *)counter;
}

static inline struct balancer_icmp_module_stats *
get_icmp_module_counter(
	struct balancer_module_config *config,
	size_t worker,
	struct counter_storage *storage
) {
	uint64_t *counter =
		counter_get_address(config->icmp_counter_id, worker, storage);
	return (struct balancer_icmp_module_stats *)counter;
}

static inline struct balancer_l4_module_stats *
get_l4_module_counter(
	struct balancer_module_config *config,
	size_t worker,
	struct counter_storage *storage
) {
	uint64_t *counter =
		counter_get_address(config->l4_counter_id, worker, storage);
	return (struct balancer_l4_module_stats *)counter;
}