#include "zone.h"

#include <unistd.h>

#include "cp_device.h"
#include "cp_module.h"
#include "cp_pipeline.h"

#include "lib/dataplane/config/zone.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/diag/diag.h"

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

// ------------ cp_config_gen

static inline void
cp_config_gen_free(
	struct cp_config *cp_config, struct cp_config_gen *config_gen
);

static inline struct cp_config_gen *
cp_config_gen_create_from(
	struct cp_config *cp_config, struct cp_config_gen *old_config_gen
) {
	struct cp_config_gen *new_config_gen =
		(struct cp_config_gen *)memory_balloc(
			&cp_config->memory_context, sizeof(struct cp_config_gen)
		);
	if (new_config_gen == NULL) {
		NEW_ERROR("failed to allocate memory for new config generation"
		);
		return NULL;
	}

	new_config_gen->gen = old_config_gen->gen + 1;

	SET_OFFSET_OF(
		&new_config_gen->dp_config, ADDR_OF(&old_config_gen->dp_config)
	);
	SET_OFFSET_OF(
		&new_config_gen->cp_config, ADDR_OF(&old_config_gen->cp_config)
	);

	if (cp_module_registry_copy(
		    &cp_config->memory_context,
		    &new_config_gen->module_registry,
		    &old_config_gen->module_registry
	    )) {
		NEW_ERROR("failed to copy module registry");
		goto error;
	}

	if (cp_function_registry_copy(
		    &cp_config->memory_context,
		    &new_config_gen->function_registry,
		    &old_config_gen->function_registry
	    )) {
		NEW_ERROR("failed to copy function registry");
		goto error;
	}

	if (cp_pipeline_registry_copy(
		    &cp_config->memory_context,
		    &new_config_gen->pipeline_registry,
		    &old_config_gen->pipeline_registry
	    )) {
		NEW_ERROR("failed to copy pipeline registry");
		goto error;
	}

	if (cp_device_registry_copy(
		    &cp_config->memory_context,
		    &new_config_gen->device_registry,
		    &old_config_gen->device_registry
	    )) {
		NEW_ERROR("failed to copy device registry");
		goto error;
	}

	if (cp_config_counter_storage_registry_init(
		    &cp_config->memory_context,
		    &new_config_gen->counter_storage_registry
	    )) {
		NEW_ERROR("failed to initialize counter storage registry");
		goto error;
	}

	return new_config_gen;

error:
	memory_bfree(
		&cp_config->memory_context,
		new_config_gen,
		sizeof(struct cp_config_gen)
	);
	return NULL;
}

static inline void
cp_config_gen_free(
	struct cp_config *cp_config, struct cp_config_gen *config_gen
) {
	(void)cp_config;
	cp_module_registry_destroy(&config_gen->module_registry);
	cp_function_registry_destroy(&config_gen->function_registry);
	cp_pipeline_registry_destroy(&config_gen->pipeline_registry);
	cp_device_registry_destroy(&config_gen->device_registry);

	struct config_gen_ectx *config_gen_ectx =
		ADDR_OF(&config_gen->config_gen_ectx);
	if (config_gen_ectx != NULL)
		config_gen_ectx_free(config_gen, config_gen_ectx);

	cp_config_counter_storage_registry_destroy(
		&config_gen->counter_storage_registry
	);
}

static inline int
cp_config_gen_install(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	struct cp_config_gen *new_config_gen
) {
	struct cp_config_gen *old_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);

	struct config_gen_ectx *new_config_gen_ectx =
		config_gen_ectx_create(new_config_gen, old_config_gen);
	if (new_config_gen_ectx == NULL) {
		PUSH_ERROR("in cp_config_gen_install");
		return -1;
	}

	SET_OFFSET_OF(&new_config_gen->config_gen_ectx, new_config_gen_ectx);

	SET_OFFSET_OF(&cp_config->cp_config_gen, new_config_gen);
	dp_config_wait_for_gen(dp_config, new_config_gen->gen);
	cp_config_gen_free(cp_config, old_config_gen);

	return 0;
}

