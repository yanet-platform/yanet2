#include "zone.h"

#include <unistd.h>

#include "common/strutils.h"

#include "lib/dataplane/config/zone.h"

#include "lib/controlplane/agent/agent.h"

bool
cp_config_try_lock(struct cp_config *cp_config) {
	pid_t pid = getpid();
	pid_t zero = 0;
	return __atomic_compare_exchange_n(
		&cp_config->config_lock,
		&zero,
		pid,
		false,
		__ATOMIC_RELAXED,
		__ATOMIC_RELAXED
	);
}

void
cp_config_lock(struct cp_config *cp_config) {
	pid_t pid = getpid();
	pid_t zero = 0;
	while (!__atomic_compare_exchange_n(
		&cp_config->config_lock,
		&zero,
		pid,
		false,
		__ATOMIC_RELAXED,
		__ATOMIC_RELAXED
	)) {
		zero = 0;
	};
}

bool
cp_config_unlock(struct cp_config *cp_config) {
	pid_t pid = getpid();
	pid_t zero = 0;
	return __atomic_compare_exchange_n(
		&cp_config->config_lock,
		&pid,
		zero,
		false,
		__ATOMIC_RELAXED,
		__ATOMIC_RELAXED
	);
}

static inline void
cp_config_collect_modules(struct cp_config *cp_config) {
	struct cp_config_gen *config_gen = ADDR_OF(&cp_config->cp_config_gen);
	struct cp_module_registry *module_registry =
		ADDR_OF(&config_gen->module_registry);

	for (uint64_t idx = 0; idx < module_registry->count; ++idx) {
		struct module_data *module_data =
			ADDR_OF(&module_registry->modules[idx].data);
		if (module_data->prev == NULL)
			continue;

		struct module_data *prev_module_data =
			ADDR_OF(&module_data->prev);
		struct agent *prev_agent = ADDR_OF(&prev_module_data->agent);
		SET_OFFSET_OF(
			&module_data->prev, ADDR_OF(&prev_module_data->prev)
		);
		// Put the data in the owning context free space
		SET_OFFSET_OF(
			&prev_module_data->prev, prev_agent->unused_module
		);
		SET_OFFSET_OF(&prev_agent->unused_module, prev_module_data);
	}
}

static inline struct cp_config_gen *
cp_config_gen_spawn(
	struct cp_config *cp_config, struct cp_config_gen *old_config_gen
) {
	struct cp_config_gen *new_config_gen =
		(struct cp_config_gen *)memory_balloc(
			&cp_config->memory_context, sizeof(struct cp_config_gen)
		);
	if (new_config_gen == NULL)
		return NULL;

	new_config_gen->gen = old_config_gen->gen + 1;
	SET_OFFSET_OF(&new_config_gen->prev, old_config_gen);
	SET_OFFSET_OF(
		&new_config_gen->module_registry,
		ADDR_OF(&old_config_gen->module_registry)
	);
	SET_OFFSET_OF(
		&new_config_gen->pipeline_registry,
		ADDR_OF(&old_config_gen->pipeline_registry)
	);
	SET_OFFSET_OF(
		&new_config_gen->device_registry,
		ADDR_OF(&old_config_gen->device_registry)
	);

	return new_config_gen;
}

static inline int
cp_config_gen_lookup_module_index(
	struct cp_config_gen *cp_config_gen,
	uint64_t index,
	const char *name,
	uint64_t *res_index
) {
	struct cp_module_registry *module_registry =
		ADDR_OF(&cp_config_gen->module_registry);
	struct cp_module *modules = module_registry->modules;
	for (uint64_t idx = 0; idx < module_registry->count; ++idx) {
		struct cp_module *module = modules + idx;
		if (index == ADDR_OF(&module->data)->index &&
		    !strncmp(name, ADDR_OF(&module->data)->name, 80)) {
			*res_index = idx;
			return 0;
		}
	}
	return -1;
}

/*
 * FIXME: The routine should be splitted into smaller pieces.
 * Also we may to use them into compound config update function which would
 * update both modules and pipelines.
 */
