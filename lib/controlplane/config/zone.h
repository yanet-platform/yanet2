#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include <sys/types.h>

#include "common/cache.h"
#include "common/memory.h"

#include "lib/counters/counters.h"

#include "lib/dataplane/config/zone.h"

#include "lib/controlplane/config/config_lock.h"
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

	// Number of holders pinning this generation alive.
	//
	// The currently published generation always counts one holder for
	// itself, so a pin taken by an unlocked reader keeps a racing update
	// from tearing the generation down until the pin is released too.
	// Gaining a reference happens only under the publication lock;
	// dropping one is a relaxed-atomic decrement that the last dropper
	// follows with the generation's own teardown.
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
	 * Publication lock: serialises reading the published generation
	 * together with gaining its first reference, and serialises the
	 * pointer swap of a publish against that. Held for a handful of
	 * instructions only — never across generation builds, worker
	 * round-trips or teardown. A publish takes the configuration lock
	 * first and this one second; readers take this one alone.
	 */
	struct spinlock gen_lock;

	/*
	 * Agent lock: serialises the structure of the agent registry —
	 * the array below and every name's predecessor chain — and the
	 * agent structures it reaches (arenas, storage, memory contexts)
	 * against the sweep's reclamation, an attach's supersession
	 * splice, a destroy's unlinks and an extension's arena growth.
	 * Agent maintenance — attach, extend, destroy, the listing and
	 * the sweep — runs under this lock alone, so it never waits
	 * behind a configuration update. A configuration mutation takes
	 * the configuration lock first and this one second for the
	 * brief crossings it needs: the superseded-owner refusal scan
	 * and the exit sweep. The sweep runs under this lock alone —
	 * a superseded agent's loaded count cannot rise past an
	 * in-flight zero check, because updates through a superseded
	 * agent are refused and a destroy gate's hold takes this lock
	 * with the sweep's check.
	 */
	struct spinlock agent_lock;

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
	offsetof(struct cp_config, gen_lock) -
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
 * Aborts in debug builds when the current thread does not hold this
 * configuration's lock.
 */
void
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

// The currently published generation.
//
// Intended for callers holding the config lock, and for the dataplane's
// assigner whose use of the generation is ordered by the publish
// handshake instead; an unlocked caller that walks the generation must
// pin through the acquire protocol below.
static inline struct cp_config_gen *
cp_config_current_gen(struct cp_config *cp_config) {
	return ATOMIC_ADDR_OF(&cp_config->cp_config_gen);
}

// Make a freshly built generation the published one.
//
// The caller must hold the config lock; the swap itself runs under the
// publication lock so it cannot interleave with a reader naming the old
// generation, and the release store pairs with the acquire loads so the
// generation's contents are fully written before any reader can see it.
static inline void
cp_config_publish_gen(
	struct cp_config *cp_config, struct cp_config_gen *config_gen
) {
	spinlock_lock(&cp_config->gen_lock);
	ATOMIC_SET_OFFSET_OF(&cp_config->cp_config_gen, config_gen);
	spinlock_unlock(&cp_config->gen_lock);
}

// Pin the currently published generation for use without the config lock.
//
// Every acquire MUST be matched by exactly one release, no matter how
// long the unlocked window that follows runs or whether an update
// replaces this generation as current during that window. Naming the
// generation and gaining its reference happen together under the
// publication lock, so a publish cannot retire and free a generation
// a reader is in the middle of pinning.
static inline struct cp_config_gen *
cp_config_gen_acquire(struct cp_config *cp_config) {
	spinlock_lock(&cp_config->gen_lock);
	struct cp_config_gen *config_gen = cp_config_current_gen(cp_config);
	// The bump is itself atomic: pin-dropping releases run without the
	// lock, and a plain increment interleaving with them could lose a
	// decrement.
	__atomic_fetch_add(&config_gen->refcnt, 1, __ATOMIC_RELAXED);
	spinlock_unlock(&cp_config->gen_lock);
	return config_gen;
}

// Release a pin taken by an acquire.
//
// Safe to call without the config lock. The last release — the one that
// drops the count to zero — performs the generation's own teardown:
// registry release and memory return, all of which is independently
// safe (atomic agent counters, internally locked allocator). That
// teardown can leave a superseded agent unreferenced, and the release
// then reclaims it too, waiting for the agent lock if another thread
// holds it: after the call returns, storage owned by such an agent may
// already be gone. A caller that itself holds the configuration lock —
// an install, whose update path sweeps before releasing it — skips that
// sweep and leaves the reclamation to its caller.
void
cp_config_gen_release(
	struct cp_config *cp_config, struct cp_config_gen *config_gen
);

// Number of pins currently held on the generation, counting the
// publication pin of a generation that is still current.
//
// An observation snapshot for tests and diagnostics; nothing may branch
// on it, because a pin may appear or drain at any moment. Valid only
// while holding a pin on the generation or the config lock.
static inline uint64_t
cp_config_gen_pin_count(
	const struct cp_config *cp_config,
	const struct cp_config_gen *config_gen
) {
	(void)cp_config;
	return __atomic_load_n(&config_gen->refcnt, __ATOMIC_RELAXED);
}

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
	if (config_gen == NULL) {
		return NULL;
	}
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
cp_config_gen_lookup_function_index(
	struct cp_config_gen *config_gen, const char *name, uint64_t *index
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
