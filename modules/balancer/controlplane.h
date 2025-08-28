#pragma once

#include <stdint.h>

struct agent;
struct cp_module;

struct cp_module *
balancer_module_config_init(struct agent *agent, const char *name);

struct balancer_module_config;
struct memory_context;

void
balancer_module_config_data_init(
	struct balancer_module_config *config,
	struct memory_context *memory_context
);

int
balancer_module_config_update_real_weight(
	struct cp_module *cp_module,
	uint64_t service_idx,
	uint64_t real_idx,
	uint16_t weight
);

void
balancer_module_config_set_state_config(
	struct cp_module *cp_module,
	uint32_t tcp_syn_ack_timeout,
	uint32_t tcp_syn_timeout,
	uint32_t tcp_fin_timeout,
	uint32_t tcp_timeout,
	uint32_t udp_timeout,
	uint32_t default_timeout
);

void
balancer_module_config_free(struct cp_module *cp_module);

struct balancer_service_config;

struct balancer_service_config *
balancer_service_config_create(
	uint64_t type,
	uint8_t *address,
	uint64_t real_count,
	uint64_t prefixes_count
);

void
balancer_service_config_free(struct balancer_service_config *service_config);

void
balancer_service_config_set_real(
	struct balancer_service_config *config,
	uint64_t index,
	uint64_t type,
	uint16_t weight,
	uint8_t *dst_addr,
	uint8_t *src_addr,
	uint8_t *src_mask
);

void
balancer_service_config_set_src_prefix(
	struct balancer_service_config *service_config,
	uint64_t index,
	uint8_t *start_addr,
	uint8_t *end_addr
);

int
balancer_module_config_add_service(
	struct cp_module *cp_module, struct balancer_service_config *service
);