int
cp_config_update_modules(
	struct agent *agent,
	uint64_t module_count,
	struct module_data **module_datas
) {
	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);
	struct cp_config *cp_config = ADDR_OF(&dp_config->cp_config);
	cp_config_lock(cp_config);

	struct cp_config_gen *old_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);

	struct cp_module_registry *old_module_registry =
		ADDR_OF(&old_config_gen->module_registry);

	uint64_t new_module_count = old_module_registry->count;
	for (uint64_t new_idx = 0; new_idx < module_count; ++new_idx) {
		uint64_t old_idx;
		struct module_data *new_module_data = module_datas[new_idx];
		if (cp_config_gen_lookup_module_index(
			    old_config_gen,
			    new_module_data->index,
			    new_module_data->name,
			    &old_idx
		    )) {
			++new_module_count;
		}
	}

	struct cp_config_gen *new_config_gen =
		cp_config_gen_spawn(cp_config, old_config_gen);

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

		SET_OFFSET_OF(&new_module->data, ADDR_OF(&old_module->data));
	}
	new_module_registry->count = old_module_registry->count;

	// Update or insert new module data
	for (uint64_t new_idx = 0; new_idx < module_count; ++new_idx) {
		uint64_t old_idx;
		struct module_data *new_module_data = module_datas[new_idx];
		if (cp_config_gen_lookup_module_index(
			    old_config_gen,
			    new_module_data->index,
			    new_module_data->name,
			    &old_idx
		    )) {
			struct cp_module *new_module =
				new_module_registry->modules +
				new_module_registry->count;

			SET_OFFSET_OF(&new_module_data->prev, NULL);
			SET_OFFSET_OF(&new_module->data, new_module_data);
			new_module_registry->count += 1;
		} else {
			struct cp_module *new_module =
				new_module_registry->modules + old_idx;

			struct module_data *old_module_data =
				ADDR_OF(&new_module->data);

			struct agent *old_agent =
				ADDR_OF(&old_module_data->agent);
			old_agent->loaded_module_count -= 1;

			SET_OFFSET_OF(&new_module_data->prev, old_module_data);
			SET_OFFSET_OF(&new_module->data, new_module_data);
		}

		new_module_data->gen = new_config_gen->gen;
		struct agent *new_agent = ADDR_OF(&new_module_data->agent);
		new_agent->loaded_module_count += 1;
	}
	// FIXME: assert new_config_gen.module_count == new_module_count

	SET_OFFSET_OF(&new_config_gen->module_registry, new_module_registry);

	SET_OFFSET_OF(&new_config_gen->prev, old_config_gen);

	SET_OFFSET_OF(&cp_config->cp_config_gen, new_config_gen);

	dp_config_wait_for_gen(dp_config, new_config_gen->gen);

	cp_config_collect_modules(cp_config);

	SET_OFFSET_OF(&new_config_gen->prev, ADDR_OF(&old_config_gen->prev));

	memory_bfree(
		&cp_config->memory_context,
		old_module_registry,
		sizeof(struct cp_module_registry) +
			sizeof(struct cp_module) * old_module_registry->count
	);

	memory_bfree(
		&cp_config->memory_context,
		old_config_gen,
		sizeof(struct cp_config_gen)
	);

	cp_config_unlock(cp_config);

	return 0;
}

static inline int
cp_config_gen_lookup_pipeline_index(
	struct cp_config_gen *cp_config_gen,
	const char *name,
	uint64_t *res_index
) {
	struct cp_pipeline_registry *pipeline_registry =
		ADDR_OF(&cp_config_gen->pipeline_registry);
	struct cp_pipeline **pipelines = pipeline_registry->pipelines;
	for (uint64_t idx = 0; idx < pipeline_registry->count; ++idx) {
		struct cp_pipeline *pipeline = ADDR_OF(pipelines + idx);
		if (!strncmp(name, pipeline->name, 80)) {
			*res_index = idx;
			return 0;
		}
	}
	return -1;
}

static inline void
cp_config_free_pipeline(
	struct cp_config *cp_config, struct cp_pipeline *pipeline
) {
	memory_bfree(
		&cp_config->memory_context,
		pipeline,
		sizeof(struct cp_pipeline) + sizeof(uint64_t) * pipeline->length
	);
}

static inline int
cp_config_make_pipeline(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	struct pipeline_config *pipeline_config,
	struct cp_pipeline **pipeline
) {
	uint64_t pipeline_size = sizeof(struct cp_pipeline) +
				 sizeof(uint64_t) * pipeline_config->length;

	struct cp_pipeline *new_pipeline = (struct cp_pipeline *)memory_balloc(
		&cp_config->memory_context, pipeline_size
	);
	if (new_pipeline == NULL) {
		return -1;
	}

	new_pipeline->refcnt = 0;

	struct cp_config_gen *config_gen = ADDR_OF(&cp_config->cp_config_gen);

	new_pipeline->length = pipeline_config->length;
	strtcpy(new_pipeline->name, pipeline_config->name, 80);

	for (uint64_t module_idx = 0; module_idx < pipeline_config->length;
	     ++module_idx) {
		uint64_t index;
		if (dp_config_lookup_module(
			    dp_config,
			    pipeline_config->modules[module_idx].type,
			    &index
		    )) {
			goto error;
		}

		if (cp_config_gen_lookup_module_index(
			    config_gen,
			    index,
			    pipeline_config->modules[module_idx].name,
			    new_pipeline->module_indexes + module_idx
		    )) {
			goto error;
		}
	}

	*pipeline = new_pipeline;
	return 0;

error:
	cp_config_free_pipeline(cp_config, new_pipeline);
	return -1;
}