int
cp_config_delete_module(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	const char *module_type,
	const char *module_name
) {
	cp_config_lock(cp_config);

	struct cp_config_gen *old_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);

	struct cp_config_gen *new_config_gen =
		cp_config_gen_create_from(cp_config, old_config_gen);
	if (new_config_gen == NULL) {
		PUSH_ERROR("failed to create new config generation in "
			   "cp_config_delete_module");
		goto error_unlock;
	}

	if (cp_module_registry_delete(
		    &new_config_gen->module_registry, module_type, module_name
	    )) {
		NEW_ERROR(
			"failed to delete module '%s:%s' from registry",
			module_type,
			module_name
		);
		goto error_free;
	}

	if (cp_config_gen_install(dp_config, cp_config, new_config_gen)) {
		PUSH_ERROR("failed to install config generation in "
			   "cp_config_delete_module");
		goto error_free;
	}
	cp_config_unlock(cp_config);

	return 0;

error_free:
	cp_config_gen_free(cp_config, new_config_gen);
error_unlock:
	cp_config_unlock(cp_config);
	return -1;
}

int
cp_config_update_modules(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	uint64_t module_count,
	struct cp_module **cp_modules
) {
	cp_config_lock(cp_config);

	struct cp_config_gen *old_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);
	struct cp_config_gen *new_config_gen =
		cp_config_gen_create_from(cp_config, old_config_gen);
	if (new_config_gen == NULL) {
		PUSH_ERROR("failed to create new config generation in "
			   "cp_config_update_modules");
		goto error_unlock;
	}

	for (uint64_t idx = 0; idx < module_count; ++idx) {
		struct cp_module *new_cp_module = cp_modules[idx];

		if (cp_module_registry_upsert(
			    &new_config_gen->module_registry,
			    new_cp_module->type,
			    new_cp_module->name,
			    new_cp_module
		    )) {
			NEW_ERROR(
				"failed to upsert module '%s:%s' into registry",
				new_cp_module->type,
				new_cp_module->name
			);
			goto error_free;
		}
	}

	if (cp_config_gen_install(dp_config, cp_config, new_config_gen)) {
		PUSH_ERROR("failed to install config generation in "
			   "cp_config_update_modules");
		goto error_free;
	}
	cp_config_unlock(cp_config);

	return 0;

error_free:
	cp_config_gen_free(cp_config, new_config_gen);
error_unlock:
	cp_config_unlock(cp_config);
	return -1;
}

/*
 * The routine updates functions configuration.
 */
int
cp_config_update_functions(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	uint64_t function_count,
	struct cp_function_config **cp_function_configs
) {
	cp_config_lock(cp_config);

	struct cp_config_gen *old_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);
	struct cp_config_gen *new_config_gen =
		cp_config_gen_create_from(cp_config, old_config_gen);
	if (new_config_gen == NULL) {
		PUSH_ERROR("failed to create new config generation in "
			   "cp_config_update_functions");
		goto error_unlock;
	}

	for (uint64_t idx = 0; idx < function_count; ++idx) {
		struct cp_function *new_cp_function = cp_function_create(
			&cp_config->memory_context,
			dp_config,
			new_config_gen,
			cp_function_configs[idx]
		);
		if (new_cp_function == NULL) {
			PUSH_ERROR("failed to create function in "
				   "cp_config_update_functions");
			goto error_free;
		}

		if (cp_function_registry_upsert(
			    &new_config_gen->function_registry,
			    new_cp_function->name,
			    new_cp_function
		    )) {
			NEW_ERROR(
				"failed to upsert function '%s' into registry",
				new_cp_function->name
			);
			goto error_free;
		}
	}

	if (cp_config_gen_install(dp_config, cp_config, new_config_gen)) {
		PUSH_ERROR("failed to install config generation in "
			   "cp_config_update_functions");
		goto error_free;
	}
	cp_config_unlock(cp_config);

	return 0;

error_free:
	cp_config_gen_free(cp_config, new_config_gen);
error_unlock:
	cp_config_unlock(cp_config);
	return -1;
}

int
cp_config_delete_function(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	const char *name
) {

	cp_config_lock(cp_config);

	struct cp_config_gen *old_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);

	struct cp_config_gen *new_config_gen =
		cp_config_gen_create_from(cp_config, old_config_gen);
	if (new_config_gen == NULL) {
		PUSH_ERROR("failed to create new config generation in "
			   "cp_config_delete_function");
		goto error_unlock;
	}

	if (cp_function_registry_delete(
		    &new_config_gen->function_registry, name
	    )) {
		NEW_ERROR("failed to delete function '%s' from registry", name);
		goto error_free;
	}

	if (cp_config_gen_install(dp_config, cp_config, new_config_gen)) {
		PUSH_ERROR("failed to install config generation in "
			   "cp_config_delete_function");
		goto error_free;
	}
	cp_config_unlock(cp_config);

	return 0;

