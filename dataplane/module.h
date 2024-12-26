#pragma once

#include <stdint.h>
#include <stdlib.h>

#define MODULE_NAME_LEN 80
#define MODULE_CONFIG_NAME_LEN 80

struct module;
struct module_config;

struct module_config_registry {
	struct module *module;
	struct module_config **configs;
	uint32_t config_count;
};

struct module_registry {
	struct module_config_registry *modules;
	uint32_t module_count;
};

struct module_config_registry *
module_registry_lookup(
	struct module_registry *module_registry, const char *module_name
);

struct module_config *
module_config_registry_lookup(
	struct module_config_registry *module_config_registry,
	const char *module_config_name
);

int
module_registry_configure(
	struct module_registry *module_registry,
	const char *module_name,
	const char *module_config_name,
	const void *data,
	size_t data_size
);

struct pipeline_front;

/*
 * Module handler called for a pipeline front.
 * Module should go through the front and handle packets.
 * For each input packet module should put into output or drop list of the
 * front.
 * Also module may create new packet and put the into output queue.
 */
typedef void (*module_handler)(
	struct module *module,
	struct module_config *module_config,
	struct pipeline_front *pipeline_front
);

/*
 * The module configuration handler called when module should be created,
 * reconfigured and freed. The handler accepts raw configuration data,
 * old instance configuration (or NULL) and sets new configuration pointer
 * via output parameter.
 *
 * The handler is responsible for:
 *  - checking if the configuration is same
 *  - preserving runtime parameters and variables
 */

typedef int (*module_config_handler)(
	struct module *module,
	const char *config_name,
	const void *config_data,
	size_t config_data_size,
	struct module_config *old_config,
	struct module_config **new_config
);

struct module {
	char name[MODULE_NAME_LEN];
	module_handler handler;
	module_config_handler config_handler;
};

struct module_config {
	char name[MODULE_CONFIG_NAME_LEN];
};

static inline void
module_process(
	struct module *module,
	struct module_config *config,
	struct pipeline_front *pipeline_front
) {
	return module->handler(module, config, pipeline_front);
}

static inline int
module_configure(
	struct module *module,
	const char *config_name,
	const void *config_data,
	size_t config_data_size,
	struct module_config *old_config,
	struct module_config **new_config
) {
	return module->config_handler(
		module,
		config_name,
		config_data,
		config_data_size,
		old_config,
		new_config
	);
}
