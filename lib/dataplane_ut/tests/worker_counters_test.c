// verifies that every worker of an instance is bound to the counter slots its
// own storage holds under the matching counter names, and that the rx pool
// gauges are seeded and resampled independently per worker pool.

#include "api/agent.h"
#include "common/memory_address.h"
#include "common/test_assert.h"
#include "lib/counters/counters.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/worker/counters.h"
#include "lib/dataplane/worker/rx_pool_sampler.h"
#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/dataplane_ut/mempool.h"

#include <rte_mbuf.h>

#include <string.h>

#define WORKER_COUNT 2

// Finds a counter by walking the registry names one by one, so the check does
// not rely on the same lookup that registration uses.
static uint64_t
lookup_id(struct counter_registry *registry, const char *name) {
	struct counter *names = ADDR_OF(&registry->names);
	for (uint64_t idx = 0; idx < registry->count; ++idx) {
		if (strncmp(names[idx].name, name, COUNTER_NAME_LEN) == 0) {
			return idx;
		}
	}
	return COUNTER_INVALID;
}

static int
check_worker(struct dp_config *dp_config, uint64_t worker_idx) {
	struct dp_worker *worker =
		ADDR_OF(ADDR_OF(&dp_config->workers) + worker_idx);
	struct counter_storage *storage =
		ADDR_OF(ADDR_OF(&dp_config->worker_counter_storages) +
			worker_idx);

	const struct {
		const char *name;
		uint64_t value_idx;
		const uint64_t *bound;
	} slots[] = {
		{"iterations", 0, worker->iterations},
		{"rx", 0, worker->rx_count},
		{"rx", 1, worker->rx_size},
		{"tx", 0, worker->tx_count},
		{"tx", 1, worker->tx_size},
		{"remote_rx", 0, worker->remote_rx_count},
		{"remote_tx", 0, worker->remote_tx_count},
		{"rx_bursts", 0, worker->rx_bursts},
		{"local_tx_drops", 0, worker->local_tx_drops},
		{"remote_tx_drops", 0, worker->remote_tx_drops},
		{"drops", 0, worker->drop_count},
	};

	for (size_t idx = 0; idx < sizeof(slots) / sizeof(slots[0]); ++idx) {
		uint64_t id =
			lookup_id(&dp_config->worker_counters, slots[idx].name);
		TEST_ASSERT(
			id != COUNTER_INVALID,
			"counter '%s' is not registered",
			slots[idx].name
		);
		TEST_ASSERT(
			slots[idx].bound == counter_get_address(id, storage) +
						    slots[idx].value_idx,
			"worker %lu counter '%s' value %lu is bound to a "
			"foreign slot",
			worker_idx,
			slots[idx].name,
			slots[idx].value_idx
		);
	}

	return TEST_SUCCESS;
}

// Gauge slot pair of one worker, resolved through the shared registry by
// name so the check does not assume a registration order.
struct gauge_slots {
	uint64_t *capacity;
	uint64_t *available;
};

static struct gauge_slots
worker_gauge_slots(struct dp_config *dp_config, uint64_t worker_idx) {
	struct gauge_slots slots = {
		.capacity = worker_counter_slot(
			dp_config,
			worker_idx,
			lookup_id(
				&dp_config->worker_counters,
				"rx_mempool_capacity"
			)
		),
		.available = worker_counter_slot(
			dp_config,
			worker_idx,
			lookup_id(
				&dp_config->worker_counters,
				"rx_mempool_available"
			)
		),
	};
	return slots;
}

// Verifies that a freshly built harness publishes a real occupancy for
// every worker: both gauges hold the shared mock pool's values, not the
// zero fill of a just-spawned storage.
static int
check_seeded_gauges(struct dp_config *dp_config) {
	for (uint64_t idx = 0; idx < WORKER_COUNT; ++idx) {
		struct gauge_slots slots = worker_gauge_slots(dp_config, idx);
		TEST_ASSERT_NOT_NULL(
			slots.capacity, "worker %lu has no capacity slot", idx
		);
		TEST_ASSERT_NOT_NULL(
			slots.available, "worker %lu has no available slot", idx
		);
		TEST_ASSERT_EQUAL(
			TEST_MEMPOOL_DEFAULT_SIZE,
			*slots.capacity,
			"worker %lu seeded capacity",
			idx
		);
		TEST_ASSERT_EQUAL(
			TEST_MEMPOOL_DEFAULT_SIZE,
			*slots.available,
			"worker %lu seeded available",
			idx
		);
	}

	return TEST_SUCCESS;
}

// Verifies that the slot accessor refuses an out-of-range worker index
// and counter id instead of handing back a stray pointer.
static int
check_slot_bounds(struct dp_config *dp_config) {
	uint64_t capacity_id =
		lookup_id(&dp_config->worker_counters, "rx_mempool_capacity");
	TEST_ASSERT(
		capacity_id != COUNTER_INVALID,
		"rx_mempool_capacity is not registered"
	);

	TEST_ASSERT_NULL(
		worker_counter_slot(dp_config, WORKER_COUNT, capacity_id),
		"out-of-range worker index must yield no slot"
	);
	TEST_ASSERT_NULL(
		worker_counter_slot(
			dp_config, 0, dp_config->worker_counters.count
		),
		"out-of-range counter id must yield no slot"
	);

	return TEST_SUCCESS;
}