error_free:
	cp_config_gen_free(cp_config, new_config_gen);
error_unlock:
	cp_config_unlock(cp_config);
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
	struct cp_pipeline_config **cp_pipeline_configs
) {
	cp_config_lock(cp_config);

	struct cp_config_gen *old_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);
	struct cp_config_gen *new_config_gen =
		cp_config_gen_create_from(cp_config, old_config_gen);

	if (new_config_gen == NULL) {
		PUSH_ERROR("failed to create new config generation in "
			   "cp_config_update_pipelines");
		goto error_unlock;
	}

	for (uint64_t idx = 0; idx < pipeline_count; ++idx) {
		struct cp_pipeline *new_cp_pipeline = cp_pipeline_create(
			&cp_config->memory_context,
			new_config_gen,
			cp_pipeline_configs[idx]
		);
		if (new_cp_pipeline == NULL) {
			PUSH_ERROR("failed to create pipeline in "
				   "cp_config_update_pipelines");
			goto error_free;
		}

		if (cp_pipeline_registry_upsert(
			    &new_config_gen->pipeline_registry,
			    new_cp_pipeline->name,
			    new_cp_pipeline
		    )) {
			NEW_ERROR(
				"failed to upsert pipeline '%s' into registry",
				new_cp_pipeline->name
			);
			goto error_free;
		}
	}

	if (cp_config_gen_install(dp_config, cp_config, new_config_gen)) {
		PUSH_ERROR("failed to install config generation in "
			   "cp_config_update_pipelines");
		goto error_free;
	}
	cp_config_unlock(cp_config);

	return 0;

error_free:
	cp_config_gen_free(cp_config, new_config_gen);
error_unlock:
	cp_config_unlock(cp_config);
	return -1;
}

int
cp_config_delete_pipeline(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	const char *name
) {

	cp_config_lock(cp_config);

	struct cp_config_gen *old_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);

	uint64_t index;
	if (cp_config_gen_lookup_pipeline_index(old_config_gen, name, &index)) {
		NEW_ERROR("pipeline '%s' not found", name);
		goto error_unlock;
	}

	struct cp_config_gen *new_config_gen =
		cp_config_gen_create_from(cp_config, old_config_gen);
	if (new_config_gen == NULL) {
		PUSH_ERROR("failed to create new config generation in "
			   "cp_config_delete_pipeline");
		goto error_unlock;
	}

	if (cp_pipeline_registry_delete(
		    &new_config_gen->pipeline_registry, name
	    )) {
		NEW_ERROR("failed to delete pipeline '%s' from registry", name);
		goto error_free;
	}

	if (cp_config_gen_install(dp_config, cp_config, new_config_gen)) {
		PUSH_ERROR("failed to install config generation in "
			   "cp_config_delete_pipeline");
		goto error_free;
	}
	cp_config_unlock(cp_config);

	return 0;

error_free:
	cp_config_gen_free(cp_config, new_config_gen);
error_unlock:
	cp_config_unlock(cp_config);
	return -1;
}

struct cp_module *
cp_config_gen_lookup_module(
	struct cp_config_gen *config_gen, const char *type, const char *name
) {
	return cp_module_registry_lookup(
		&config_gen->module_registry, type, name
	);
}

struct cp_function *
cp_config_gen_lookup_function(
	struct cp_config_gen *config_gen, const char *name
) {
	return cp_function_registry_lookup(
		&config_gen->function_registry, name
	);
}

struct cp_pipeline *
cp_config_gen_lookup_pipeline(
	struct cp_config_gen *config_gen, const char *name
) {
	return cp_pipeline_registry_lookup(
		&config_gen->pipeline_registry, name
	);
}

int
cp_config_gen_lookup_function_index(
	struct cp_config_gen *config_gen, const char *name, uint64_t *index
) {
	return cp_function_registry_lookup_index(
		&config_gen->function_registry, name, index
	);
}

int
cp_config_gen_lookup_pipeline_index(
	struct cp_config_gen *config_gen, const char *name, uint64_t *index
) {
	return cp_pipeline_registry_lookup_index(
		&config_gen->pipeline_registry, name, index
	);
}

