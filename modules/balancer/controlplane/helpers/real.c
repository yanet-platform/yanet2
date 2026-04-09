#include "common/memory_address.h"

#include "modules/balancer/dataplane/types/real.h"
#include "modules/balancer/dataplane/types/sessions_tracker.h"

#include "real.h"

void
balancer_real_sessions(
	struct balancer_real *real,
	size_t workers,
	uint64_t *active_sessions,
	uint32_t *last_packet_timestamp
) {
	struct balancer_sessions_tracker_shard *tracker_shards =
		ADDR_OF(&real->tracker_shards);
	*active_sessions = 0;
	*last_packet_timestamp = 0;
	for (size_t worker_idx = 0; worker_idx < workers; ++worker_idx) {
		struct balancer_sessions_tracker_shard *shard =
			&tracker_shards[worker_idx];
		*active_sessions += shard->count;
		if (*last_packet_timestamp < shard->counter.last_timestamp) {
			*last_packet_timestamp = shard->counter.last_timestamp;
		}
	}
}