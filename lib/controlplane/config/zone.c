#include "zone.h"

#include <unistd.h>

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
		bool found = false;

		for (uint64_t old_idx = 0; old_idx < old_module_registry->count;
		     ++old_idx) {
			struct cp_module *old_module =
				old_module_registry->modules + old_idx;

			if (module_datas[new_idx]->index ==
				    ADDR_OF(&old_module->data)->index &&
			    !strncmp(
				    module_datas[new_idx]->name,
				    ADDR_OF(&old_module->data)->name,
				    64
			    )) {
				SET_OFFSET_OF(
					&module_datas[new_idx]->prev,
					ADDR_OF(&old_module->data)
				);
				found = true;
				break;
			}
		}
		if (!found) {
			module_datas[new_idx]->prev = NULL;
			++new_module_count;
		}
	}

	struct cp_config_gen *new_config_gen =
		(struct cp_config_gen *)memory_balloc(
			&cp_config->memory_context, sizeof(struct cp_config_gen)
		);
	new_config_gen->gen = old_config_gen->gen + 1;
	/*
	 * As we do not change original module order we may just to copy
	 * pipeline registry to the new config generation.
	 */
	SET_OFFSET_OF(
		&new_config_gen->pipeline_registry,
		ADDR_OF(&old_config_gen->pipeline_registry)
	);
	SET_OFFSET_OF(
		&new_config_gen->device_registry,
		ADDR_OF(&old_config_gen->device_registry)
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

		SET_OFFSET_OF(&new_module->data, ADDR_OF(&old_module->data));
	}
	new_module_registry->count = old_module_registry->count;

	// Update or insert new module data
	for (uint64_t new_idx = 0; new_idx < module_count; ++new_idx) {
		bool found = false;

		struct module_data *new_module_data = module_datas[new_idx];

		for (uint64_t old_idx = 0; old_idx < old_module_registry->count;
		     ++old_idx) {
			struct cp_module *new_module =
				new_module_registry->modules + old_idx;

			struct module_data *old_module_data =
				ADDR_OF(&new_module->data);

			if (new_module_data->index == old_module_data->index &&
			    !strncmp(
				    new_module_data->name,
				    old_module_data->name,
				    64
			    )) {
				struct agent *old_agent =
					ADDR_OF(&old_module_data->agent);
				old_agent->loaded_module_count -= 1;

				SET_OFFSET_OF(
					&new_module->data, new_module_data
				);
				new_module_data->gen = new_config_gen->gen;
				found = true;
				break;
			}
		}
		if (!found) {
			struct cp_module *new_module =
				new_module_registry->modules +
				new_module_registry->count;

			new_module_data->gen = new_config_gen->gen;
			SET_OFFSET_OF(&new_module->data, new_module_data);
			new_module_registry->count += 1;
		}

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
cp_config_gen_lookup_module(
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
		    !strncmp(name, ADDR_OF(&module->data)->name, 64)) {
			*res_index = idx;
			return 0;
		}
	}
	return -1;
}

/*
 * The routine updates pipelines configuration.
 */
