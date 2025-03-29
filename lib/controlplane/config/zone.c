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

static inline int
cp_config_gen_lookup_module_index(
	struct cp_config_gen *cp_config_gen,
	uint64_t index,
	const char *name,
	uint64_t *res_index
) {
	struct cp_module_registry *module_registry =
		ADDR_OF(&cp_config_gen->module_registry);
	for (uint64_t idx = 0; idx < module_registry->count; ++idx) {
		struct module_data *module_data =
			ADDR_OF(module_registry->modules + idx);
		if (module_data == NULL)
			continue;

		if (index == module_data->index &&
		    !strncmp(
			    name, module_data->name, CP_MODULE_DATA_NAME_LEN
		    )) {
			*res_index = idx;
			return 0;
		}
	}
	return -1;
}

// ------------ module data

static inline void
module_data_ref(struct module_data *module_data) {
	module_data->refcnt += 1;
}

static inline void
module_data_unref(
	struct cp_config *cp_config, struct module_data *module_data
) {
	(void)cp_config;

	module_data->refcnt -= 1;
	if (module_data->refcnt == 0) {
		struct agent *agent = ADDR_OF(&module_data->agent);
		// Put the data in the owning context free space
		SET_OFFSET_OF(&module_data->prev, agent->unused_module);
		SET_OFFSET_OF(&agent->unused_module, module_data);
	}
}

// ------------ module registry

static inline uint64_t
cp_module_registry_size(uint64_t capacity) {
	return sizeof(struct cp_module_registry) +
	       sizeof(struct module_data *) * capacity;
}

static inline struct cp_module_registry *
cp_module_registry_create(struct cp_config *cp_config, uint64_t capacity) {
	struct cp_module_registry *new_module_registry =
		(struct cp_module_registry *)memory_balloc(
			&cp_config->memory_context,
			cp_module_registry_size(capacity)
		);
	if (new_module_registry == NULL)
		return NULL;

	new_module_registry->refcnt = 0;
	new_module_registry->capacity = capacity;
	new_module_registry->count = 0;

	for (uint64_t idx = 0; idx < capacity; ++idx)
		SET_OFFSET_OF(new_module_registry->modules + idx, NULL);

	return new_module_registry;
}

static inline struct cp_module_registry *
cp_module_registry_spawn(
	struct cp_config *cp_config,
	struct cp_module_registry *old_module_registry,
	uint64_t capacity
) {
	if (capacity < old_module_registry->count)
		return NULL;

	struct cp_module_registry *new_module_registry =
		cp_module_registry_create(cp_config, capacity);
	if (new_module_registry == NULL)
		return NULL;

	new_module_registry->refcnt = 0;
	new_module_registry->capacity = capacity;

	new_module_registry->count = old_module_registry->count;
	for (uint64_t idx = 0; idx < old_module_registry->count; ++idx) {
		struct module_data *module_data =
			ADDR_OF(old_module_registry->modules + idx);

		if (module_data != NULL)
			module_data_ref(module_data);

		SET_OFFSET_OF(new_module_registry->modules + idx, module_data);
	}

	for (uint64_t idx = old_module_registry->count; idx < capacity; ++idx)
		SET_OFFSET_OF(new_module_registry->modules + idx, NULL);

	return new_module_registry;
}

static inline void
cp_module_registry_free(
	struct cp_config *cp_config, struct cp_module_registry *module_registry
) {
	for (uint64_t idx = 0; idx < module_registry->count; ++idx) {
		struct module_data *module_data =
			ADDR_OF(module_registry->modules + idx);

		if (module_data == NULL)
			continue;

		module_data_unref(cp_config, module_data);
	}

	memory_bfree(
		&cp_config->memory_context,
		module_registry,
		sizeof(struct cp_module_registry) +
			sizeof(struct module_data) * module_registry->capacity
	);
}

