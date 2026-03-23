#include "modules/balancer/dataplane/active_sessions.h"

#include "common/memory.h"
#include <string.h>

struct active_sessions_tracker_shard *
active_sessions_tracker_create(
	struct memory_context *mctx, size_t shards, uint32_t now
);

void
active_sessions_tracker_destroy(
	struct active_sessions_tracker_shard *tracker_shards,
	size_t shards,
	struct memory_context *mctx
);
