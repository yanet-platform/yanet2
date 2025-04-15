#pragma once

#include <stdint.h>

struct agent;
struct cp_module;
struct memory_context;
struct dscp_module_config;

// Create a new configuration for the DSCP module
struct cp_module *
dscp_module_config_create(struct agent *agent, const char *name);

void
dscp_module_config_free(struct cp_module *cp_module);

int
dscp_module_config_data_init(
	struct dscp_module_config *config, struct memory_context *memory_context
);

void
dscp_module_config_data_destroy(struct dscp_module_config *config);

// Add IPv4 prefix to the DSCP module configuration
int
dscp_module_config_add_prefix_v4(
	struct cp_module *module, uint8_t *addr_start, uint8_t *addr_end
);

// Add IPv6 prefix to the DSCP module configuration
int
dscp_module_config_add_prefix_v6(
	struct cp_module *module, uint8_t *addr_start, uint8_t *addr_end
);

// Set DSCP marking options for the module
int
dscp_module_config_set_dscp_marking(
	struct cp_module *module, uint8_t flag, uint8_t mark
);