int
cp_config_update_pipelines(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	uint64_t pipeline_count,
	struct pipeline_config *pipelines[]
) {
	cp_config_lock(cp_config);

	struct cp_config_gen *old_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);

	struct cp_config_gen *new_config_gen =
		(struct cp_config_gen *)memory_balloc(
			&cp_config->memory_context, sizeof(struct cp_config_gen)
		);

	new_config_gen->gen = old_config_gen->gen + 1;
	SET_OFFSET_OF(
		&new_config_gen->module_registry,
		ADDR_OF(&old_config_gen->module_registry)
	);
	SET_OFFSET_OF(
		&new_config_gen->device_registry,
		ADDR_OF(&old_config_gen->device_registry)
	);

	struct cp_pipeline_registry *old_pipeline_registry =
		ADDR_OF(&old_config_gen->pipeline_registry);

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
				goto unlock;
			}

			if (cp_config_gen_lookup_module(
				    new_config_gen,
				    index,
				    pipeline_config->modules[module_idx].name,
				    new_module_indexes + module_idx
			    )) {
				// FIXME: free resources
				goto unlock;
			}
		}

		struct cp_pipeline *cp_pipeline =
			new_pipeline_registry->pipelines + pipeline_idx;
		cp_pipeline->length = pipeline_config->length;
		SET_OFFSET_OF(&cp_pipeline->module_indexes, new_module_indexes);
	}

	new_pipeline_registry->count = pipeline_count;
	SET_OFFSET_OF(
		&new_config_gen->pipeline_registry, new_pipeline_registry
	);

	SET_OFFSET_OF(&new_config_gen->prev, old_config_gen);

	SET_OFFSET_OF(&cp_config->cp_config_gen, new_config_gen);

	dp_config_wait_for_gen(dp_config, new_config_gen->gen);

	SET_OFFSET_OF(&new_config_gen->prev, ADDR_OF(&old_config_gen->prev));

	for (uint64_t idx = 0; idx < old_pipeline_registry->count; ++idx) {
		struct cp_pipeline *pipeline =
			old_pipeline_registry->pipelines + idx;
		uint64_t *module_indexes = ADDR_OF(&pipeline->module_indexes);

		memory_bfree(
			&cp_config->memory_context,
			module_indexes,
			sizeof(uint64_t) * pipeline->length
		);
	}

	memory_bfree(
		&cp_config->memory_context,
		old_pipeline_registry,
		sizeof(struct cp_pipeline_registry
		) + sizeof(struct cp_pipeline) * old_pipeline_registry->count
	);

	memory_bfree(
		&cp_config->memory_context,
		old_config_gen,
		sizeof(struct cp_config_gen)
	);

unlock:
	cp_config_unlock(cp_config);
	return 0;
}

int
cp_config_update_devices(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	uint64_t device_count,
	uint64_t *pipelines
) {
	(void)dp_config;

	cp_config_lock(cp_config);

	struct cp_config_gen *old_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);

	struct cp_config_gen *new_config_gen =
		(struct cp_config_gen *)memory_balloc(
			&cp_config->memory_context, sizeof(struct cp_config_gen)
		);
	new_config_gen->gen = old_config_gen->gen + 1;
	SET_OFFSET_OF(
		&new_config_gen->module_registry,
		ADDR_OF(&old_config_gen->module_registry)
	);
	SET_OFFSET_OF(
		&new_config_gen->pipeline_registry,
		ADDR_OF(&old_config_gen->pipeline_registry)
	);

	struct cp_device_registry *new_device_registry =
		(struct cp_device_registry *)memory_balloc(
			&cp_config->memory_context,
			sizeof(struct cp_device_registry) +
				sizeof(uint64_t) * device_count
		);
	new_device_registry->count = device_count;
	for (uint64_t dev_idx = 0; dev_idx < device_count; ++dev_idx)
		new_device_registry->pipelines[dev_idx] = pipelines[dev_idx];

	SET_OFFSET_OF(&new_config_gen->device_registry, new_device_registry);

	// FIXME: prev for the first one config_gen is invalid
	SET_OFFSET_OF(&new_config_gen->prev, old_config_gen);

	SET_OFFSET_OF(&cp_config->cp_config_gen, new_config_gen);

	dp_config_wait_for_gen(dp_config, new_config_gen->gen);

	SET_OFFSET_OF(&new_config_gen->prev, ADDR_OF(&old_config_gen->prev));

	struct cp_device_registry *old_device_registry =
		ADDR_OF(&old_config_gen->device_registry);

	memory_bfree(
		&cp_config->memory_context,
		old_device_registry,
		sizeof(struct cp_device_registry) +
			sizeof(uint64_t) * old_device_registry->count
	);

	memory_bfree(
		&cp_config->memory_context,
		old_config_gen,
		sizeof(struct cp_config_gen)
	);

	cp_config_unlock(cp_config);
	return 0;
}