// Verifies the sampler timing contract on two pools of different
// capacity: init publishes capacity and current occupancy at once, the
// deadline starts expired so a first call resamples both pools, an
// earlier call is a no-op, a call at the deadline resamples and
// reschedules, and a pool whose sampler then stays idle keeps its
// gauge.
static int
check_sampler_interval(struct dp_config *dp_config) {
	struct rte_mempool *pool_a = test_mempool_create_sized(8);
	struct rte_mempool *pool_b = test_mempool_create_sized(16);
	TEST_ASSERT_NOT_NULL(pool_a, "failed to create the 8-object pool");
	TEST_ASSERT_NOT_NULL(pool_b, "failed to create the 16-object pool");

	struct gauge_slots slots_a = worker_gauge_slots(dp_config, 0);
	struct gauge_slots slots_b = worker_gauge_slots(dp_config, 1);
	TEST_ASSERT_NOT_NULL(slots_a.capacity, "worker 0 has no capacity slot");
	TEST_ASSERT_NOT_NULL(
		slots_a.available, "worker 0 has no available slot"
	);
	TEST_ASSERT_NOT_NULL(slots_b.capacity, "worker 1 has no capacity slot");
	TEST_ASSERT_NOT_NULL(
		slots_b.available, "worker 1 has no available slot"
	);

	struct worker_rx_pool_sampler sampler_a;
	struct worker_rx_pool_sampler sampler_b;
	TEST_ASSERT_SUCCESS(
		worker_rx_pool_sampler_init(
			&sampler_a, pool_a, slots_a.capacity, slots_a.available
		),
		"failed to init the sampler for the 8-object pool"
	);
	TEST_ASSERT_SUCCESS(
		worker_rx_pool_sampler_init(
			&sampler_b, pool_b, slots_b.capacity, slots_b.available
		),
		"failed to init the sampler for the 16-object pool"
	);
	TEST_ASSERT_EQUAL(8, *slots_a.capacity, "pool A capacity slot");
	TEST_ASSERT_EQUAL(8, *slots_a.available, "pool A initial available");
	TEST_ASSERT_EQUAL(16, *slots_b.capacity, "pool B capacity slot");
	TEST_ASSERT_EQUAL(16, *slots_b.available, "pool B initial available");

	struct rte_mbuf *held[5] = {0};
	for (size_t idx = 0; idx < 2; ++idx) {
		held[idx] = rte_pktmbuf_alloc(pool_a);
		TEST_ASSERT_NOT_NULL(
			held[idx], "pool A allocation %zu failed", idx
		);
	}

	// Both deadlines start expired, so this call resamples both pools;
	// only pool A lost objects, so only its gauge moves.
	worker_rx_pool_sampler_sample(&sampler_a, pool_a, 0);
	worker_rx_pool_sampler_sample(&sampler_b, pool_b, 0);
	TEST_ASSERT_EQUAL(6, *slots_a.available, "pool A first resample");
	TEST_ASSERT_EQUAL(16, *slots_b.available, "pool B first resample");

	for (size_t idx = 2; idx < 5; ++idx) {
		held[idx] = rte_pktmbuf_alloc(pool_a);
		TEST_ASSERT_NOT_NULL(
			held[idx], "pool A allocation %zu failed", idx
		);
	}

	worker_rx_pool_sampler_sample(
		&sampler_a, pool_a, WORKER_RX_POOL_SAMPLE_INTERVAL_NS - 1
	);
	TEST_ASSERT_EQUAL(
		6,
		*slots_a.available,
		"a sample before the deadline must not republish"
	);

	worker_rx_pool_sampler_sample(
		&sampler_a, pool_a, WORKER_RX_POOL_SAMPLE_INTERVAL_NS
	);
	TEST_ASSERT_EQUAL(
		3, *slots_a.available, "an elapsed deadline must republish"
	);
	TEST_ASSERT_EQUAL(
		16,
		*slots_b.available,
		"a pool not resampled again must keep its gauge"
	);

	for (size_t idx = 0; idx < 5; ++idx) {
		rte_pktmbuf_free(held[idx]);
	}
	test_mempool_free(pool_a);
	test_mempool_free(pool_b);

	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 25,
		.dp_memory = 1u << 20,
		.worker_count = WORKER_COUNT,
	};

	struct dataplane_ut *ut = dataplane_ut_new(&cfg);
	TEST_ASSERT_NOT_NULL(ut, "failed to create the harness");

	struct dp_config *dp_config =
		yanet_shm_dp_config(dataplane_ut_shm(ut), 0);
	TEST_ASSERT_NOT_NULL(dp_config, "harness has no dataplane config");

	int result = TEST_SUCCESS;
	for (uint64_t idx = 0; idx < WORKER_COUNT && result == TEST_SUCCESS;
	     ++idx) {
		result = check_worker(dp_config, idx);
	}
	if (result == TEST_SUCCESS) {
		result = check_seeded_gauges(dp_config);
	}
	if (result == TEST_SUCCESS) {
		result = check_slot_bounds(dp_config);
	}
	if (result == TEST_SUCCESS) {
		result = check_sampler_interval(dp_config);
	}

	dataplane_ut_free(ut);

	return result;
}
