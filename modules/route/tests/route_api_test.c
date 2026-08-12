/*
 * Tests that construction does not leak memory when the module's own data
 * setup fails partway through, at each of its fallible steps.
 *
 * Attaching an agent carves a private arena of exactly the given byte
 * limit from the shared allocator, and every allocation the module under
 * test makes draws from that one arena.
 *
 * The arena's free-byte count must return to its value recorded right
 * after attach. A byte a cleanup path leaves unfreed stays missing from
 * that count, and a block freed twice makes the count overshoot instead,
 * so the same check catches both a leak and a double free.
 */

#include "api/agent.h"
#include "common/memory_block.h"
#include "common/test_assert.h"
#include "controlplane/agent/agent.h"
#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"
#include "logging/log.h"
#include "modules/route/api/controlplane.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/*
 * 16 KB: enough for cp_module_init's small allocations (empirically < 8 KB),
 * but well below the 32 KB lpm_init page-chunk request.
 */
#define ROUTE_TEST_MEMORY_LIMIT (16u * 1024u)

/*
 * 40800 bytes, found empirically: enough for construction plus one
 * routing table's own setup, but too little for the second table to also
 * succeed. This size reaches data setup's own error path instead of the
 * shared type teardown, verifying that path frees the first table
 * exactly once.
 */
#define ROUTE_TEST_SECOND_LPM_LIMIT (40800u)

static int
run_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm, 0, "route-test", ROUTE_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	size_t baseline = block_allocator_free_size(&agent->block_allocator);

	struct cp_module *cp = route_module_config_new(agent, "probe", &err);
	TEST_ASSERT_NULL(cp, "create unexpectedly succeeded");

	const char *errmsg = (err != NULL) ? yanet_error_message(err) : "";
	TEST_ASSERT_STR_CONTAINS(
		errmsg, "failed to init config data", "wrong failure path"
	);
	yanet_error_reset(&err);

	size_t after = block_allocator_free_size(&agent->block_allocator);
	TEST_ASSERT_EQUAL(
		(long)after,
		(long)baseline,
		"memory leaked after failed create: baseline=%zu after=%zu",
		baseline,
		after
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

/*
 * Same shape as the first test, but sized to fail at the second routing
 * table's own setup instead of the first.
 *
 * This exercises the error path for a partially constructed module,
 * verifying it frees the first table's memory exactly once rather than
 * leaving that job to a teardown that would double free it.
 */
static int
run_second_lpm_failure_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm,
		0,
		"route-test-second-lpm",
		ROUTE_TEST_SECOND_LPM_LIMIT,
		&err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	size_t baseline = block_allocator_free_size(&agent->block_allocator);

	struct cp_module *cp = route_module_config_new(agent, "probe", &err);
	TEST_ASSERT_NULL(cp, "create unexpectedly succeeded");

	const char *errmsg = (err != NULL) ? yanet_error_message(err) : "";
	TEST_ASSERT_STR_CONTAINS(
		errmsg, "failed to init config data", "wrong failure path"
	);
	yanet_error_reset(&err);

	size_t after = block_allocator_free_size(&agent->block_allocator);
	TEST_ASSERT_EQUAL(
		(long)after,
		(long)baseline,
		"free list drifted after failed create: baseline=%zu after=%zu",
		baseline,
		after
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	const char *port_names[] = {"01:00.0"};
	const char *modules[] = {"route"};
	const char *devs_to_load[] = {"plain"};

	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 25,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices = port_names,
		.device_count = 1,
		.modules = modules,
		.module_count = 1,
		.devices_to_load = devs_to_load,
		.devices_to_load_count = 1,
	};

	struct dataplane_ut *ut = dataplane_ut_new(&cfg);
	if (ut == NULL) {
		fprintf(stderr, "dataplane_ut_new failed\n");
		return 1;
	}

	struct yanet_shm *shm = dataplane_ut_shm(ut);
	if (shm == NULL) {
		fprintf(stderr, "dataplane_ut_shm returned NULL\n");
		dataplane_ut_free(ut);
		return 1;
	}

	int res = run_test(shm);
	if (res == TEST_SUCCESS) {
		res = run_second_lpm_failure_test(shm);
	}
	dataplane_ut_free(ut);

	return (res == TEST_SUCCESS) ? 0 : 1;
}
