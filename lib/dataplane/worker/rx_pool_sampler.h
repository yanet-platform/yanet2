#pragma once

#include <stdint.h>

#include <rte_mempool.h>

// Lower bound on the resample spacing of one worker pool: two
// consecutive resamples sit at least this far apart.
#define WORKER_RX_POOL_SAMPLE_INTERVAL_NS UINT64_C(10000000)

// Gauge state for one worker RX mempool, owned by the worker itself.
//
// A worker round resamples the occupancy at most once per interval, on
// finding the deadline expired; a slow round delays a resample past the
// interval, so the interval bounds the rate rather than fixing the
// spacing. A scrape never touches DPDK state. The state lives outside
// the plugin-visible worker layout: only the published gauge crosses
// into shared memory.
struct worker_rx_pool_sampler {
	uint64_t *available_slot;
	uint64_t next_sample_ns;
};

// Binds the sampler to its counter slots and records the initial values.
//
// Returns 0, or -1 when the pool or either slot is missing, so the
// caller can fail startup instead of publishing a zero-valued pool. The
// capacity slot is only needed here: it is written once and never
// resampled, so it is not kept in the sampler state.
static inline int
worker_rx_pool_sampler_init(
	struct worker_rx_pool_sampler *sampler,
	struct rte_mempool *pool,
	uint64_t *capacity_slot,
	uint64_t *available_slot
) {
	if (pool == NULL || capacity_slot == NULL || available_slot == NULL) {
		return -1;
	}

	*sampler = (struct worker_rx_pool_sampler){
		.available_slot = available_slot,
		// Zero leaves the deadline already expired: the first worker
		// round resamples right after these initial values, and the
		// resample schedule starts from the loop clock.
		.next_sample_ns = 0,
	};

	*capacity_slot = (uint64_t)pool->size;
	*available_slot = (uint64_t)rte_mempool_avail_count(pool);

	return 0;
}

// Resamples the available gauge when the deadline has expired and
// schedules the next one a full interval past the current time.
static inline void
worker_rx_pool_sampler_sample(
	struct worker_rx_pool_sampler *sampler,
	struct rte_mempool *pool,
	uint64_t now_ns
) {
	if (now_ns < sampler->next_sample_ns) {
		return;
	}

	sampler->next_sample_ns = now_ns + WORKER_RX_POOL_SAMPLE_INTERVAL_NS;
	*sampler->available_slot = (uint64_t)rte_mempool_avail_count(pool);
}
