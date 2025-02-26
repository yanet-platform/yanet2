#pragma once

#include <stdbool.h>
#include <stdint.h>

#include "common/memory.h"

#include "dataplane/module/module.h"

#include "dataplane/config/topology.h"

#include "controlplane/agent/agent.h"

struct module_data {
	uint64_t index;
	char name[80];
	struct memory_context memory_context;
};

struct cp_module {
	struct module_data *data;
};

struct cp_module_registry {
	uint64_t count;
	struct cp_module modules[0];
};

struct cp_pipeline {
	uint64_t length;
	uint64_t *module_indexes;
};

struct cp_pipeline_registry {
	uint64_t count;
	struct cp_pipeline pipelines[0];
};

struct cp_config_gen {
	uint64_t gen;

	struct cp_pipeline_registry *pipeline_registry;
	struct cp_module_registry *module_registry;
};

// Controlplane config entry zone
struct cp_config {
	struct block_allocator block_allocator;
	struct memory_context memory_context;
	// Relative reference to active controlplane config
	struct cp_config_gen *cp_config_gen;
};

struct dp_module {
	char name[80];
	module_handler handler;
};

struct dp_config {
	struct block_allocator block_allocator;
	struct memory_context memory_context;

	struct cp_config *cp_config;

	uint64_t module_count;
	struct dp_module *dp_modules;

	struct dp_topology dp_topology;
};

static inline size_t
dp_config_modules_count(struct dp_config *dp_config) {
	return dp_config->module_count;
}

static inline struct dp_module *
dp_config_module_by_index(struct dp_config *dp_config, size_t index) {
	if (index >= dp_config->module_count) {
		return NULL;
	}

	struct dp_module *modules = ADDR_OF(dp_config, dp_config->dp_modules);

	return modules + index;
}

static inline int
dp_config_lookup_module(
	struct dp_config *dp_config, const char *name, uint64_t *index
) {
	struct dp_module *modules = ADDR_OF(dp_config, dp_config->dp_modules);
	for (uint64_t idx = 0; idx < dp_config->module_count; ++idx) {
		if (!strncmp(modules[idx].name, name, 80)) {
			*index = idx;
			return 0;
		}
	}
	return -1;
}

/*
 * The routine updates one or more module confings linking them into
 * exisiting pipelines. Already exising modules are updated preserving its
 * index while new modules are to be appended to the tail of module list.
 * This means that pipilenes are not mutating here except adress recoding to
 * the new configuration generation container.
 */
/*
 * FIXME: The routine should be splitted into smaller pieces.
 * Also we may to use them into compound config update function which would
 * update both modules and pipelines.
 */
/*
 * FIXME: There are also some considerations about module and pipeline
 * registries - there may be self-contained object which would be able to
 * referenced from configuration generation with cost of one inderect fetch
 * for each.
 */
static inline int
cp_config_update_modules(
	struct cp_config *cp_config,
	uint64_t module_count,
	struct module_data **module_datas
) {
	// lock
	struct cp_config_gen *old_config_gen =
		ADDR_OF(cp_config, cp_config->cp_config_gen);
	// ref config
	// unlock

	struct cp_module_registry *old_module_registry =
		ADDR_OF(old_config_gen, old_config_gen->module_registry);

	uint64_t new_module_count = old_module_registry->count;
	for (uint64_t new_idx = 0; new_idx < module_count; ++new_idx) {
		bool found = false;

		for (uint64_t old_idx = 0; old_idx < old_module_registry->count;
		     ++old_idx) {
			struct cp_module *old_module =
				old_module_registry->modules + old_idx;

			if (module_datas[new_idx]->index ==
				    ADDR_OF(old_module, old_module->data)
					    ->index &&
			    !strncmp(
				    module_datas[new_idx]->name,
				    ADDR_OF(old_module, old_module->data)->name,
				    64
			    )) {
				found = true;
				break;
			}
		}
		if (!found) {
			++new_module_count;
		}
	}

	struct cp_config_gen *new_config_gen =
		(struct cp_config_gen *)memory_balloc(
			&cp_config->memory_context, sizeof(struct cp_config_gen)
		);
	// FIXME: zero initialize in order to provide correct error handling

	struct cp_module_registry *new_module_registry = memory_balloc(
		&cp_config->memory_context,
		sizeof(struct cp_module_registry) +
			sizeof(struct cp_module) * new_module_count
	);

	// Just copy old modules
	for (uint64_t idx = 0; idx < old_module_registry->count; ++idx) {
		struct cp_module *old_module =
			old_module_registry->modules + idx;
		struct cp_module *new_module =
			new_module_registry->modules + idx;

		*new_module = (struct cp_module){
			.data = OFFSET_OF(
				new_module,
				ADDR_OF(old_module, old_module->data)
			),
		};
	}
	new_module_registry->count = old_module_registry->count;

	// Update or insert new module data
	for (uint64_t new_idx = 0; new_idx < module_count; ++new_idx) {
		bool found = false;

		for (uint64_t old_idx = 0; old_idx < old_module_registry->count;
		     ++old_idx) {
			struct cp_module *new_module =
				new_module_registry->modules + old_idx;

			if (module_datas[new_idx]->index ==
				    ADDR_OF(new_module, new_module->data)
					    ->index &&
			    !strncmp(
				    module_datas[new_idx]->name,
				    ADDR_OF(new_module, new_module->data)->name,
				    64
			    )) {
				new_module->data = OFFSET_OF(
					new_module, module_datas[new_idx]
				);

				found = true;
				break;
			}
		}
		if (!found) {
			struct cp_module *new_module =
				new_module_registry->modules +
				new_module_registry->count;

			*new_module = (struct cp_module){
				.data = OFFSET_OF(
					new_module, module_datas[new_idx]
				),
			};
			new_module_registry->count += 1;
		}
	}
	// FIXME: assert new_config_gen.module_count == new_module_count

	new_config_gen->module_registry =
		OFFSET_OF(new_config_gen, new_module_registry);

	/*
	 * Now we have to update pipelines
	 * As we do not change original module order we may just to copy
	 * pipeline registry to the new config generation.
	 */
	new_config_gen->pipeline_registry = OFFSET_OF(
		new_config_gen,
		ADDR_OF(old_config_gen, old_config_gen->pipeline_registry)
	);

	// lock
	cp_config->cp_config_gen = OFFSET_OF(cp_config, new_config_gen);

	// unlock
	return 0;
}

