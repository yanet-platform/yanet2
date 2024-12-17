#include "module_registry.h"

#include <string.h>

#include "dataplane/module/module.h"

int
module_registry_register(
	struct module_registry *module_registry, struct module *module
) {

	for (uint32_t idx = 0; idx < module_registry->module_count; ++idx) {
		struct module *known_module = module_registry->modules[idx];

		if (!strncmp(
			    known_module->name, module->name, MODULE_NAME_LEN
		    )) {
			// TODO: error code
			return -1;
		}
	}

	// Module is not known by pointer nor name

	// FIXME array extending as routine/library
	if (module_registry->module_count % 8 == 0) {
		struct module **new_modules = (struct module **)realloc(
			module_registry->modules,
			sizeof(struct module *) *
				(module_registry->module_count + 8)
		);
		if (new_modules == NULL) {
			// TODO: error code
			return -1;
		}
		module_registry->modules = new_modules;
	}

	module_registry->modules[module_registry->module_count++] = module;

	return 0;
}

struct module *
module_registry_lookup(
	struct module_registry *module_registry, const char *module_name
) {
	for (uint32_t idx = 0; idx < module_registry->module_count; ++idx) {
		struct module *module = module_registry->modules[idx];

		if (!strncmp(module->name, module_name, MODULE_NAME_LEN)) {
			return module;
		}
	}

	return NULL;
}
