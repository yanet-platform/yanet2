#pragma once

#include <pthread.h>
#include <stdbool.h>
#include <stdint.h>
#include <sys/types.h>

#include "lib/dataplane/device/device.h"
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/object/object.h"

#include "lib/dataplane/time/clock.h"

#include "lib/dataplane/config/topology.h"

#include "lib/counters/counters.h"

struct cp_config;
struct cp_config_gen;
struct rte_mempool;

struct dp_module {
	char name[80];
	module_handler handler;
};

struct dp_device {
	char name[DEVICE_TYPE_LEN];
	device_handler input_handler;
	device_handler output_handler;
};

/*
 * Inert dataplane-side descriptor for a loaded object type.
 *
 * Carries only the type name: objects take no per-packet action, so there
 * is no handler slot. A handler field can be added here when objects gain
 * dataplane behaviour.
 */
struct dp_object {
	char name[OBJECT_TYPE_LEN];
};

// Per-DPDK-port counter registry and storage.
//
// Each physical port owns a distinct registry (xstat schema) and storage
// (values), so a port's counter lifecycle is independent of the others. All
// port storages are spawned directly from the dp_config memory context.
struct dp_port_counters {
	uint16_t port_id;
	char port_name[80];
	struct counter_registry registry;
	struct counter_storage *storage;
};

struct dp_worker {
	uint64_t idx;

	uint64_t gen;

	// Allows to get current worker time.
	//
	// Currently, we init it only once and never re-adjust it. The TSC
	// crystal drifts against real time by a small ppm-scale rate (see
	// tsc_clock docs), so the longer the worker runs without a fresh
	// tsc_clock_adjust call, the further current_time strays from real
	// time. It is not important for now and fix should be easy, but
	// need discuss.
	//
	// TODO: FIXME
	struct tsc_clock clock;

	// Current worker time in nanoseconds,
	// initialized on the start of the current
	// loop round.
	//
	// Also read by processes attached to this zone.
	uint64_t current_time;

	uint64_t *iterations;

	uint64_t *rx_count;
	uint64_t *rx_size;

	uint64_t *tx_count;
	uint64_t *tx_size;

	uint64_t *remote_rx_count;
	uint64_t *remote_tx_count;

	// Packets dropped because the local NIC TX burst could not accept them.
	uint64_t *local_tx_drops;
	// Packets dropped because the inter-worker data pipe was full or
	// absent.
	uint64_t *remote_tx_drops;
	// Total packets dropped by this worker for any reason.
	uint64_t *drop_count;

	struct rte_mempool *rx_mempool;

	uint64_t *rx_bursts;
	uint32_t core_id;
	uint32_t device_id;
	uint32_t queue_id;
	uint32_t rx_burst_size;

	// Offset pointer to the execution context this worker runs,
	// taken from the generation most recently assigned to it.
	//
	// Written by the instance's config assigner with release ordering
	// whenever a new generation is published; read by the worker at
	// the start of every round with acquire ordering. NULL until the
	// first generation carrying execution contexts is assigned, which
	// keeps the worker on the pre-configuration drop path.
	struct config_gen_ectx *config_gen_ectx;
};

// Value written to dp_config.ready_magic by dp_config_mark_ready once the
// instance is fully initialised and cp_config has been released.
//
// The writer uses release ordering; readers use acquire ordering before
// attempting to attach, so observing this value guarantees the instance is
// ready and its cp_config lock is free.
#define DP_CONFIG_READY_MAGIC UINT64_C(0xDEAD10CC00000001)

struct dp_config {
	uint32_t instance_count;
	uint32_t instance_idx;

	/*
	 * Use it to attach workers
	 */
	uint32_t numa_idx;
	uint16_t packet_recirc_limit;

	uint64_t storage_size;

	struct block_allocator block_allocator;
	struct memory_context memory_context;

	pid_t config_lock;

	struct dp_topology dp_topology;

