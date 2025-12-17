#pragma once

#include "lib/controlplane/config/cp_module.h"
#include "lib/controlplane/config/econtext.h"

#include "lib/counters/counters.h"

#include "filter/filter.h"

#include "../api/module.h"
#include "../api/stats.h"

#include <assert.h>
#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////

struct virtual_service;
struct real;

////////////////////////////////////////////////////////////////////////////////

struct balancer_module_config {
	// hook for the controlplane
	struct cp_module cp_module;

	// timeouts of sessions with different types
	struct balancer_sessions_timeouts sessions_timeouts;

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

	// counters
	struct {
		// common counter
		uint64_t common;

		// icmp v4 counter
		uint64_t icmp_v4;

		// icmp v6 counter
		uint64_t icmp_v6;

		// l4 (tcp and udp) counter
		uint64_t l4;
	} counter;

	// if packet destination id is from decap list,
	// then we make decap
	struct lpm decap_filter_v4;
	struct lpm decap_filter_v6;

	// source address of the balancer
	uint8_t source_ip[NET4_LEN];
	uint8_t source_ip_v6[NET6_LEN];

	// set of IP addresses announced by balancer
	struct lpm announce_ipv4;
	struct lpm announce_ipv6;
};

////////////////////////////////////////////////////////////////////////////////

static inline struct balancer_common_module_stats *
get_module_counter(
	struct balancer_module_config *config,
	size_t worker,
	struct counter_storage *storage
) {
	uint64_t *counter =
		counter_get_address(config->counter.common, worker, storage);
	return (struct balancer_common_module_stats *)counter;
}

static inline struct balancer_icmp_module_stats *
get_icmp_v4_module_counter(
	struct balancer_module_config *config,
	size_t worker,
	struct counter_storage *storage
) {
	uint64_t *counter =
		counter_get_address(config->counter.icmp_v4, worker, storage);
	return (struct balancer_icmp_module_stats *)counter;
}

static inline struct balancer_icmp_module_stats *
get_icmp_v6_module_counter(
	struct balancer_module_config *config,
	size_t worker,
	struct counter_storage *storage
) {
	uint64_t *counter =
		counter_get_address(config->counter.icmp_v6, worker, storage);
	return (struct balancer_icmp_module_stats *)counter;
}

static inline struct balancer_l4_module_stats *
get_l4_module_counter(
	struct balancer_module_config *config,
	size_t worker,
	struct counter_storage *storage
) {
	uint64_t *counter =
		counter_get_address(config->counter.l4, worker, storage);
	return (struct balancer_l4_module_stats *)counter;
}