static inline int
cp_module_registry_update(
	struct cp_config *cp_config,
	struct cp_module_registry *module_registry,
	struct module_data *new_module_data
) {
	uint64_t unused_pos = (uint64_t)-1;
	for (uint64_t idx = 0; idx < module_registry->count; ++idx) {
		struct module_data *old_module_data =
			ADDR_OF(module_registry->modules + idx);
		if (old_module_data == NULL) {
			unused_pos = idx;
			continue;
		}

		if (old_module_data->index == new_module_data->index &&
		    !strncmp(
			    old_module_data->name,
			    new_module_data->name,
			    CP_MODULE_DATA_NAME_LEN
		    )) {
			module_data_ref(new_module_data);
			SET_OFFSET_OF(
				module_registry->modules + idx, new_module_data
			);

			module_data_unref(cp_config, old_module_data);

			return 0;
		}
	}

	if (unused_pos == (uint64_t)-1) {
		if (module_registry->count == module_registry->capacity)
			return -1;

		unused_pos = module_registry->count++;
	}

	module_data_ref(new_module_data);
	SET_OFFSET_OF(module_registry->modules + unused_pos, new_module_data);

	return 0;
}

static inline void
cp_module_registry_ref(struct cp_module_registry *module_registry) {
	module_registry->refcnt += 1;
}

static inline void
cp_module_registry_unref(
	struct cp_config *cp_config, struct cp_module_registry *module_registry
) {
	module_registry->refcnt -= 1;
	if (module_registry->refcnt == 0) {
		cp_module_registry_free(cp_config, module_registry);
	}
}

// ------------ pipeline registry

static inline uint64_t
cp_pipeline_size(uint64_t length) {
	return sizeof(struct cp_pipeline) + sizeof(uint64_t) * length;
}

static inline void
cp_pipeline_free(struct cp_config *cp_config, struct cp_pipeline *pipeline) {
	memory_bfree(
		&cp_config->memory_context,
		pipeline,
		cp_pipeline_size(pipeline->length)
	);
}

static inline struct cp_pipeline *
cp_pipeline_make(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	struct pipeline_config *pipeline_config
) {
	struct cp_pipeline *new_pipeline = (struct cp_pipeline *)memory_balloc(
		&cp_config->memory_context,
		cp_pipeline_size(pipeline_config->length)
	);
	if (new_pipeline == NULL) {
		return NULL;
	}

	new_pipeline->refcnt = 0;

	new_pipeline->length = pipeline_config->length;
	strtcpy(new_pipeline->name, pipeline_config->name, CP_PIPELINE_NAME_LEN
	);

	struct cp_config_gen *config_gen = ADDR_OF(&cp_config->cp_config_gen);
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

	return new_pipeline;

error:
	cp_pipeline_free(cp_config, new_pipeline);
	return NULL;
}

static inline void
cp_pipeline_ref(struct cp_pipeline *pipeline) {
	pipeline->refcnt += 1;
}

static inline void
cp_pipeline_unref(struct cp_config *cp_config, struct cp_pipeline *pipeline) {
	pipeline->refcnt -= 1;
	if (pipeline->refcnt == 0) {
		cp_pipeline_free(cp_config, pipeline);
	}
}

static inline uint64_t
cp_pipeline_registry_size(uint64_t capacity) {
	return sizeof(struct cp_pipeline_registry) +
	       sizeof(struct cp_pipeline *) * capacity;
}

static inline struct cp_pipeline_registry *
cp_pipeline_registry_create(struct cp_config *cp_config, uint64_t capacity) {
	struct cp_pipeline_registry *new_pipeline_registry =
		(struct cp_pipeline_registry *)memory_balloc(
			&cp_config->memory_context,
			cp_pipeline_registry_size(capacity)
		);
	if (new_pipeline_registry == NULL)
		return NULL;

	new_pipeline_registry->refcnt = 0;
	new_pipeline_registry->capacity = capacity;
	new_pipeline_registry->count = 0;

	for (uint64_t idx = 0; idx < capacity; ++idx)
		SET_OFFSET_OF(new_pipeline_registry->pipelines + idx, NULL);

	return new_pipeline_registry;
}

static inline struct cp_pipeline_registry *
cp_pipeline_registry_spawn(
	struct cp_config *cp_config,
	struct cp_pipeline_registry *old_pipeline_registry,
	uint64_t capacity
) {
	if (capacity < old_pipeline_registry->count)
		return NULL;

	struct cp_pipeline_registry *new_pipeline_registry =
		cp_pipeline_registry_create(cp_config, capacity);
	if (new_pipeline_registry == NULL)
		return NULL;

	new_pipeline_registry->count = old_pipeline_registry->count;
	for (uint64_t idx = 0; idx < old_pipeline_registry->count; ++idx) {
		struct cp_pipeline *pipeline =
			ADDR_OF(old_pipeline_registry->pipelines + idx);

		if (pipeline == NULL)
			continue;

		cp_pipeline_ref(pipeline);
		SET_OFFSET_OF(new_pipeline_registry->pipelines + idx, pipeline);
	}

	return new_pipeline_registry;
}

