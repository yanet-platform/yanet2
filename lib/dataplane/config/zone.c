#include "zone.h"

#include <sched.h>
#include <time.h>
#include <unistd.h>

#include "common/container_of.h"
#include "lib/controlplane/config/cp_device.h"
#include "lib/controlplane/config/cp_module.h"
#include "lib/controlplane/config/registry.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/pipeline/pipeline.h"
#include "lib/logging/log.h"

struct dp_config *
dp_config_nextk(struct dp_config *current, uint32_t k) {
	for (uint32_t i = 0; i < k; ++i) {
		current = (struct dp_config *)((uintptr_t)current +
					       current->storage_size);
	}
	return current;
}

// Number of sched_yield iterations tried before falling back to nanosleep.
#define DP_CONFIG_GEN_ACK_YIELD_ITERS 1024

// Fixed nanosleep interval used once the yield phase is exhausted.
#define DP_CONFIG_GEN_ACK_SLEEP_NS UINT64_C(1000000)

// Observes both the worker's context assignment and its acknowledged
// generation.
//
// The waiter needs the pair, not either alone: the assignment proves the
// worker switched away from the superseded context, the acknowledgement
// orders the round that last used it before this observation.
static bool
dp_worker_ectx_and_gen_observed(
	struct dp_worker *worker, struct config_gen_ectx *expected, uint64_t gen
) {
	return ATOMIC_ADDR_OF(&worker->config_gen_ectx) == expected &&
	       __atomic_load_n(&worker->gen, __ATOMIC_ACQUIRE) >= gen;
}

// Waits for a single worker to take the expected context and acknowledge
// gen.
//
// Backs off from sched_yield to a fixed-interval nanosleep, without ever
// giving up.
static void
dp_config_wait_for_worker_ectx(
	struct dp_worker *worker, struct config_gen_ectx *expected, uint64_t gen
) {
	for (unsigned iters = 0; iters < DP_CONFIG_GEN_ACK_YIELD_ITERS;
	     ++iters) {
		if (dp_worker_ectx_and_gen_observed(worker, expected, gen)) {
			return;
		}
		sched_yield();
	}

	static const struct timespec sleep_ts = {
		.tv_sec = (time_t)(DP_CONFIG_GEN_ACK_SLEEP_NS / 1000000000ULL),
		.tv_nsec = (long)(DP_CONFIG_GEN_ACK_SLEEP_NS % 1000000000ULL),
	};
	for (;;) {
		if (dp_worker_ectx_and_gen_observed(worker, expected, gen)) {
			return;
		}
		nanosleep(&sleep_ts, NULL);
	}
}

// Serializes commit passes within one dataplane process.
//
// Every assigner thread of the process contends here before reading the
// commit bookkeeping, so exactly one thread runs the handlers for a
// shared config and generation while the others observe the recorded
// outcome and skip — the single-writer guarantee the handlers rely on.
static pthread_mutex_t commit_lock = PTHREAD_MUTEX_INITIALIZER;

// Runs the module commit handlers of a generation.
//
// Modules are visited in registry order; an item whose dataplane
// slot index falls outside the loaded array is skipped with an error
// logged. That path is unreachable in practice — the indices are
// validated when the config is built — so the skip is purely
// defensive.
static void
dp_config_commit_gen_modules(
	struct dp_config *dp_config, struct cp_config_gen *config_gen
) {
	struct cp_module_registry *module_registry =
		&config_gen->module_registry;
	struct dp_module *dp_modules = ADDR_OF(&dp_config->dp_modules);

	for (uint64_t idx = 0; idx < module_registry->registry.capacity;
	     ++idx) {
		struct registry_item *item =
			registry_get(&module_registry->registry, idx);
		if (item == NULL) {
			continue;
		}
		struct cp_module *cp_module =
			container_of(item, struct cp_module, config_item);
		if (cp_module->dp_module_idx >= dp_config->module_count) {
			LOG(ERROR,
			    "commit skipped for module '%s:%s': no dataplane "
			    "module",
			    cp_module->type,
			    cp_module->name);
			continue;
		}
		struct dp_module *dp_module =
			dp_modules + cp_module->dp_module_idx;
		if (dp_module->commit_handler != NULL) {
			dp_module->commit_handler(dp_config, cp_module);
		}
	}
}

