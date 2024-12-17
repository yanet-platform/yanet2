#pragma once

#include <stdint.h>

#include "network.h"

struct module_kernel_config_data {
	uint32_t net6_count;
	uint32_t net4_count;
	struct net6 *net6_routes;
	struct net4 *net4_routes;

	uint16_t device_count;
	uint16_t *device_map;
};

#include "dataplane/module/module.h"

struct module *
new_module_kernel();