static inline void
cp_pipeline_registry_free(
	struct cp_config *cp_config,
	struct cp_pipeline_registry *pipeline_registry
) {
	for (uint64_t idx = 0; idx < pipeline_registry->count; ++idx) {
		struct cp_pipeline *pipeline =
			ADDR_OF(pipeline_registry->pipelines + idx);

		if (pipeline == NULL)
			continue;

		cp_pipeline_unref(cp_config, pipeline);
	}

	memory_bfree(
		&cp_config->memory_context,
		pipeline_registry,
		sizeof(struct cp_pipeline_registry
		) + sizeof(struct cp_pipeline *) * pipeline_registry->capacity
	);
}

static inline void
cp_pipeline_registry_unref(
	struct cp_config *cp_config,
	struct cp_pipeline_registry *pipeline_registry
) {
	pipeline_registry->refcnt -= 1;
	if (pipeline_registry->refcnt == 0) {
		cp_pipeline_registry_free(cp_config, pipeline_registry);
	}
}

static inline void
cp_pipeline_registry_ref(struct cp_pipeline_registry *pipeline_registry) {
	pipeline_registry->refcnt += 1;
}

// ------------ device registry

static inline uint64_t
cp_device_size(uint64_t size) {
	return sizeof(struct cp_device) + sizeof(uint64_t) * size;
}

static inline void
cp_device_free(struct cp_config *cp_config, struct cp_device *device) {
	memory_bfree(
		&cp_config->memory_context, device, cp_device_size(device->size)
	);
}

static inline void
cp_device_ref(struct cp_device *device) {
	device->refcnt += 1;
}

static inline void
cp_device_unref(struct cp_config *cp_config, struct cp_device *device) {
	device->refcnt -= 1;
	if (device->refcnt == 0) {
		cp_device_free(cp_config, device);
	}
}

static inline uint64_t
cp_device_registry_size(uint64_t capacity) {
	return sizeof(struct cp_device_registry) +
	       sizeof(struct cp_device *) * capacity;
}

static inline struct cp_device_registry *
cp_device_registry_create(struct cp_config *cp_config, uint64_t capacity) {
	struct cp_device_registry *new_device_registry =
		(struct cp_device_registry *)memory_balloc(
			&cp_config->memory_context,
			cp_device_registry_size(capacity)
		);
	if (new_device_registry == NULL)
		return NULL;

	new_device_registry->refcnt = 0;
	new_device_registry->capacity = capacity;
	new_device_registry->count = 0;

	for (uint64_t idx = 0; idx < capacity; ++idx)
		SET_OFFSET_OF(new_device_registry->devices + idx, NULL);

	return new_device_registry;
}

static inline struct cp_device_registry *
cp_device_registry_spawn(
	struct cp_config *cp_config,
	struct cp_device_registry *old_device_registry,
	uint64_t capacity
) {
	if (capacity < old_device_registry->count)
		return NULL;

	struct cp_device_registry *new_device_registry =
		cp_device_registry_create(cp_config, capacity);
	if (new_device_registry == NULL)
		return new_device_registry;

	new_device_registry->count = old_device_registry->count;
	for (uint64_t idx = 0; idx < old_device_registry->count; ++idx) {
		struct cp_device *device =
			ADDR_OF(old_device_registry->devices + idx);

		if (device != NULL)
			cp_device_ref(device);

		SET_OFFSET_OF(new_device_registry->devices + idx, device);
	}

	for (uint64_t idx = old_device_registry->count; idx < capacity; ++idx) {
		SET_OFFSET_OF(new_device_registry->devices + idx, NULL);
	}

	return new_device_registry;
}

static inline void
cp_device_registry_free(
	struct cp_config *cp_config, struct cp_device_registry *device_registry
) {
	for (uint64_t idx = 0; idx < device_registry->count; ++idx) {
		struct cp_device *device =
			ADDR_OF(device_registry->devices + idx);

		if (device == NULL)
			continue;

		cp_device_unref(cp_config, device);
	}

	memory_bfree(
		&cp_config->memory_context,
		device_registry,
		sizeof(struct cp_device_registry) +
			sizeof(struct cp_device_pipeline_map *) *
				device_registry->capacity
	);
}

