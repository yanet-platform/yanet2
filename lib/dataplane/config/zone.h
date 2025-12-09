#pragma once

#include <stdalign.h>
#include <stdbool.h>
#include <stdint.h>
#include <time.h>
#include <unistd.h>

#include "common/memory.h"

#include "dataplane/module/module.h"

#include "dataplane/time/clock.h"

#include "dataplane/config/topology.h"

#include "counters/counters.h"

#include "controlplane/agent/agent.h"

struct cp_config;

struct dp_module {
	char name[80];
	module_handler handler;
};

struct dp_device {
	char name[DEVICE_NAME_LEN];
	device_handler input_handler;
	device_handler output_handler;
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

	uint8_t pad[24];
};
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
