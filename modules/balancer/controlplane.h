#pragma once

#include <stdint.h>

struct agent;
struct module_data;

struct module_data *
balancer_module_config_init(struct agent *agent, const char *name);

struct blanacer_service_config;

struct balancer_service_config *
balancer_service_config_create(
	uint64_t type, uint8_t *address, uint64_t real_count
);

void
balancer_service_config_free(struct balancer_service_config *service_config);

void
balancer_service_config_set_real(
	struct balancer_service_config *config,
	uint64_t index,
	uint64_t type,
	uint8_t *dst_addr,
	uint8_t *src_addr,
	uint8_t *src_mask
);

int
balancer_module_config_add_service(
	struct module_data *module_data, struct balancer_service_config *service
);
