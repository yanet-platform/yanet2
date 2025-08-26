#pragma once

#include "defines.h"
#include "ring.h"
#include "state.h"

#include "common/lpm.h"

#include "controlplane/config/zone.h"

struct balancer_vs {
	uint64_t type;
	uint8_t address[16];
	uint64_t real_start;
	uint64_t real_count;
	struct lpm src;
	struct ring real_ring;
};

struct balancer_rs {
	uint64_t type;
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
};

struct balancer_module_config {
	struct cp_module cp_module;

	struct lpm v4_service_lookup;
	struct lpm v6_service_lookup;
	struct state state;

	struct balancer_state_config state_config;

	uint64_t service_count;
	struct balancer_vs **services;

	uint64_t real_count;
	struct balancer_rs *reals;
};
