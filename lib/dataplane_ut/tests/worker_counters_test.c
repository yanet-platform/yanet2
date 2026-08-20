// verifies that every worker of an instance is bound to the counter slots its
// own storage holds under the matching counter names.

#include "api/agent.h"
#include "common/memory_address.h"
#include "common/test_assert.h"
#include "lib/counters/counters.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/worker/counters.h"
#include "lib/dataplane_ut/dataplane_ut.h"

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

	dataplane_ut_free(ut);

	return result;
}