static inline void
cp_device_registry_ref(struct cp_device_registry *device_registry) {
	device_registry->refcnt += 1;
}

static inline void
cp_device_registry_unref(
	struct cp_config *cp_config, struct cp_device_registry *device_registry
) {
	device_registry->refcnt -= 1;
	if (device_registry->refcnt == 0) {
		cp_device_registry_free(cp_config, device_registry);
	}
}

// ------------ cp_config_gen

static inline struct cp_config_gen *
cp_config_gen_spawn(
	struct cp_config *cp_config, struct cp_config_gen *config_gen
) {
	struct cp_config_gen *new_config_gen =
		(struct cp_config_gen *)memory_balloc(
			&cp_config->memory_context, sizeof(struct cp_config_gen)
		);
	if (new_config_gen == NULL)
		return NULL;

	new_config_gen->gen = config_gen->gen + 1;
	SET_OFFSET_OF(&new_config_gen->prev, config_gen);

	struct cp_module_registry *module_registry =
		ADDR_OF(&config_gen->module_registry);
	cp_module_registry_ref(module_registry);
	SET_OFFSET_OF(&new_config_gen->module_registry, module_registry);

	struct cp_pipeline_registry *pipeline_registry =
		ADDR_OF(&config_gen->pipeline_registry);
	cp_pipeline_registry_ref(pipeline_registry);
	SET_OFFSET_OF(&new_config_gen->pipeline_registry, pipeline_registry);

	struct cp_device_registry *device_registry =
		ADDR_OF(&config_gen->device_registry);
	cp_device_registry_ref(device_registry);
	SET_OFFSET_OF(&new_config_gen->device_registry, device_registry);

	return new_config_gen;
}

static inline void
cp_config_gen_free(
	struct cp_config *cp_config, struct cp_config_gen *config_gen
) {
	struct cp_module_registry *module_registry =
		ADDR_OF(&config_gen->module_registry);
	cp_module_registry_unref(cp_config, module_registry);

	struct cp_pipeline_registry *pipeline_registry =
		ADDR_OF(&config_gen->pipeline_registry);
	cp_pipeline_registry_unref(cp_config, pipeline_registry);

	struct cp_device_registry *device_registry =
		ADDR_OF(&config_gen->device_registry);
	cp_device_registry_unref(cp_config, device_registry);
}

static inline void
cp_config_gen_replace_module(
	struct cp_config *cp_config,
	struct cp_config_gen *config_gen,
	struct cp_module_registry *module_registry
) {
	struct cp_module_registry *old_module_registry =
		ADDR_OF(&config_gen->module_registry);

	cp_module_registry_ref(module_registry);
	SET_OFFSET_OF(&config_gen->module_registry, module_registry);

	cp_module_registry_unref(cp_config, old_module_registry);
}

static inline void
cp_config_gen_replace_pipeline(
	struct cp_config *cp_config,
	struct cp_config_gen *config_gen,
	struct cp_pipeline_registry *pipeline_registry
) {
	struct cp_pipeline_registry *old_pipeline_registry =
		ADDR_OF(&config_gen->pipeline_registry);

	cp_pipeline_registry_ref(pipeline_registry);
	SET_OFFSET_OF(&config_gen->pipeline_registry, pipeline_registry);

	cp_pipeline_registry_unref(cp_config, old_pipeline_registry);
}

static inline void
cp_config_gen_replace_device(
	struct cp_config *cp_config,
	struct cp_config_gen *config_gen,
	struct cp_device_registry *device_registry
) {
	struct cp_device_registry *old_device_registry =
		ADDR_OF(&config_gen->device_registry);

	cp_device_registry_ref(device_registry);
	SET_OFFSET_OF(&config_gen->device_registry, device_registry);

	cp_device_registry_unref(cp_config, old_device_registry);
}

static inline void
cp_config_gen_install(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	struct cp_config_gen *new_config_gen
) {
	struct cp_config_gen *old_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);

	SET_OFFSET_OF(&cp_config->cp_config_gen, new_config_gen);
	dp_config_wait_for_gen(dp_config, new_config_gen->gen);
	cp_config_gen_free(cp_config, old_config_gen);
}

