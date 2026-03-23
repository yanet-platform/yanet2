#include "active_sessions.h"
#include "interval_counter.h"
#include "modules/balancer/dataplane/active_sessions.h"

static inline size_t
tracker_size(size_t shards) {
	return sizeof(struct active_sessions_tracker_shard) * shards;
}

struct active_sessions_tracker_shard *
active_sessions_tracker_create(
	struct memory_context *mctx, size_t shards, uint32_t now
) {
	size_t size = tracker_size(shards);
	struct active_sessions_tracker_shard *tracker_shards =
		memory_balloc(mctx, size);
	if (tracker_shards != NULL) {
		for (size_t shard = 0; shard < shards; ++shard) {
			rt_interval_counter_init(
				&tracker_shards[shard].counter, now
			);
			tracker_shards[shard].count = 0;
		}
	}
	return tracker_shards;
}

void
active_sessions_tracker_destroy(
	struct active_sessions_tracker_shard *tracker_shards,
	size_t shards,
	struct memory_context *mctx
) {
	size_t size = tracker_size(shards);
	memory_bfree(mctx, tracker_shards, size);
}