#pragma once

#include <stdint.h>

struct counter_storage;
struct dp_config;
struct dp_worker;

enum {
	WORKER_RX_BURST_SIZE = 32,
};

// Registry-assigned identifiers of the standard worker counters, one named
// slot per counter.
//
// Captured at registration time so the dataplane addresses counter storage
// by identifier instead of by registration order; inserting, removing, or
// reordering a registration can no longer silently mislabel a slot.
struct worker_counters {
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

// Register standard worker counters in dp_config->worker_counters and
// capture their assigned identifiers into counters. Caller must initialise
// the registry via counter_registry_init before calling.
//
// Returns 0 on success, -1 if any counter fails to register.
int
worker_counters_register(
	struct dp_config *dp_config, struct worker_counters *counters
);

// Resolve the per-worker counter address pointers against the given
// storage, using the identifiers captured at registration time.
void
worker_counters_bind(
	struct dp_worker *dp_worker, struct counter_storage *storage
);
