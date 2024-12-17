#include "dataplane_registry.h"

#include <string.h>

#include <dlfcn.h>

#include "dataplane/module/module.h"
#include "dataplane/pipeline/pipeline.h"

int
dataplane_registry_load_module(
	struct dataplane_registry *dataplane_registry,
	void *binary,
	const char *module_name
) {
	char loader_name[MODULE_NAME_LEN];
	snprintf(
		loader_name,
		sizeof(loader_name),
		"%s%s",
		"new_module_",
		module_name
	);

	module_load_handler loader =
		(module_load_handler)dlsym(binary, loader_name);
	// FIXME sanity checks
	return module_registry_register(
		&dataplane_registry->module_registry, loader()
	);
}

/*
 * FIXME: the routine is to big and should be refactored into few less ones.
 */
/*
 * FIXME: configuration refcounting is broken and should be redesigned
 */
int
dataplane_registry_update(
	struct dataplane_registry *dataplane_registry,
	struct dataplane_module_config *modules,
	uint32_t module_count,
	struct dataplane_pipeline_config *pipelines,
	uint32_t pipeline_count
) {
	struct module_config_registry new_module_config_registry;

	module_config_registry_init(&new_module_config_registry);

	/*
	 * A full-join between existing configuration and updates -
	 * as we should preserve non-touched configration as-is, reconfigure
	 * existing and instantiate new ones.
	 * So there is a two step process:
	 *  - iterate through existing configuration filtering out ones with
	 *    corresponding update found. The remaining configurations are
	 *    to be copied as is.
	 *  - iterate through updates and apply ones to a corrresponding
	 *    configuration or instantiate a new one.
	 */

	/*
	 * FIXME: The code bellow is not efficient but the configuration item
	 * count is not expected to be big and reconfiguration is not te be
	 * called frequently so the optimization is the subject of further
	 * development.
	 */
	for (uint32_t idx = 0;
	     idx <
	     dataplane_registry->module_config_registry.module_config_count;
	     ++idx) {
		struct module_config *module_config =
			dataplane_registry->module_config_registry
				.module_configs[idx];
		int found = 0;
		for (uint32_t idx = 0; idx < module_count; ++idx) {
			if (!strncmp(
				    module_config->module->name,
				    modules[idx].module_name,
				    MODULE_NAME_LEN
			    ) &&
			    !strncmp(
				    module_config->name,
				    modules[idx].module_config_name,
				    MODULE_CONFIG_NAME_LEN
			    )) {
				found = 1;
				break;
			}
		}

		if (!found) {
			module_config_registry_register(
				&new_module_config_registry, module_config
			);
		}
	}

	for (uint32_t idx = 0; idx < module_count; ++idx) {
		struct dataplane_module_config *dataplane_module_config =
			modules + idx;

		/*
		 * TODO: sanity checks.
		 * There is interesting questions: what the consistency we
		 * expect here. So what if configuration module differs from
		 * lookuped one? What about module hot-reload? Should we
		 * enforce module-name/config-name consistency here?
		 */
		struct module *module = module_registry_lookup(
			&dataplane_registry->module_registry,
			dataplane_module_config->module_name
		);

		if (module == NULL) {
			goto error_config;
		}

		struct module_config *old_module_config =
			module_config_registry_lookup(
				&dataplane_registry->module_config_registry,
				dataplane_module_config->module_name,
				dataplane_module_config->module_config_name
			);

		struct module_config *new_module_config;
		if (module_configure(
			    module,
			    dataplane_module_config->module_config_name,
			    dataplane_module_config->data,
			    dataplane_module_config->data_size,
			    old_module_config,
			    &new_module_config
		    )) {
			goto error_config;
		}

		module_config_registry_register(
			&new_module_config_registry, new_module_config
		);
	}

	struct pipeline_registry new_pipeline_registry;
	pipeline_registry_init(&new_pipeline_registry);

	/*
	 * The same full-join logic for pipeline reconfiguration.
	 * However, we should rebuild also directly untouched pipelines -
	 * just because linked in modules may be reconfigured.
	 */

	for (uint32_t idx = 0;
	     idx < dataplane_registry->pipeline_registry.pipeline_count;
	     ++idx) {
		struct pipeline *pipeline =
			dataplane_registry->pipeline_registry.pipelines[idx];

		int found = 0;
		for (uint32_t idx = 0; idx < pipeline_count; ++idx) {
			if (!strncmp(
				    pipeline->name,
				    pipelines[idx].pipeline_name,
				    PIPELINE_NAME_LEN
			    )) {
				found = 1;
				break;
			}
		}

		if (!found) {
			struct module_config
				*new_configs[pipeline->module_config_count];

			for (uint32_t idx = 0;
			     idx < pipeline->module_config_count;
			     ++idx) {
				struct module_config *old_module_config =
					pipeline->module_configs[idx];

				struct module_config *new_module_config =
					module_config_registry_lookup(
						&new_module_config_registry,
						old_module_config->module->name,
						old_module_config->name
					);
				if (new_module_config == NULL) {
					// FIXME: handle error
				}

				new_configs[idx] = new_module_config;
			}

			struct pipeline *new_pipeline;
			if (pipeline_configure(
				    pipeline->name,
				    new_configs,
				    pipeline->module_config_count,
				    &new_pipeline
			    )) {
				// FIXME: handle error
			}

			if (pipeline_registry_register(
				    &new_pipeline_registry, new_pipeline
			    )) {
				// FIXME: handle error
			}
		}
	}

	for (uint32_t idx = 0; idx < pipeline_count; ++idx) {
		struct pipeline *new_pipeline;

		struct dataplane_pipeline_config *pipeline_config =
			pipelines + idx;

		struct module_config
			*new_configs[pipeline_config->module_config_count];
		for (uint32_t idx = 0;
		     idx < pipeline_config->module_config_count;
		     ++idx) {
			new_configs[idx] = module_config_registry_lookup(
				&new_module_config_registry,
				pipeline_config->module_configs[idx]
					.module_name,
				pipeline_config->module_configs[idx]
					.module_config_name
			);
			if (new_configs[idx] == NULL) {
				// FIXME: handle error
			}
		}

		if (pipeline_configure(
			    pipelines[idx].pipeline_name,
			    new_configs,
			    pipelines[idx].module_config_count,
			    &new_pipeline
		    )) {
			// FIXME: handle error
		}

		if (pipeline_registry_register(
			    &new_pipeline_registry, new_pipeline
		    )) {
			// FIXME: handle error
		}
	}
	// Release old config and pipeline registries

	dataplane_registry->module_config_registry = new_module_config_registry;
	dataplane_registry->pipeline_registry = new_pipeline_registry;

	return 0;

error_config:
	// FIXME: free resources
	return -1;
}