int
cp_config_update_modules(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	uint64_t module_count,
	struct module_data **module_datas
) {
	cp_config_lock(cp_config);

	struct cp_config_gen *old_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);

	struct cp_module_registry *old_module_registry =
		ADDR_OF(&old_config_gen->module_registry);

	struct cp_module_registry *new_module_registry =
		cp_module_registry_spawn(
			cp_config,
			old_module_registry,
			old_module_registry->count + module_count
		);

	if (new_module_registry == NULL) {
		goto error_unlock;
	}

	for (uint64_t idx = 0; idx < module_count; ++idx) {
		struct module_data *new_module_data = module_datas[idx];

		new_module_data->refcnt = 0;

		if (cp_module_registry_update(
			    cp_config, new_module_registry, new_module_data
		    )) {
			goto error_free;
		}
	}

	struct cp_config_gen *new_config_gen =
		cp_config_gen_spawn(cp_config, old_config_gen);
	if (new_config_gen == NULL) {
		goto error_free;
	}

	cp_config_gen_replace_module(
		cp_config, new_config_gen, new_module_registry
	);
	cp_config_gen_install(dp_config, cp_config, new_config_gen);
	cp_config_unlock(cp_config);

	return 0;

error_free:
	cp_module_registry_free(cp_config, new_module_registry);
error_unlock:
	cp_config_unlock(cp_config);
	return -1;
}

static inline int
cp_pipeline_registry_update(
	struct cp_config *cp_config,
	struct cp_pipeline_registry *pipeline_registry,
	struct cp_pipeline *new_pipeline
) {
	uint64_t unused_pos = (uint64_t)-1;
	for (uint64_t idx = 0; idx < pipeline_registry->count; ++idx) {
		struct cp_pipeline *old_pipeline =
			ADDR_OF(pipeline_registry->pipelines + idx);
		if (old_pipeline == NULL) {
			unused_pos = idx;
			continue;
		}

		if (!strncmp(
			    old_pipeline->name,
			    new_pipeline->name,
			    CP_PIPELINE_NAME_LEN
		    )) {
			cp_pipeline_ref(new_pipeline);
			SET_OFFSET_OF(
				pipeline_registry->pipelines + idx, new_pipeline
			);

			cp_pipeline_unref(cp_config, old_pipeline);

			return 0;
		}
	}

	if (unused_pos == (uint64_t)-1) {
		if (pipeline_registry->count == pipeline_registry->capacity)
			return -1;

		unused_pos = pipeline_registry->count++;
	}

	cp_pipeline_ref(new_pipeline);
	SET_OFFSET_OF(pipeline_registry->pipelines + unused_pos, new_pipeline);

	return 0;
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

	struct cp_pipeline_registry *new_pipeline_registry =
		cp_pipeline_registry_spawn(
			cp_config,
			old_pipeline_registry,
			old_pipeline_registry->count + pipeline_count
		);

	if (new_pipeline_registry == NULL) {
		goto error_unlock;
	}

	for (uint64_t idx = 0; idx < pipeline_count; ++idx) {
		struct cp_pipeline *new_pipeline = cp_pipeline_make(
			dp_config, cp_config, pipeline_configs[idx]
		);
		if (new_pipeline == NULL) {
			goto error_free;
		}

		if (cp_pipeline_registry_update(
			    cp_config, new_pipeline_registry, new_pipeline
		    )) {
			goto error_free;
		}
	}

	struct cp_config_gen *new_config_gen =
		cp_config_gen_spawn(cp_config, old_config_gen);
	if (new_config_gen == NULL) {
		goto error_free;
	}

	cp_config_gen_replace_pipeline(
		cp_config, new_config_gen, new_pipeline_registry
	);
	cp_config_gen_install(dp_config, cp_config, new_config_gen);
	cp_config_unlock(cp_config);

	return 0;

error_free:
	cp_pipeline_registry_free(cp_config, new_pipeline_registry);
error_unlock:
	cp_config_unlock(cp_config);
	return -1;
}

static inline int
cp_config_gen_lookup_pipeline_index(
	struct cp_config_gen *config_gen, const char *name, uint64_t *index
) {
	struct cp_pipeline_registry *pipeline_registry =
		ADDR_OF(&config_gen->pipeline_registry);

	for (uint64_t idx = 0; idx < pipeline_registry->count; ++idx) {
		struct cp_pipeline *pipeline =
			ADDR_OF(pipeline_registry->pipelines + idx);
		if (pipeline == NULL)
			continue;

		if (!strncmp(name, pipeline->name, CP_PIPELINE_NAME_LEN)) {
			*index = idx;
			return 0;
		}
	}

	return -1;
}

