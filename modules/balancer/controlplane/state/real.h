#pragma once

#include "api/real.h"
#include "worker.h"

////////////////////////////////////////////////////////////////////////////////

/**
 * Per-worker shards of real info for accumulation.
 */
struct sharded_real_info {
	struct real_info shard[MAX_WORKERS_NUM];
};

/**
 * State-layer representation of a real backend.
 */
struct real_state {
	struct real_identifier
		identifier;	       // Unique key (VS + addr + proto + port)
	struct sharded_real_info info; // Per-worker stats/info
	bool enabled; // Whether traffic is allowed to this real

	size_t registry_idx; // index of the real in registry
};

/**
 * Read current info snapshot for a real into named_real_info.
 */
void
real_get_info(struct real_state *real, struct named_real_info *info);