static inline struct cp_pipeline_registry *
cp_config_make_pipeline_registry(struct cp_config *cp_config, uint64_t count) {
	struct cp_pipeline_registry *new_pipeline_registry =
		(struct cp_pipeline_registry *)memory_balloc(
			&cp_config->memory_context,
			sizeof(struct cp_pipeline_registry) +
				sizeof(struct cp_pipeline *) * count
		);
	if (new_pipeline_registry == NULL)
		return NULL;

	memset(new_pipeline_registry,
	       0,
	       sizeof(struct cp_pipeline_registry) +
		       sizeof(struct cp_pipeline *) * count);
	new_pipeline_registry->count = count;

	return new_pipeline_registry;
}

static inline void
cp_config_free_pipeline_registry(
	struct cp_config *cp_config,
	struct cp_pipeline_registry *pipeline_registry
) {
	for (uint64_t idx = 0; idx < pipeline_registry->count; ++idx) {
		struct cp_pipeline *pipeline =
			ADDR_OF(pipeline_registry->pipelines + idx);
		if (pipeline == NULL)
			continue;
		if (!--pipeline->refcnt)
			cp_config_free_pipeline(cp_config, pipeline);
	}

	memory_bfree(
		&cp_config->memory_context,
		pipeline_registry,
		sizeof(struct cp_pipeline_registry) +
			sizeof(struct cp_pipeline *) * pipeline_registry->count
	);
}

/*
 * The routine updates pipelines configuration.
 */
int
cp_config_update_pipelines(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	uint64_t pipeline_count,
	struct pipeline_config **pipeline_configs
) {
	cp_config_lock(cp_config);

	struct cp_config_gen *old_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);

	struct cp_pipeline_registry *old_pipeline_registry =
		ADDR_OF(&old_config_gen->pipeline_registry);

	uint64_t new_pipeline_count = old_pipeline_registry->count;
	for (uint64_t new_idx = 0; new_idx < pipeline_count; ++new_idx) {
		uint64_t old_idx;
		if (cp_config_gen_lookup_pipeline_index(
			    old_config_gen,
			    pipeline_configs[new_idx]->name,
			    &old_idx
		    )) {
			++new_pipeline_count;
		}
	}

	struct cp_pipeline_registry *new_pipeline_registry =
		cp_config_make_pipeline_registry(cp_config, new_pipeline_count);
	if (new_pipeline_registry == NULL) {
		cp_config_unlock(cp_config);
		return -1;
	}

	for (uint64_t idx = 0; idx < old_pipeline_registry->count; ++idx) {
		struct cp_pipeline *pipeline =
			ADDR_OF(old_pipeline_registry->pipelines + idx);

		++pipeline->refcnt;
		SET_OFFSET_OF(new_pipeline_registry->pipelines + idx, pipeline);
	}

	for (uint64_t new_idx = 0; new_idx < pipeline_count; ++new_idx) {
		struct cp_pipeline *new_pipeline;
		if (cp_config_make_pipeline(
			    dp_config,
			    cp_config,
			    pipeline_configs[new_idx],
			    &new_pipeline
		    )) {
			cp_config_free_pipeline_registry(
				cp_config, new_pipeline_registry
			);

			cp_config_unlock(cp_config);
			return -1;
		}

		++new_pipeline->refcnt;
		SET_OFFSET_OF(
			new_pipeline_registry->pipelines + new_idx, new_pipeline
		);
	}

	struct cp_config_gen *new_config_gen =
		cp_config_gen_spawn(cp_config, old_config_gen);
	if (new_config_gen == NULL) {
		cp_config_free_pipeline_registry(
			cp_config, new_pipeline_registry
		);

		cp_config_unlock(cp_config);
		return -1;
	}

	SET_OFFSET_OF(
		&new_config_gen->pipeline_registry, new_pipeline_registry
	);

	// New config generation is ready, set it as working one
	SET_OFFSET_OF(&cp_config->cp_config_gen, new_config_gen);

	dp_config_wait_for_gen(dp_config, new_config_gen->gen);

	// Now remove previous one configuration
	SET_OFFSET_OF(&new_config_gen->prev, ADDR_OF(&old_config_gen->prev));

	// Free old pipeline registry
	cp_config_free_pipeline_registry(cp_config, old_pipeline_registry);

	// Free old configuration
	memory_bfree(
		&cp_config->memory_context,
		old_config_gen,
		sizeof(struct cp_config_gen)
	);

	cp_config_unlock(cp_config);
	return 0;
}