// Runs the device commit handlers of a generation, after every module
// handler; devices are visited in registry order, with the same
// defensive skip of an out-of-range slot index as the module pass.
static void
dp_config_commit_gen_devices(
	struct dp_config *dp_config, struct cp_config_gen *config_gen
) {
	struct cp_device_registry *device_registry =
		&config_gen->device_registry;
	struct dp_device *dp_devices = ADDR_OF(&dp_config->dp_devices);

	for (uint64_t idx = 0; idx < device_registry->registry.capacity;
	     ++idx) {
		struct registry_item *item =
			registry_get(&device_registry->registry, idx);
		if (item == NULL) {
			continue;
		}
		struct cp_device *cp_device =
			container_of(item, struct cp_device, config_item);
		if (cp_device->dp_device_idx >= dp_config->device_count) {
			LOG(ERROR,
			    "commit skipped for device '%s:%s': no dataplane "
			    "device",
			    cp_device->type,
			    cp_device->name);
			continue;
		}
		struct dp_device *dp_device =
			dp_devices + cp_device->dp_device_idx;
		if (dp_device->commit_handler != NULL) {
			dp_device->commit_handler(dp_config, cp_device);
		}
	}
}

void
dp_config_commit_gen(
	struct dp_config *dp_config, struct cp_config_gen *config_gen
) {
	if (config_gen == NULL) {
		return;
	}

	// Generation sequence, encoded so zero still means "none".
	const uint64_t gen_seq = config_gen->gen + 1;

	pthread_mutex_lock(&commit_lock);
	if (dp_config->committed_gen_seq == gen_seq) {
		pthread_mutex_unlock(&commit_lock);
		return;
	}

	dp_config_commit_gen_modules(dp_config, config_gen);
	dp_config_commit_gen_devices(dp_config, config_gen);

	// Plain store: the record is touched only under this lock, by
	// this process's assigner threads or harness round driver; no
	// reader in another process exists.
	dp_config->committed_gen_seq = gen_seq;
	pthread_mutex_unlock(&commit_lock);
}

void
dp_config_assign_worker_ectxs(
	struct dp_config *dp_config, struct cp_config_gen *config_gen
) {
	dp_config_commit_gen(dp_config, config_gen);

	// The loop bound and the array it indexes come from one observation.
	struct dp_worker *const *workers = ADDR_OF(&dp_config->workers);
	const uint64_t worker_count = dp_config->worker_count;

	for (uint64_t idx = 0; idx < worker_count; ++idx) {
		struct dp_worker *worker = ADDR_OF(workers + idx);
		struct config_gen_ectx *expected =
			cp_config_gen_worker_ectx(config_gen, idx);
		// Derive the absolute stage addresses before the release
		// store.
		//
		// The store pairs with the acquire load at the worker's
		// round start, so the derived addresses are complete before
		// any stage runs. The derivation is idempotent, so a worker
		// already holding this context is unaffected.
		if (expected != NULL) {
			config_gen_ectx_resolve_counters(expected);
		}
		// Skip workers already holding the expected context: the
		// release store exists for the switch, and re-issuing it on
		// every assignment pass would make the field flap for
		// readers that are content.
		if (ADDR_OF(&worker->config_gen_ectx) != expected) {
			ATOMIC_SET_OFFSET_OF(
				&worker->config_gen_ectx, expected
			);
		}
	}
}

