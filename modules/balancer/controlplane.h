#pragma once

#include "rs_def.h"
#include "vs_def.h"

#include <stddef.h>
#include <stdint.h>

struct agent;
struct cp_module;

////////////////////////////////////////////////////////////////////////////////

struct balancer_state;

struct balancer_state *
balancer_state_init(struct agent *agent, size_t sessions_to_reserve);

void
balancer_state_free(struct balancer_state *state);

////////////////////////////////////////////////////////////////////////////////

struct cp_module *
balancer_module_config_create(
	struct agent *agent, struct balancer_state *state, const char *name
);

void
balancer_module_config_free(struct cp_module *cp_module);

int
balancer_module_config_update_real_weight(
	struct cp_module *cp_module,
	uint64_t service_idx,
	uint64_t real_idx,
	uint16_t weight
);

void
balancer_module_config_set_timeouts(
	struct cp_module *cp_module,
	uint32_t tcp_syn_ack_timeout,
	uint32_t tcp_syn_timeout,
	uint32_t tcp_fin_timeout,
	uint32_t tcp_timeout,
	uint32_t udp_timeout,
	uint32_t default_timeout
);

struct balancer_vs_config;

struct balancer_vs_config *
balancer_service_config_create(
	balancer_vs_flags_t flags,
	uint8_t *address,
	uint16_t port,
	uint8_t proto,
	uint64_t real_count,
	uint64_t prefixes_count
);

void
balancer_service_config_free(struct balancer_vs_config *service_config);

void
balancer_service_config_set_real(
	struct balancer_vs_config *config,
	uint64_t index,
	balancer_rs_flags_t flags,
	uint16_t weight,
	uint8_t *dst_addr,
	uint8_t *src_addr,
	uint8_t *src_mask
);

void
balancer_service_config_set_src_prefix(
	struct balancer_vs_config *service_config,
	uint64_t index,
	uint8_t *start_addr,
	uint8_t *end_addr
);

int
balancer_module_config_add_service(
	struct cp_module *cp_module, struct balancer_vs_config *service
);

void
balancer_module_config_update_current_time(struct cp_module *cp_module);
