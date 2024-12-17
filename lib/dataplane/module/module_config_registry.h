#pragma once

#include <stdint.h>
#include <stdlib.h>

struct module_config;

struct module_config_registry {
	struct module_config **module_configs;
	uint32_t module_config_count;
};

static inline int
module_config_registry_init(struct module_config_registry *config_registry) {
	config_registry->module_configs = NULL;
	config_registry->module_config_count = 0;
	return 0;
}

int
module_config_registry_register(
	struct module_config_registry *config_registry,
	struct module_config *module_config
);

struct module_config *
module_config_registry_lookup(
	struct module_config_registry *config_registry,
	const char *module_name,
	const char *module_config_name
);
