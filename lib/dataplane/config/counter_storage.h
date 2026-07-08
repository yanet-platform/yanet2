#pragma once

#include <stdint.h>

struct cp_config;
struct dp_config;

// Link the worker counter registry and spawn its per-worker storages.
//
// Links the worker counter registry and spawns one single-instance backing
// storage per worker into dp_config->worker_counter_storages. Caller must have
// registered the worker counters into dp_config->worker_counters before
// invoking.
//
// Returns 0 on success, -1 if the registry link fails or storage
// allocation cannot complete.
int
dp_counter_storage_init(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	uint64_t worker_count
);