	uint64_t module_count;
	struct dp_module *dp_modules;

	uint64_t device_count;
	struct dp_device *dp_devices;

	uint64_t object_count;
	struct dp_object *dp_objects;

	struct cp_config *cp_config;

	// Both fields are filled in by dataplane_worker_init during dataplane
	// startup and stay unchanged afterwards.
	uint64_t worker_count;
	struct dp_worker **workers;

	struct counter_registry worker_counters;

	// Per-worker worker-counter storages.
	//
	// worker_counter_storages points to an array of
	// worker_counter_storage_count offset pointers, one single-instance
	// storage per worker.
	uint64_t worker_counter_storage_count;
	struct counter_storage **worker_counter_storages;

	uint64_t port_count;
	struct dp_port_counters *port_counters;

	// Selects the synchronous generation hand-off used by in-process
	// harnesses; production leaves it clear.
	//
	// Set once before the instance is marked ready, when no worker
	// threads exist and rounds run on caller threads: the generation
	// waiter then delivers contexts to the workers itself,
	// synchronously, under the round lock below.
	bool external_worker_rounds;

	// Storage of the harness round lock.
	//
	// pthread_mutex_t sizing differs between architectures, so the
	// lock lives in fixed-size storage to keep the shared-memory
	// layout architecture-independent. Only a harness that set
	// external_worker_rounds initializes and locks it.
	uint64_t external_round_lock_storage[6];

	// Written by dp_config_mark_ready with release ordering after the
	// dataplane releases cp_config and finishes initialising the instance.
	//
	// Readers check this with acquire ordering before attaching. Observing
	// the magic guarantees the instance is fully initialised and that
	// cp_config is not held, so an attacher will not block on the lock.
	uint64_t ready_magic;
};

// The harness round lock, reached through its fixed-size storage.
static inline pthread_mutex_t *
dp_config_external_round_lock(struct dp_config *dp_config) {
	return (pthread_mutex_t *)dp_config->external_round_lock_storage;
}

_Static_assert(
	sizeof(((struct dp_config *)NULL)->external_round_lock_storage) >=
		sizeof(pthread_mutex_t),
	"external_round_lock_storage no longer fits pthread_mutex_t"
);

/*
 * Returns dp_config of k-th instance from current.
 */
struct dp_config *
dp_config_nextk(struct dp_config *current, uint32_t k);

bool
dp_config_try_lock(struct dp_config *dp_config);

void
dp_config_lock(struct dp_config *dp_config);

bool
dp_config_unlock(struct dp_config *dp_config);

int
dp_config_lookup_module(
	struct dp_config *dp_config, const char *name, uint64_t *index
);

int
dp_config_lookup_device(
	struct dp_config *dp_config, const char *name, uint64_t *index
);

int
dp_config_lookup_object(
	struct dp_config *dp_config, const char *name, uint64_t *index
);

// Count of workers bound to device_id, across every instance.
//
// O(1), reading a count dp_topology_set_device_worker_count publishes per
// device. Zero for an out-of-range device_id or one whose count was never
// set.
uint64_t
dp_config_device_worker_count(struct dp_config *dp_config, uint32_t device_id);

// Deliver each worker of dp_config the execution context built for it in
// config_gen.
//
// A context of NULL is valid and returns the worker to the pre-configuration
// drop path; it is what generations without per-worker contexts assign.
void
dp_config_assign_worker_ectxs(
	struct dp_config *dp_config, struct cp_config_gen *config_gen
);

// Waits for every worker of dp_config to hold the execution context of
// config_gen and to have acknowledged that generation.
//
// This is the grace period the publisher needs before it may reclaim the
// generation config_gen superseded. Blocks with no timeout, by design: an
// early return would let an owner reclaim memory an unacknowledged worker
// may still dereference.
void
dp_config_wait_for_gen(
	struct dp_config *dp_config, struct cp_config_gen *config_gen
);
