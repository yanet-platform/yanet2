#pragma once

#include "dataplane/module/module.h"
#include "dataplane/pipeline/pipeline.h"

#include "dataplane/module/module_config_registry.h"
#include "dataplane/module/module_registry.h"
#include "dataplane/pipeline/pipeline_registry.h"

struct dataplane_registry {
	struct module_registry module_registry;
	struct module_config_registry module_config_registry;
	struct pipeline_registry pipeline_registry;
};

static inline int
dataplane_registry_init(struct dataplane_registry *dataplane_registry) {
	module_registry_init(&dataplane_registry->module_registry);
	module_config_registry_init(&dataplane_registry->module_config_registry
	);
	pipeline_registry_init(&dataplane_registry->pipeline_registry);
	return 0;
}

int
dataplane_registry_load_module(
	struct dataplane_registry *dataplane_registry,
	void *binary,
	const char *module_name
);

/*
 * FIXME: structures bellow use pointers for item names but fixed-length arrays
 * may be more convinient for message decoding and processing.
 */
struct dataplane_module_config {
	char *module_name;
	char *module_config_name;
	const void *data;
	uint64_t data_size;
};

/*
 * DRAFT.
 * Module configuration data denotes module and instance name and a pointer
 * to configuration values which the module should decode and apply.
 */
struct dataplane_pipeline_module {
	char module_name[MODULE_NAME_LEN];
	char module_config_name[MODULE_CONFIG_NAME_LEN];
};

struct dataplane_pipeline_config {
	char pipeline_name[PIPELINE_NAME_LEN];
	struct dataplane_pipeline_module *module_configs;
	uint32_t module_config_count;
};

int
dataplane_registry_update(
	struct dataplane_registry *dataplane_registry,
	struct dataplane_module_config *modules,
	uint32_t module_count,
	struct dataplane_pipeline_config *pipelines,
	uint32_t pipeline_count
);
