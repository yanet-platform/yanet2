#pragma once

#include <stddef.h>
#include <stdint.h>

#include "common/network.h"

struct agent;
struct module_data;

struct module_data *
route_module_config_init(struct agent *agent, const char *name);

void
route_module_config_free(struct module_data *module_data);

int
route_module_config_add_route(
	struct module_data *module_data,
	struct ether_addr dst_addr,
	struct ether_addr src_addr
);

int
route_module_config_add_route_list(
	struct module_data *module_data, size_t count, const uint32_t *indexes
);

int
route_module_config_add_prefix_v4(
	struct module_data *module_data,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t route_list_index
);

int
route_module_config_add_prefix_v6(
	struct module_data *module_data,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t route_list_index
);