static inline struct cp_device_registry *
cp_config_make_device_registry(struct cp_config *cp_config, uint64_t count) {
	struct cp_device_registry *new_device_registry =
		(struct cp_device_registry *)memory_balloc(
			&cp_config->memory_context,
			sizeof(struct cp_device_registry) +
				sizeof(struct cp_device_pipeline_map *) * count
		);
	if (new_device_registry == NULL)
		return NULL;

	memset(new_device_registry,
	       0,
	       sizeof(struct cp_device_registry) +
		       sizeof(struct cp_device_pipeline_map *) * count);
	new_device_registry->count = count;

	return new_device_registry;
}

static inline void
cp_config_free_device_registry(
	struct cp_config *cp_config, struct cp_device_registry *device_registry
) {
	for (uint64_t idx = 0; idx < device_registry->count; ++idx) {
		struct cp_device_pipeline_map *device =
			ADDR_OF(device_registry->device_map + idx);
		if (device == NULL)
			continue;
		// FIXME: refcount
		memory_bfree(
			&cp_config->memory_context,
			device,
			sizeof(struct cp_device_pipeline_map) +
				sizeof(uint64_t) * device->size
		);
	}

	memory_bfree(
		&cp_config->memory_context,
		device_registry,
		sizeof(struct cp_device_registry) +
			sizeof(struct cp_device_pipeline_map *) *
				device_registry->count
	);
}

int
cp_config_update_devices(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	uint64_t device_count,
	struct device_pipeline_map *pipeline_maps[]
) {
	// TODO weight clamp
	cp_config_lock(cp_config);

	struct cp_config_gen *old_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);

	struct cp_device_registry *new_device_registry =
		cp_config_make_device_registry(
			cp_config, dp_config->dp_topology.device_count
		);
	if (new_device_registry == NULL) {
		cp_config_unlock(cp_config);
		return -1;
	}

	for (uint64_t idx = 0; idx < device_count; ++idx) {
		uint64_t weight = 0;
		struct device_pipeline_map *pipeline_map = pipeline_maps[idx];

		for (uint64_t pipeline_weight_idx = 0;
		     pipeline_weight_idx < pipeline_map->count;
		     ++pipeline_weight_idx) {
			struct pipeline_weight *pipeline_weight =
				pipeline_map->pipelines + pipeline_weight_idx;

			weight += pipeline_weight->weight;

			uint64_t pipeline_idx = 0;
			if (cp_config_gen_lookup_pipeline_index(
				    old_config_gen,
				    pipeline_weight->name,
				    &pipeline_idx
			    )) {
				cp_config_free_device_registry(
					cp_config, new_device_registry
				);

				cp_config_unlock(cp_config);
				return -1;
			}
		}

		if (weight == 0)
			continue;

		struct cp_device_pipeline_map *new_device =
			(struct cp_device_pipeline_map *)memory_balloc(
				&cp_config->memory_context,
				sizeof(struct cp_device_pipeline_map) +
					sizeof(uint64_t) * weight
			);
		if (new_device == NULL) {
			cp_config_free_device_registry(
				cp_config, new_device_registry
			);

			cp_config_unlock(cp_config);
			return -1;
		}
		new_device->size = weight;

		weight = 0;
		for (uint64_t pipeline_weight_idx = 0;
		     pipeline_weight_idx < pipeline_map->count;
		     ++pipeline_weight_idx) {

			struct pipeline_weight *pipeline_weight =
				pipeline_map->pipelines + pipeline_weight_idx;

			uint64_t pipeline_idx = 0;
			// Should not fail
			cp_config_gen_lookup_pipeline_index(
				old_config_gen,
				pipeline_weight->name,
				&pipeline_idx
			);

			for (uint64_t idx = 0; idx < pipeline_weight->weight;
			     ++idx) {
				new_device->pipelines[weight++] = pipeline_idx;
			}
		}

		SET_OFFSET_OF(
			new_device_registry->device_map +
				pipeline_map->device_id,
			new_device
		);
	}

	struct cp_config_gen *new_config_gen =
		cp_config_gen_spawn(cp_config, old_config_gen);

	SET_OFFSET_OF(&new_config_gen->device_registry, new_device_registry);

	// FIXME: prev for the first one config_gen is invalid
	SET_OFFSET_OF(&new_config_gen->prev, old_config_gen);

	SET_OFFSET_OF(&cp_config->cp_config_gen, new_config_gen);

	dp_config_wait_for_gen(dp_config, new_config_gen->gen);

	SET_OFFSET_OF(&new_config_gen->prev, ADDR_OF(&old_config_gen->prev));

	struct cp_device_registry *old_device_registry =
		ADDR_OF(&old_config_gen->device_registry);

	cp_config_free_device_registry(cp_config, old_device_registry);

	// Free old configuration
	memory_bfree(
		&cp_config->memory_context,
		old_config_gen,
		sizeof(struct cp_config_gen)
	);

	cp_config_unlock(cp_config);
	return 0;
}
