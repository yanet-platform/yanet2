#pragma once

#include <stddef.h>
#include <stdint.h>

#include "common/network.h"

struct agent;
struct cp_module;
struct memory_context;
struct route_module_config;

struct cp_module *
route_module_config_create(struct agent *agent, const char *name);

void
route_module_config_free(struct cp_module *cp_module);

int
route_module_config_data_init(
	struct route_module_config *config,
	struct memory_context *memory_context
);

int
route_module_config_add_route(
	struct cp_module *cp_module,
	struct ether_addr dst_addr,
	struct ether_addr src_addr
);

int
route_module_config_add_route_list(
	struct cp_module *cp_module, size_t count, const uint32_t *indexes
);

int
route_module_config_add_prefix_v4(
	struct cp_module *cp_module,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t route_list_index
);

int
route_module_config_add_prefix_v6(
	struct cp_module *cp_module,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t route_list_index
);
