/*
 * Tests that route_module_config_create does not leak memory when
 * route_module_config_data_init fails due to OOM after cp_module_init
 * succeeds.
 *
 * Mechanism: agent_attach carves a private arena of exactly memory_limit
 * bytes from the cp_config allocator. All module allocations draw from
 * this arena.
 *
 * Leak detection: block_allocator_free_size must return to its value
 * recorded immediately after agent_attach. Any byte not freed by a
 * cleanup path stays missing from the free list.
 */

#include "api/agent.h"
#include "common/memory.h"
#include "common/memory_block.h"
#include "common/strutils.h"
#include "common/test_assert.h"
#include "controlplane/agent/agent.h"
#include "lib/errors/errors.h"
#include "logging/log.h"
#include "mock/config.h"
#include "mock/mock.h"
#include "modules/route/api/controlplane.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/*
 * 16 KB: enough for cp_module_init's small allocations (empirically < 8 KB),
 * but well below the 32 KB lpm_init page-chunk request.
 */
#define ROUTE_TEST_MEMORY_LIMIT (16u * 1024u)

static int
run_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm, 0, "route-test", ROUTE_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	size_t baseline = block_allocator_free_size(&agent->block_allocator);

	struct cp_module *cp = route_module_config_create(agent, "probe", &err);
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

int
main(void) {
	log_enable_name("debug");

	struct yanet_mock_config config;
	memset(&config, 0, sizeof(config));
	config.cp_memory = 1 << 25;
	config.dp_memory = 1 << 20;
	config.device_count = 1;
	config.devices[0].id = 0;
	strtcpy(config.devices[0].name,
		"01:00.0",
		sizeof(config.devices[0].name));
	config.worker_count = 1;

	struct yanet_mock mock;
	int res = yanet_mock_init(&mock, &config, NULL);
	if (res != 0) {
		fprintf(stderr, "yanet_mock_init failed: %d\n", res);
		return 1;
	}

	struct yanet_shm *shm = yanet_mock_shm(&mock);
	if (shm == NULL) {
		fprintf(stderr, "yanet_mock_shm returned NULL\n");
		yanet_mock_free(&mock);
		return 1;
	}

	res = run_test(shm);
	yanet_mock_free(&mock);

	return (res == TEST_SUCCESS) ? 0 : 1;
}
