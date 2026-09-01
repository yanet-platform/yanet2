#pragma once

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

	// Written by dp_config_mark_ready with release ordering after the
	// dataplane releases cp_config and finishes initialising the instance.
	//
	// Readers check this with acquire ordering before attaching. Observing
	// the magic guarantees the instance is fully initialised and that
	// cp_config is not held, so an attacher will not block on the lock.
	uint64_t ready_magic;
};

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

// Waits for every worker of dp_config to acknowledge generation gen.
//
// This is the grace period cp_config_gen_install needs before it may
// reclaim the generation gen superseded. Blocks with no timeout, by
// design: an early return would let an owner reclaim memory an
// unacknowledged worker may still dereference.
void
dp_config_wait_for_gen(struct dp_config *dp_config, uint64_t gen);
