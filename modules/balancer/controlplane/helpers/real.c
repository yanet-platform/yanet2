#include "common/memory_address.h"

#include "modules/balancer/dataplane/types/interval_counter.h"
#include "modules/balancer/dataplane/types/real.h"
#include "modules/balancer/dataplane/types/sessions_tracker.h"

#include "real.h"

static uint32_t
calc_active_sessions(
	const struct balancer_sessions_tracker_shard *shard,
	uint32_t now,
	uint32_t *last_packet_timestamp
) {
	const struct balancer_interval_counter *counter = &shard->counter;

	const uint32_t initial_last_timestamp = shard->last_timestamp;
	const uint32_t last_dp_tick = counter->last_timestamp;
	uint32_t current_cp_tick = now / BALANCER_SESSIONS_TRACKER_PRECISION;

	uint32_t next_tick = counter->last_timestamp + 1;

	int32_t add = 0;
	while (next_tick <= current_cp_tick &&
	       next_tick - last_dp_tick < BALANCER_IC_RING_SIZE) {
		add += counter->diff[next_tick & BALANCER_IC_RING_MASK];
		++next_tick;
	}

	// Prevent reuse of the last_timestamp value
	// and force reload from memory.
	__asm__ volatile("" ::: "memory");

	uint32_t count = shard->count;
	uint32_t last_timestamp = shard->last_timestamp;
	if (last_timestamp != initial_last_timestamp) {
		add = 0;
	}

	*last_packet_timestamp = last_timestamp;

	return ((int64_t)count + add >= 0 ? count + add : 0);
}

void
balancer_real_sessions(
	struct balancer_real *real,
	size_t workers,
	uint64_t *active_sessions,
	uint32_t *last_packet_timestamp,
	uint32_t now
) {
	struct balancer_sessions_tracker_shard *tracker_shards =
		ADDR_OF(&real->tracker_shards);
	*active_sessions = 0;
	*last_packet_timestamp = 0;
	for (size_t worker_idx = 0; worker_idx < workers; ++worker_idx) {
		const struct balancer_sessions_tracker_shard *shard =
			&tracker_shards[worker_idx];
		uint32_t last_timestamp;
		*active_sessions +=
			calc_active_sessions(shard, now, &last_timestamp);
		if (*last_packet_timestamp < last_timestamp) {
			*last_packet_timestamp = last_timestamp;
		}
	}
}