int
cp_config_update_devices(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	uint64_t device_count,
	struct cp_device *devices[]
) {
	// TODO weight clamp
	cp_config_lock(cp_config);

	struct cp_config_gen *old_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);
	struct cp_config_gen *new_config_gen =
		cp_config_gen_create_from(cp_config, old_config_gen);
	if (new_config_gen == NULL) {
		PUSH_ERROR("failed to create new config generation in "
			   "cp_config_update_devices");
		goto error_unlock;
	}

	for (uint64_t idx = 0; idx < device_count; ++idx) {
		if (cp_device_registry_upsert(
			    &new_config_gen->device_registry,
			    devices[idx]->name,
			    devices[idx]
		    )) {
			NEW_ERROR(
				"failed to upsert device '%s' into registry",
				devices[idx]->name
			);
			goto error_free;
		}
	}

	if (cp_config_gen_install(dp_config, cp_config, new_config_gen)) {
		PUSH_ERROR("failed to install config generation in "
			   "cp_config_update_devices");
		goto error_free;
	}
	cp_config_unlock(cp_config);

	return 0;

error_free:
	cp_config_gen_free(cp_config, new_config_gen);
error_unlock:
	cp_config_unlock(cp_config);
	return -1;
}

struct cp_config_gen *
cp_config_gen_create(struct agent *agent) {
	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);
	struct cp_config_gen *cp_config_gen =
		(struct cp_config_gen *)memory_balloc(
			&cp_config->memory_context, sizeof(struct cp_config_gen)
		);

	if (cp_config_gen == NULL) {
		NEW_ERROR("failed to allocate memory for initial config "
			  "generation");
		return NULL;
	}
	cp_config_gen->gen = 0;
	SET_OFFSET_OF(
		&cp_config_gen->dp_config, ADDR_OF(&cp_config->dp_config)
	);
	SET_OFFSET_OF(&cp_config_gen->cp_config, cp_config);

	if (cp_module_registry_init(
		    &cp_config->memory_context, &cp_config_gen->module_registry
	    )) {
		NEW_ERROR("failed to initialize module registry");
		goto error;
	}

	if (cp_function_registry_init(
		    &cp_config->memory_context,
		    &cp_config_gen->function_registry
	    )) {
		NEW_ERROR("failed to initialize function registry");
		goto error;
	}

	if (cp_pipeline_registry_init(
		    &cp_config->memory_context,
		    &cp_config_gen->pipeline_registry
	    )) {
		NEW_ERROR("failed to initialize pipeline registry");
		goto error;
	}

	if (cp_device_registry_init(
		    &cp_config->memory_context, &cp_config_gen->device_registry
	    )) {
		NEW_ERROR("failed to initialize device registry");
		goto error;
	}

	if (cp_config_counter_storage_registry_init(
		    &cp_config->memory_context,
		    &cp_config_gen->counter_storage_registry
	    )) {
		NEW_ERROR("failed to initialize counter storage registry");
		goto error;
	}

	// Create phy devices
	for (uint64_t idx = 0; idx < dp_config->dp_topology.device_count;
	     ++idx) {

		struct cp_device_config device_config;
		memset(&device_config, 0, sizeof(struct cp_device_config));
		strtcpy(device_config.name,
			ADDR_OF(&dp_config->dp_topology.devices)[idx].port_name,
			CP_DEVICE_NAME_LEN);
		strtcpy(device_config.type, "plain", sizeof(device_config.type)
		);
		struct cp_device_entry_config pipe_cfg;
		pipe_cfg.count = 0;
		device_config.input_pipelines = &pipe_cfg;
		device_config.output_pipelines = &pipe_cfg;
		struct cp_device *cp_device =
			cp_device_create(agent, &device_config);
		if (cp_device == NULL) {
			NEW_ERROR(
				"failed to create physical device '%s'",
				device_config.name
			);
			return NULL;
		}
		if (cp_device_registry_upsert(
			    &cp_config_gen->device_registry,
			    device_config.name,
			    cp_device
		    )) {
			NEW_ERROR(
				"failed to upsert physical device '%s' into "
				"registry",
				device_config.name
			);
			return NULL;
		}
	}

	return cp_config_gen;

error:
	memory_bfree(
		&cp_config->memory_context,
		cp_config_gen,
		sizeof(struct cp_config_gen)
	);
	return NULL;
}
