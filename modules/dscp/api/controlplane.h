#pragma once

#include <stdint.h>

struct agent;
struct module_data;

// Create a new configuration for the DSCP module
struct module_data *
dscp_module_config_init(struct agent *agent, const char *name);

void
dscp_module_config_free(struct module_data *module_data);

// Add IPv4 prefix to the DSCP module configuration
int
dscp_module_config_add_prefix_v4(
	struct module_data *module, uint8_t *addr_start, uint8_t *addr_end
);

// Add IPv6 prefix to the DSCP module configuration
int
dscp_module_config_add_prefix_v6(
	struct module_data *module, uint8_t *addr_start, uint8_t *addr_end
);

// Set DSCP marking options for the module
int
dscp_module_config_set_dscp_marking(
	struct module_data *module, uint8_t flag, uint8_t mark
);
