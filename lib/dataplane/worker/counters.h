#pragma once

#include <stdint.h>

struct counter_registry;
struct dp_config;

enum {
	WORKER_RX_BURST_SIZE = 32,
};

// Registry id of each standard worker counter.
struct worker_counter_ids {
	uint64_t iterations;
	uint64_t rx;
	uint64_t tx;
	uint64_t remote_rx;
	uint64_t remote_tx;
	uint64_t rx_bursts;
	uint64_t local_tx_drops;
	uint64_t remote_tx_drops;
	uint64_t drops;
};

// Registers the standard worker counters in an already initialised registry
// and reports the id of each one.
//
// Returns 0 on success, -1 if any counter fails to register. The ids are
// meaningful only on success.
int
worker_counters_register(
	struct counter_registry *registry, struct worker_counter_ids *ids
);

// Points every worker of an instance at its own counter storage.
//
// Call after the workers are created and their storages are spawned. The
// instance must hold one storage per worker.
void
worker_counters_bind(
	struct dp_config *dp_config, const struct worker_counter_ids *ids
);
