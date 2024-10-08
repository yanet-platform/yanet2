#ifndef MODULE_H
#define MODULE_H

#include <stdlib.h>

#define MODULE_NAME_LEN 80
#define MODULE_CONFIG_NAME_LEN 80

struct module;
struct module_config;

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
	struct pipeline_front *pipeline_front);

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
	const void *config_data,
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
module_process(struct module *module, struct module_config *config, struct pipeline_front *pipeline_front)
{
	return module->handler(module, config, pipeline_front);
}

static inline int
module_configure(struct module *module, const void *config_data, struct module_config *old_config, struct module_config **new_config)
{
	if (config_data == NULL) {
		*new_config = old_config;
		return 0;
	}

	return module->config_handler(module, config_data, old_config, new_config);
}

struct module *
module_lookup(const char *name);

int
module_register(struct module *module);

#endif
