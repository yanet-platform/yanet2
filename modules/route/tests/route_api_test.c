/*
 * Tests that construction of the table object does not leak memory when
 * its own data setup fails partway through, at each of its fallible steps.
 *
 * Attaching an agent carves a private arena of exactly the given byte
 * limit from the shared allocator, and every allocation the module under
 * test makes draws from that one arena. The limits from a small one up to
 * the first that fits the object starve each step in turn.
 *
 * The arena's free-byte count must return to its value recorded right
 * after attach. A byte a cleanup path leaves unfreed stays missing from
 * that count, and a block freed twice makes the count overshoot instead,
 * so the same check catches both a leak and a double free.
 */

#include "api/agent.h"
#include "common/memory_block.h"
#include "common/test_assert.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"
#include "modules/route/api/fib_object.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

// The agent limits scanned, from below the object to above it.
#define ROUTE_TEST_LIMIT_FROM (2u * 1024u)
#define ROUTE_TEST_LIMIT_TO (64u * 1024u)
#define ROUTE_TEST_LIMIT_STEP 64u

/*
 * Starves the construction at every limit until one fits it.
 *
 * A failed construction must report its data setup and leave the arena's
 * free bytes as attach left them; the scan must reach a limit that fits.
 */
static int
run_failed_create_scan_test(struct yanet_shm *shm) {
	uint32_t failed_data_setups = 0;
	for (size_t limit = ROUTE_TEST_LIMIT_FROM; limit < ROUTE_TEST_LIMIT_TO;
	     limit += ROUTE_TEST_LIMIT_STEP) {
		yanet_error *err = NULL;
		struct agent *agent =
			agent_attach(shm, 0, "route-test", limit, &err);
		TEST_ASSERT_NOT_NULL(
			agent, "agent_attach failed at %zu", limit
		);

		size_t baseline =
			block_allocator_free_size(&agent->block_allocator);

		struct cp_object *cp =
			route_fib_object_new(agent, "probe", &err);
		if (cp != NULL) {
			TEST_ASSERT(
				failed_data_setups > 0,
				"no limit starved the data setup"
			);
			agent_detach(agent);
			return TEST_SUCCESS;
		}

		const char *errmsg =
			(err != NULL) ? yanet_error_message(err) : "";
		if (strstr(errmsg, "failed to init route fib object data")) {
			++failed_data_setups;
		}
		yanet_error_reset(&err);

		size_t after =
			block_allocator_free_size(&agent->block_allocator);
		TEST_ASSERT_EQUAL(
			(long)after,
			(long)baseline,
			"free bytes drifted after failed create at %zu: "
			"baseline=%zu after=%zu",
			limit,
			baseline,
			after
		);

		agent_detach(agent);
	}
	TEST_ASSERT(
		0, "no limit up to %u fits the object", ROUTE_TEST_LIMIT_TO
	);
	return TEST_FAILED;
}

int
main(void) {
	log_enable_name("debug");

	const char *port_names[] = {"01:00.0"};
	const char *modules[] = {"route"};
	const char *devs_to_load[] = {"plain"};
	const char *objects_to_load[] = {"route_fib"};

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
		.objects_to_load = objects_to_load,
		.objects_to_load_count = 1,
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

	int res = run_failed_create_scan_test(shm);
	dataplane_ut_free(ut);

	return (res == TEST_SUCCESS) ? 0 : 1;
}
