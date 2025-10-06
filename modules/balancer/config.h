#pragma once

#include "ring.h"
#include "state.h"

#include <common/lpm.h>
#include <controlplane/config/cp_module.h>

#include <filter/filter.h>

struct balancer_vs_port_range {
	uint16_t from;
	uint16_t to;
};

struct balancer_vs {
	uint64_t flags;
	
	uint8_t address[16];

	struct balancer_vs_port_range *port_ranges;
	size_t port_range_count;

	uint64_t real_start;
	uint64_t real_count;

	struct lpm src_filter;

	struct ring real_ring;
};

struct balancer_rs {
	uint64_t flags;
	uint16_t weight;
	uint8_t dst_addr[16];
	uint8_t src_addr[16];
	uint8_t src_mask[16];
};

struct balancer_state_config {
	uint32_t tcp_syn_ack_timeout;
	uint32_t tcp_syn_timeout;
	uint32_t tcp_fin_timeout;
	uint32_t tcp_timeout;
	uint32_t udp_timeout;
	uint32_t default_timeout;
	uint32_t sessions_to_reserve;
};

struct balancer_module_config {
	struct cp_module cp_module;

	// src_ip v4 + port
	// struct lpm v4_service_lookup;
	struct filter v4_service_lookup;

	// src ip v6 + port
	// struct lpm v6_service_lookup;
	struct filter v6_service_lookup;

	struct balancer_state state;

	struct balancer_state_config state_config;

	// FIXME: Should we store array of `balancer_vs` instead of `balancer_vs
	// *`?
	uint64_t service_count;
	struct balancer_vs **services;

	uint64_t real_count;
	struct balancer_rs *reals;
};
