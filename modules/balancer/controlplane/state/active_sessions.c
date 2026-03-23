#include "active_sessions.h"
#include "interval_counter.h"

static inline size_t tracker_size(
    size_t  shards
) {
    return sizeof(struct active_sessions_tracker) +
        sizeof(struct active_sessions_tracker_shard) * shards;
}

struct active_sessions_tracker *
active_sessions_tracker_create(
    struct memory_context *mctx,
    size_t shards,
    uint32_t now
) {
    size_t size = tracker_size(shards);
    struct active_sessions_tracker *tracker = memory_balloc(mctx, size);
    if (tracker != NULL) {
        for (size_t shard = 0; shard < shards; ++shard) {
            rt_interval_counter_init(&tracker->shards[shard].counter, now);
            tracker->shards[shard].count = 0;
        }
    }
    return tracker;
}

void
active_sessions_tracker_destroy(
    struct active_sessions_tracker *tracker,
    size_t shards,
    struct memory_context *mctx
) {
    size_t size = tracker_size(shards);
    memory_bfree(mctx, tracker, size);
}