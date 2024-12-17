#pragma once

#include <stdint.h>
#include <stdlib.h>

struct module;

struct module_registry {
	struct module **modules;
	uint32_t module_count;
};

static inline int
module_registry_init(struct module_registry *registry) {
	registry->modules = NULL;
	registry->module_count = 0;
	return 0;
}

int
module_registry_register(
	struct module_registry *module_registry, struct module *module
);

struct module *
module_registry_lookup(
	struct module_registry *module_registry, const char *module_name
);
