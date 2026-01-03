#pragma once

#include "api/vs.h"
#include "worker.h"

struct sharded_vs_info {
	struct vs_info shard[MAX_WORKERS_NUM];
};

struct vs_state {
	struct vs_identifier identifier;
	struct sharded_vs_info info;
	size_t registry_idx; // index of the virtual service in the registry
};

void
vs_get_info(struct vs_state *vs, struct named_vs_info *info);
