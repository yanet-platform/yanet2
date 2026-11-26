#pragma once

#include "dataplane_ut.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/dataplane/config/plugin_loader.h"

struct dp_worker;
struct config_gen_ectx;

struct dataplane_ut_stage_round {
	uint64_t generation;
	uint64_t iteration;
};

struct dataplane_ut {
	void *arena;
	size_t arena_size;
	// Whether the harness allocated the arena and frees it, rather than
	// its caller owning it.
	bool owns_arena;
	// Shared-memory handle over the arena, handed out to agents.
	//
	// Owned by the harness: it is never detached, because the arena is
	// memory that the harness or its caller frees.
	struct yanet_shm shm;
	struct dp_config *dp_config;
	struct cp_config *cp_config;
	struct agent *agent;
	struct rte_mempool *mempool;
	uint64_t mock_time_ns;
	struct plugin_registry plugins;
	struct dataplane_ut_stage_round *stage_rounds;
};
