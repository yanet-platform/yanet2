#include "pipeline.h"

#include <stdlib.h>

#include "module.h"

int
pipeline_init(struct pipeline *pipeline)
{
	pipeline->module_configs = NULL;

	return 0;
}

void
pipeline_process(
	struct pipeline *pipeline,
	struct pipeline_front *pipeline_front)
{
	pipeline_front->pipeline = pipeline;

	/*
	 * TODO: 8-byte aligned read and write should be atomic but we
	 * have to ensure it.
	 */
	for (struct pipeline_module_config *module_config = pipeline->module_configs;
             module_config != NULL;
	     module_config = module_config->next) {

		// Connect previous output to the next input. */
		pipeline_front_switch(pipeline_front);

		// Invoke module instance.
		module_process(
			module_config->module, module_config->config,
			pipeline_front);
	}
}

/*
 * Helper routeing looking for existing module instance.
 * Pipeline attempts to reuse any module instance if possible.
 */
static struct pipeline_module_config *
pipeline_find_module_config(
	struct pipeline *pipeline,
	const char *module_name,
	const char *config_name)
{
	//FIXME: linear scan is not the best choice but is enough right now.
	for (struct pipeline_module_config *module_config = pipeline->module_configs;
             module_config != NULL;
	     module_config = module_config->next) {
		const struct module *module = module_config->module;
		const struct module_config *config = module_config->config;

		if (!strncmp(module_name, module->name, sizeof(module->name)) &&
		    !strncmp(config_name, config->name, sizeof(config->name))) {
			return module_config;
		}
	}
	return NULL;
}

int
pipeline_configure(
	struct pipeline *pipeline,
	struct pipeline_module_config_data *module_config_datas,
	uint32_t config_size) {

	/*
	 * New pipeline configuration chain placed into continuous
	 * memory chunk.
	 */
	struct pipeline_module_config *first_module_config =
		(struct pipeline_module_config *)malloc(sizeof(struct pipeline_module_config) * config_size);
	if (first_module_config == NULL) {
		return -1;
	}

	struct pipeline_module_config *last_module_config = first_module_config;

	for (uint32_t idx = 0; idx < config_size; ++idx) {
		struct pipeline_module_config_data *module_config_data =
			module_config_datas + idx;

		// Module handle and existing configuration
		struct module *module = NULL;
		struct module_config *config = NULL;

		/*
		 * Module instance lookup:
		 *  - if found the call module to update the instance,
		 *    the module is in duty to preserve existing runtime
		 *    values, tables, counters, etc.
		 *  - if not found create new module instance and insert into
		 *    th chain.
		 */
		struct pipeline_module_config *configured =
			pipeline_find_module_config(
				pipeline,
				module_config_data->module_name,
				module_config_data->config_name);
		if (configured != NULL) {
			module = configured->module;
			config = configured->config;
		} else {
			module = module_lookup(module_config_data->module_name);
			if (module == NULL) {
				goto error;
			}
		}

		if (module_configure(
			module, module_config_data->data,
			config, &last_module_config->config)) {
			goto error;
		}

		last_module_config->module = configured->module;
		last_module_config->next = last_module_config + 1;
		++last_module_config;
	}

	/*
	 * Now all modules are configured so it is high time to replace
	 * the pipeline chain.
	 */
	pipeline->module_configs = first_module_config;
	//FIXME: free the previous pipeline module chain

	return 0;

error:
	for (struct pipeline_module_config *module_config = first_module_config;
	     module_config != last_module_config;
	     module_config = module_config->next) {

		//FIXME: free module config
	}


	return -1;
}
