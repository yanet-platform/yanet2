#pragma once

#include <stdbool.h>
#include <stdint.h>
#include <sys/types.h>

#include "dataplane/device/device.h"
#include "dataplane/module/module.h"

#include "dataplane/time/clock.h"

#include "dataplane/config/topology.h"

#include "counters/counters.h"

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

// Per-DPDK-port counter registry and storage.
//
// Each physical port owns a distinct registry (xstat schema) and storage
// (values), so a port's counter lifecycle is independent of the others. All
// port storages are spawned from the single shared allocator on dp_config.
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
	// Currently, we init it only once
	// and dont adjust.
	// So, we have some drift, which is small but...
	// (see tsc_clock docs). It is not important
	// for now and fix should be easy, but need discuss.
	//
	// TODO: FIXME
	struct tsc_clock clock;

	// Current worker time in nanoseconds,
	// initialized on the start of the current
	// loop round.
	uint64_t current_time;

	uint64_t *iterations;

	uint64_t *rx_count;
	uint64_t *rx_size;

	uint64_t *tx_count;
	uint64_t *tx_size;

	uint64_t *remote_rx_count;
	uint64_t *remote_tx_count;

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

	uint64_t storage_size;

	struct block_allocator block_allocator;
	struct memory_context memory_context;

	pid_t config_lock;

	struct dp_topology dp_topology;

	uint64_t module_count;
	struct dp_module *dp_modules;

	uint64_t device_count;
	struct dp_device *dp_devices;

	struct cp_config *cp_config;

	uint64_t worker_count;
	struct dp_worker **workers;

	struct counter_storage_allocator counter_storage_allocator;
	struct counter_registry worker_counters;
	struct counter_storage *worker_counter_storage;

	struct counter_storage_allocator port_counter_storage_allocator;
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

void
dp_config_wait_for_gen(struct dp_config *dp_config, uint64_t gen);
