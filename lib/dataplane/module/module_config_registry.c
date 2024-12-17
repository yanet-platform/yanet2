#include "module_config_registry.h"

#include <string.h>

#include "dataplane/module/module.h"

int
module_config_registry_register(
	struct module_config_registry *config_registry,
	struct module_config *module_config
) {
	for (uint32_t idx = 0; idx < config_registry->module_config_count;
	     ++idx) {
		struct module_config *known_config =
			config_registry->module_configs[idx];

		if (!strncmp(
			    known_config->name,
			    module_config->name,
			    MODULE_CONFIG_NAME_LEN
		    )) {
			// TODO: error code
			return -1;
		}
	}

	// Module is not known by pointer nor name

	// FIXME array extending as routine/library
	if (config_registry->module_config_count % 8 == 0) {
		struct module_config **new_configs = (struct module_config **)
			realloc(config_registry->module_configs,
				sizeof(struct module_config *) *
					(config_registry->module_config_count +
					 8));
		if (new_configs == NULL) {
			// TODO: error code
			return -1;
		}
		config_registry->module_configs = new_configs;
	}

	config_registry
		->module_configs[config_registry->module_config_count++] =
		module_config;

	return 0;
}

struct module_config *
module_config_registry_lookup(
	struct module_config_registry *config_registry,
	const char *module_name,
	const char *module_config_name
) {
	for (uint32_t idx = 0; idx < config_registry->module_config_count;
	     ++idx) {
		struct module_config *module_config =
			config_registry->module_configs[idx];

		if (!strncmp(
			    module_config->module->name,
			    module_name,
			    MODULE_NAME_LEN
		    ) &&
		    !strncmp(
			    module_config->name,
			    module_config_name,
			    MODULE_CONFIG_NAME_LEN
		    )) {
			return module_config;
		}
	}

	return NULL;
}