static inline struct cp_device *
cp_device_make(
	struct cp_config *cp_config,
	struct cp_config_gen *config_gen,
	struct device_pipeline_map *pipeline_map
) {
	uint64_t size = 0;
	for (uint64_t idx = 0; idx < pipeline_map->count; ++idx) {
		struct pipeline_weight *pipeline_weight =
			pipeline_map->pipelines + idx;
		size += pipeline_weight->weight;
	}

	struct cp_device *device = (struct cp_device *)memory_balloc(
		&cp_config->memory_context, cp_device_size(size)
	);
	if (device == NULL) {
		goto error;
	}

	device->refcnt = 0;
	device->size = size;

	uint64_t pos = 0;

	for (uint64_t idx = 0; idx < pipeline_map->count; ++idx) {
		struct pipeline_weight *pipeline_weight =
			pipeline_map->pipelines + idx;

		uint64_t pipeline_idx = 0;
		if (cp_config_gen_lookup_pipeline_index(
			    config_gen, pipeline_weight->name, &pipeline_idx
		    )) {
			goto error_free;
		}

		size = pipeline_weight->weight;
		while (size--) {
			device->pipelines[pos++] = pipeline_idx;
		}
	}

	return device;

error_free:
	cp_device_free(cp_config, device);
error:
	return NULL;
}

static inline int
cp_device_registry_update(
	struct cp_config *cp_config,
	struct cp_device_registry *device_registry,
	struct cp_device *new_device,
	uint64_t device_idx
) {
	if (device_idx >= device_registry->capacity)
		return -1;

	struct cp_device *old_device =
		ADDR_OF(device_registry->devices + device_idx);
	cp_device_ref(new_device);
	SET_OFFSET_OF(device_registry->devices + device_idx, new_device);

	if (old_device != NULL) {
		cp_device_unref(cp_config, old_device);
	}

	if (device_idx >= device_registry->count)
		device_registry->count = device_idx + 1;

	return 0;
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

	struct cp_device_registry *old_device_registry =
		ADDR_OF(&old_config_gen->device_registry);

	struct cp_device_registry *new_device_registry =
		cp_device_registry_spawn(
			cp_config,
			old_device_registry,
			dp_config->dp_topology.device_count
		);
	if (new_device_registry == NULL) {
		goto error_unlock;
	}

	for (uint64_t idx = 0; idx < device_count; ++idx) {
		struct cp_device *device = cp_device_make(
			cp_config, old_config_gen, pipeline_maps[idx]
		);

		if (cp_device_registry_update(
			    cp_config,
			    new_device_registry,
			    device,
			    pipeline_maps[idx]->device_id
		    )) {
			goto error_free;
		}
	}

	struct cp_config_gen *new_config_gen =
		cp_config_gen_spawn(cp_config, old_config_gen);
	if (new_config_gen == NULL) {
		goto error_free;
	}

	cp_config_gen_replace_device(
		cp_config, new_config_gen, new_device_registry
	);
	cp_config_gen_install(dp_config, cp_config, new_config_gen);
	cp_config_unlock(cp_config);

	return 0;

error_free:
	cp_device_registry_free(cp_config, new_device_registry);
error_unlock:
	cp_config_unlock(cp_config);
	return -1;
}

struct cp_config_gen *
cp_config_gen_create(struct cp_config *cp_config) {
	struct cp_config_gen *cp_config_gen =
		(struct cp_config_gen *)memory_balloc(
			&cp_config->memory_context, sizeof(struct cp_config_gen)
		);
	cp_config_gen->gen = 0;

	struct cp_module_registry *cp_module_registry =
		cp_module_registry_create(cp_config, 0);
	cp_module_registry_ref(cp_module_registry);
	SET_OFFSET_OF(&cp_config_gen->module_registry, cp_module_registry);

	struct cp_pipeline_registry *cp_pipeline_registry =
		cp_pipeline_registry_create(cp_config, 0);
	cp_pipeline_registry_ref(cp_pipeline_registry);
	SET_OFFSET_OF(&cp_config_gen->pipeline_registry, cp_pipeline_registry);

	struct cp_device_registry *cp_device_registry =
		cp_device_registry_create(cp_config, 0);
	cp_device_registry_ref(cp_device_registry);
	SET_OFFSET_OF(&cp_config_gen->device_registry, cp_device_registry);

	return cp_config_gen;
}
