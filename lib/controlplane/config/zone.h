#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include <sys/types.h>

#include "common/cache.h"
#include "common/memory.h"

#include "lib/counters/counters.h"

#include "lib/dataplane/config/zone.h"

#include "lib/controlplane/config/cp_chain.h"
#include "lib/controlplane/config/cp_device.h"
#include "lib/controlplane/config/cp_function.h"
#include "lib/controlplane/config/cp_module.h"
#include "lib/controlplane/config/cp_object.h"
#include "lib/controlplane/config/cp_pipeline.h"

#include "lib/controlplane/config/cp_counter.h"

#include "lib/errors/errors.h"

struct dp_config;
struct cp_config;
struct cp_config_gen;
struct config_gen_ectx;

/*
 * Configuration generation denotes a snapshot of controlplane
 * packet processing configuration. It contains module and pipeline
 * registries also with pipeline to device binding.
 *
 * On each update a new copy of the current active configuration generation
 * is instantiated and modified. After all updates are done the new generation
 * replaces an old one. However the previous could be still in use by
 * dataplane workers so the updater should wait until new generation reaches
 * all workers before resource freeing.
 */
struct cp_config_gen {
	uint64_t gen;

	struct cp_config *cp_config;
	struct dp_config *dp_config;

	// Per-worker execution contexts.
	//
	// config_gen_ectxs points to an array of config_gen_ectx_count offset
	// pointers, one execution context per worker, each with its own
	// single-instance counter storages.
	uint64_t config_gen_ectx_count;
	struct config_gen_ectx **config_gen_ectxs;

	struct cp_module_registry module_registry;
	struct cp_function_registry function_registry;
	struct cp_pipeline_registry pipeline_registry;
	struct cp_device_registry device_registry;
	struct cp_object_registry object_registry;

	// Number of holders pinning this generation alive, read and mutated
	// only while the config lock is held.
	//
	// The currently published generation always counts one holder for
	// itself, so a pin taken by an unlocked reader keeps a racing update
	// from tearing the generation down until the pin is released too.
	uint64_t refcnt;
};

/*
 * The structure contains agents attached to controplane configuration
 * zone.
 */
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
	 * Controlplane memory context used for econtext placement.
	 */
	struct memory_context ectx_memory_context;
	/*
	 * Controlplane memory context used for counter storage placement.
	 */
	struct memory_context counter_storage_memory_context;
	/*
	 * Relative porinter to the corresponding dataplane memory zone
	 * structure.
	 */
	struct dp_config *dp_config;
	/*
	 * Identifier of a process changinf the controplane configuration.
	 */
	pid_t config_lock;

	// Keep lock ownership writes away from the published generation.
	//
	// Every dataplane worker reads the generation once per round, so lock
	// contention must not invalidate the same cache line.
	uint8_t _config_lock_pad[YANET_CACHE_LINE_SIZE - sizeof(pid_t)];

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
};

_Static_assert(
	offsetof(struct cp_config, cp_config_gen) -
			offsetof(struct cp_config, config_lock) >=
		YANET_CACHE_LINE_SIZE,
	"config lock and generation publication must use separate cache lines"
);

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
	struct cp_module **cp_modules,
	yanet_error **err
);

int
cp_config_update_functions(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	uint64_t function_count,
	struct cp_function_config **functions,
	yanet_error **err
);

int
cp_config_delete_function(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	const char *name,
	yanet_error **err
);

int
cp_config_update_pipelines(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	uint64_t pipeline_count,
	struct cp_pipeline_config **pipelines,
	yanet_error **err
);

int
cp_config_delete_pipeline(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	const char *name,
	yanet_error **err
);

int
cp_config_update_devices(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	uint64_t device_count,
	struct cp_device *devices[],
	yanet_error **err
);

// Delete device with specified name.
//
// Returns -1 if the device does not exist or is a predefined topology
// device (created automatically for each dataplane port), 0 on success.
int
cp_config_delete_device(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	const char *name,
	yanet_error **err
);

int
cp_config_update_objects(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	uint64_t object_count,
	struct cp_object *objects[],
	yanet_error **err
);

int
cp_config_delete_object(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	const char *object_type,
	const char *object_name,
	yanet_error **err
);

struct cp_config_gen *
cp_config_gen_new(struct agent *agent, yanet_error **err);

// Pin the currently published generation for use outside the config lock.
//
// MUST be called with the config lock held. Every acquire MUST be matched
// by exactly one release, no matter how long the unlocked window that
// follows runs or whether a config update replaces this generation as
// current during that window.
static inline struct cp_config_gen *
cp_config_gen_acquire(struct cp_config *cp_config) {
	struct cp_config_gen *config_gen = ADDR_OF(&cp_config->cp_config_gen);
	config_gen->refcnt += 1;
	return config_gen;
}

// Release a pin, freeing the generation once no pin remains.
//
// MUST be called with the config lock held. A generation that lost its
// current-published status while pinned is freed here instead of at
// publish time, once its last pin drops.
void
cp_config_gen_release(
	struct cp_config *cp_config, struct cp_config_gen *config_gen
);

static inline struct cp_module *
cp_config_gen_get_module(struct cp_config_gen *config_gen, uint64_t index) {
	return cp_module_registry_get(&config_gen->module_registry, index);
}

static inline struct cp_function *
cp_config_gen_get_function(struct cp_config_gen *config_gen, uint64_t index) {
	return cp_function_registry_get(&config_gen->function_registry, index);
}

static inline struct cp_pipeline *
cp_config_gen_get_pipeline(struct cp_config_gen *config_gen, uint64_t index) {
	return cp_pipeline_registry_get(&config_gen->pipeline_registry, index);
}

static inline struct cp_device *
cp_config_gen_get_device(struct cp_config_gen *config_gen, uint64_t index) {
	return cp_device_registry_get(&config_gen->device_registry, index);
}

static inline struct cp_object *
cp_config_gen_get_object(struct cp_config_gen *config_gen, uint64_t index) {
	return cp_object_registry_get(&config_gen->object_registry, index);
}

// Return the execution context built for the given worker, or NULL when the
// worker index is out of range or no execution context has been installed yet.
static inline struct config_gen_ectx *
cp_config_gen_worker_ectx(
	struct cp_config_gen *config_gen, uint64_t worker_idx
) {
	if (worker_idx >= config_gen->config_gen_ectx_count) {
		return NULL;
	}
	struct config_gen_ectx **ectxs = ADDR_OF(&config_gen->config_gen_ectxs);
	if (ectxs == NULL) {
		return NULL;
	}
	return ADDR_OF(ectxs + worker_idx);
}

struct cp_module *
cp_config_gen_lookup_module(
	struct cp_config_gen *config_gen, const char *type, const char *name
);

struct cp_function *
cp_config_gen_lookup_function(
	struct cp_config_gen *config_gen, const char *name
);

struct cp_pipeline *
cp_config_gen_lookup_pipeline(
	struct cp_config_gen *config_gen, const char *name
);

int
cp_config_gen_lookup_pipeline_index(
	struct cp_config_gen *config_gen, const char *name, uint64_t *index
);

struct cp_object *
cp_config_gen_lookup_object(
	struct cp_config_gen *config_gen,
	const char *object_type,
	const char *object_name
);

int
cp_config_gen_lookup_object_index(
	struct cp_config_gen *config_gen,
	const char *object_type,
	const char *object_name,
	uint64_t *index
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
	const char *module_type,
	const char *module_name,
	yanet_error **err
);