static inline int
cp_config_gen_lookup_module(
	struct cp_config_gen *cp_config_gen,
	uint64_t index,
	const char *name,
	uint64_t *res_index
) {
	struct cp_module_registry *module_registry =
		ADDR_OF(cp_config_gen, cp_config_gen->module_registry);
	struct cp_module *modules = module_registry->modules;
	for (uint64_t idx = 0; idx < module_registry->count; ++idx) {
		struct cp_module *module = modules + idx;
		if (index == ADDR_OF(module, module->data)->index &&
		    !strncmp(name, ADDR_OF(module, module->data)->name, 64)) {
			*res_index = idx;
			return 0;
		}
	}
	return -1;
}

/*
 * The routine updates pipelines configuration.
 */
static inline int
cp_config_update_pipelines(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	uint64_t pipeline_count,
	struct pipeline_config *pipelines[]
) {
	// lock
	struct cp_config_gen *old_config_gen =
		ADDR_OF(cp_config, cp_config->cp_config_gen);
	// ref config
	// unlock

	struct cp_config_gen *new_config_gen =
		(struct cp_config_gen *)memory_balloc(
			&cp_config->memory_context, sizeof(struct cp_config_gen)
		);

	new_config_gen->module_registry = OFFSET_OF(
		new_config_gen,
		ADDR_OF(old_config_gen, old_config_gen->module_registry)
	);

	struct cp_pipeline_registry *new_pipeline_registry =
		(struct cp_pipeline_registry *)memory_balloc(
			&cp_config->memory_context,
			sizeof(struct cp_pipeline_registry) +
				sizeof(struct cp_pipeline) * pipeline_count
		);

	for (uint64_t pipeline_idx = 0; pipeline_idx < pipeline_count;
	     ++pipeline_idx) {
		struct pipeline_config *pipeline_config =
			pipelines[pipeline_idx];

		uint64_t *new_module_indexes = (uint64_t *)memory_balloc(
			&cp_config->memory_context,
			sizeof(uint64_t) * pipeline_config->length
		);
		for (uint64_t module_idx = 0;
		     module_idx < pipeline_config->length;
		     ++module_idx) {
			uint64_t index;
			if (dp_config_lookup_module(
				    dp_config,
				    pipeline_config->modules[module_idx].type,
				    &index
			    )) {
				// FIXME: free resources
				return -1;
			}

			if (cp_config_gen_lookup_module(
				    new_config_gen,
				    index,
				    pipeline_config->modules[module_idx].name,
				    new_module_indexes + module_idx
			    )) {
				// FIXME: free resources
				return -1;
			}
		}

		struct cp_pipeline *cp_pipeline =
			new_pipeline_registry->pipelines + pipeline_idx;
		*cp_pipeline = (struct cp_pipeline){
			.length = pipeline_config->length,
			.module_indexes =
				OFFSET_OF(cp_pipeline, new_module_indexes),
		};
	}

	new_pipeline_registry->count = pipeline_count;
	new_config_gen->pipeline_registry =
		OFFSET_OF(new_config_gen, new_pipeline_registry);

	// lock
	cp_config->cp_config_gen = OFFSET_OF(cp_config, new_config_gen);

	// unlock
	return 0;
}
