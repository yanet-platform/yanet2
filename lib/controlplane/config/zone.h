#pragma once

#include <stdbool.h>
#include <stdint.h>

#include <sys/types.h>

#include "common/memory.h"

#include "counters/counters.h"

#include "dataplane/config/zone.h"

#include "controlplane/config/cp_device.h"
#include "controlplane/config/cp_module.h"
#include "controlplane/config/cp_pipeline.h"

#include "controlplane/config/cp_counter.h"

struct dp_config;
struct cp_config;
struct cp_config_gen;

/*
 * Configuration generation denotes a snapshot of controlplane
 * packet processing configuration. It contains module and pipeline
 * registries also with pipeline to device binding.
 *
 * On each update a new copy of the current active configuration generation
 * is instantiated and modified. After all updates are done the new generation
 * replaces an old one. However the previous could be still in use by
 * dataplane workers so the updater should wait until new generation reaches
 * all workers before resorce freeing.
 */
struct cp_config_gen {
	uint64_t gen;

	struct cp_config *cp_config;
	struct dp_config *dp_config;

	struct cp_module_registry module_registry;
	struct cp_pipeline_registry pipeline_registry;
	struct cp_device_registry device_registry;

	struct cp_pipeline_module_counter_storage_registry
		pipeline_module_counter_storage_registry;
	struct cp_pipeline_counter_storage_registry
		pipeline_counter_storage_registry;
};

struct agent;
/*
 * The structure contains agents attached to controplane configuration
 * zone.
 */
struct cp_agent_registry;
struct cp_agent_registry {
	uint64_t count;
	struct agent *agents[];
};

/*
 * Controplane configuration memory zone entry point.
 * This structure is placed just after controplane start address
 * and used for any controplane configuration manipulations.
 */
struct cp_config {
	/*
	 * The allocator owns whole controplane memory zone except
	 * this structure itself.
	 */
	struct block_allocator block_allocator;
	/*
	 * Controlplane memory context used to provide access to the
	 * allocator and account memory operations.
	 */
	struct memory_context memory_context;
	/*
	 * Relative porinter to the corresponding dataplane memory zone
	 * structure.
	 */
	struct dp_config *dp_config;
	/*
	 * Identifier of a process changinf the controplane configuration.
	 */
	pid_t config_lock;

	/*
	 * Relative pointer to the current active packet processing
	 * configuration.
	 */
	struct cp_config_gen *cp_config_gen;

	/*
	 * Registry of agent attached to the controplane configuration
	 * memory zone.
	 */
	struct cp_agent_registry *agent_registry;
	/*
	 * Allocator for counter backend storage
	 */
	struct counter_storage_allocator counter_storage_allocator;
};

/*
 * Try to lock controlplane configuration.
 * The function does not support recursive locking.
 */
bool
cp_config_try_lock(struct cp_config *cp_config);

/*
 * Wait until controplane is locked by the current process.
 * The function does not support recursive locking.
 */
void
cp_config_lock(struct cp_config *cp_config);

/*
 * Unlock controplane configuration.
 * The function returns false in case when controplane was not locked
 * by the current process.
 */
bool
cp_config_unlock(struct cp_config *cp_config);

/*
 * The routine updates one or more module confings linking them into
 * existing pipelines. Already existing modules are updated preserving its
 * index while new modules are to be appended to the tail of module list.
 * This means that pipilenes are not mutating here except address recoding to
 * the new configuration generation container.
 */
int
cp_config_update_modules(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	uint64_t module_count,
	struct cp_module **cp_modules
);

int
cp_config_update_pipelines(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	uint64_t pipeline_count,
	struct pipeline_config **pipelines
);

int
cp_config_delete_pipeline(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	const char *name
);

int
cp_config_update_devices(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	uint64_t device_count,
	struct cp_device_config *device_configs[]
);

struct cp_config_gen *
cp_config_gen_create(struct cp_config *cp_config);

static inline struct cp_module *
cp_config_gen_get_module(struct cp_config_gen *config_gen, uint64_t index) {
	return cp_module_registry_get(&config_gen->module_registry, index);
}

static inline struct cp_pipeline *
cp_config_gen_get_pipeline(struct cp_config_gen *config_gen, uint64_t index) {
	return cp_pipeline_registry_get(&config_gen->pipeline_registry, index);
}

static inline struct cp_device *
cp_config_gen_get_device(struct cp_config_gen *config_gen, uint64_t index) {
	return cp_device_registry_get(&config_gen->device_registry, index);
}

static inline struct counter_storage *
cp_config_gen_get_pipeline_module_counter_storage(
	struct cp_config_gen *config_gen,
	const char *pipeline_name,
	uint64_t module_type,
	const char *module_name
) {
	return cp_pipeline_module_counter_storage_registry_lookup(
		&config_gen->pipeline_module_counter_storage_registry,
		pipeline_name,
		module_type,
		module_name
	);
}

static inline struct counter_storage *
cp_config_gen_get_pipeline_counter_storage(
	struct cp_config_gen *config_gen, const char *pipeline_name
) {
	return cp_pipeline_counter_storage_registry_lookup(
		&config_gen->pipeline_counter_storage_registry, pipeline_name
	);
}

int
cp_config_gen_lookup_module_index(
	struct cp_config_gen *cp_config_gen,
	uint64_t type,
	const char *name,
	uint64_t *index
);

int
cp_config_gen_lookup_pipeline_index(
	struct cp_config_gen *config_gen, const char *name, uint64_t *index
);

/*
 * Delete module with specified type and name.
 * Method does not free memory of the module.
 * Returns error if module is beeing used by some pipeline.
 */
int
cp_config_delete_module(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	uint64_t module_type,
	const char *module_name
);