void
dp_config_wait_for_gen(
	struct dp_config *dp_config, struct cp_config_gen *config_gen
) {
	// The harness round driver holds external_round_lock across every
	// dereference of a worker context, and rounds are the only users
	// of the contexts in this mode.
	//
	// Waiting for the lock therefore spans any in-flight round: once
	// it is held, no round can still be dereferencing the retired
	// contexts, so delivering the new ones in place completes the
	// switch with no further acknowledgement to wait for.
	if (dp_config->external_worker_rounds) {
		pthread_mutex_t *round_lock =
			dp_config_external_round_lock(dp_config);
		pthread_mutex_lock(round_lock);
		dp_config_assign_worker_ectxs(dp_config, config_gen);
		pthread_mutex_unlock(round_lock);
		return;
	}

	// The loop bound and the array it indexes come from one observation.
	struct dp_worker *const *workers = ADDR_OF(&dp_config->workers);
	const uint64_t worker_count = dp_config->worker_count;

	for (uint64_t idx = 0; idx < worker_count; ++idx) {
		struct dp_worker *worker = ADDR_OF(workers + idx);
		struct config_gen_ectx *expected =
			cp_config_gen_worker_ectx(config_gen, idx);
		dp_config_wait_for_worker_ectx(
			worker, expected, config_gen->gen
		);
	}
}

bool
dp_config_try_lock(struct dp_config *dp_config) {
	pid_t pid = getpid();
	pid_t zero = 0;
	return __atomic_compare_exchange_n(
		&dp_config->config_lock,
		&zero,
		pid,
		false,
		__ATOMIC_ACQUIRE,
		__ATOMIC_RELAXED
	);
}

void
dp_config_lock(struct dp_config *dp_config) {
	pid_t pid = getpid();
	pid_t zero = 0;
	while (!__atomic_compare_exchange_n(
		&dp_config->config_lock,
		&zero,
		pid,
		false,
		__ATOMIC_ACQUIRE,
		__ATOMIC_RELAXED
	)) {
		zero = 0;
	};
}

bool
dp_config_unlock(struct dp_config *dp_config) {
	pid_t pid = getpid();
	pid_t zero = 0;
	return __atomic_compare_exchange_n(
		&dp_config->config_lock,
		&pid,
		zero,
		false,
		__ATOMIC_RELEASE,
		__ATOMIC_RELAXED
	);
}

int
dp_config_lookup_module(
	struct dp_config *dp_config, const char *name, uint64_t *index
) {
	struct dp_module *modules = ADDR_OF(&dp_config->dp_modules);
	for (uint64_t idx = 0; idx < dp_config->module_count; ++idx) {
		if (!strncmp(
			    modules[idx].name, name, sizeof(modules[idx].name)
		    )) {
			*index = idx;
			return 0;
		}
	}
	return -1;
}

int
dp_config_lookup_device(
	struct dp_config *dp_config, const char *name, uint64_t *index
) {
	struct dp_device *devices = ADDR_OF(&dp_config->dp_devices);
	for (uint64_t idx = 0; idx < dp_config->device_count; ++idx) {
		if (!strncmp(
			    devices[idx].name, name, sizeof(devices[idx].name)
		    )) {
			*index = idx;
			return 0;
		}
	}
	return -1;
}

int
dp_config_lookup_object(
	struct dp_config *dp_config, const char *name, uint64_t *index
) {
	struct dp_object *objects = ADDR_OF(&dp_config->dp_objects);
	for (uint64_t idx = 0; idx < dp_config->object_count; ++idx) {
		if (!strncmp(
			    objects[idx].name, name, sizeof(objects[idx].name)
		    )) {
			*index = idx;
			return 0;
		}
	}
	return -1;
}

uint64_t
dp_config_device_worker_count(struct dp_config *dp_config, uint32_t device_id) {
	if (device_id >= dp_config->dp_topology.device_count) {
		return 0;
	}
	struct dp_port *devices = ADDR_OF(&dp_config->dp_topology.devices);
	return devices[device_id].worker_count;
}
