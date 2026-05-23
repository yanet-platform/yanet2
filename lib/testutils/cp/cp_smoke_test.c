#include "cp.h"

#include <stdint.h>
#include <string.h>

#include "api/agent.h"
#include "common/memory_block.h"
#include "common/strutils.h"
#include "common/test_assert.h"
#include "controlplane/agent/agent.h"
#include "controlplane/config/zone.h"
#include "dataplane/config/zone.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"

// 8 MB cp, 4 MB dp: enough for the harness itself plus a spare agent arena.
#define SMOKE_CP_SIZE (8u * 1024u * 1024u)
#define SMOKE_DP_SIZE (4u * 1024u * 1024u)

// 1 MB agent arena: sufficient for cp_module_init bookkeeping.
#define SMOKE_AGENT_MEM (1u * 1024u * 1024u)

static int
test_lifecycle(void) {
	yanet_error *err = NULL;

	struct test_shm *shm =
		test_shm_create(SMOKE_CP_SIZE, SMOKE_DP_SIZE, NULL, 0, &err);
	TEST_ASSERT_NOT_NULL(shm, "test_shm_create failed");

	struct agent *agent = agent_attach(
		(struct yanet_shm *)test_shm_dp_config(shm),
		0,
		"smoke-lifecycle",
		SMOKE_AGENT_MEM,
		&err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	agent_cleanup(agent);
	test_shm_destroy(shm);

	return TEST_SUCCESS;
}

static int
test_free_size_baseline(void) {
	yanet_error *err = NULL;

	struct test_shm *shm =
		test_shm_create(SMOKE_CP_SIZE, SMOKE_DP_SIZE, NULL, 0, &err);
	TEST_ASSERT_NOT_NULL(shm, "test_shm_create failed");

	struct dp_config *dp_config =
		(struct dp_config *)test_shm_dp_config(shm);
	struct cp_config *cp_config = ADDR_OF(&dp_config->cp_config);

	// Capture how much cp memory is free before the agent is attached.
	size_t before = block_allocator_free_size(&cp_config->block_allocator);

	struct agent *agent = agent_attach(
		(struct yanet_shm *)test_shm_dp_config(shm),
		0,
		"smoke-free-size",
		SMOKE_AGENT_MEM,
		&err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	// The agent's own arena is drawn from cp memory, so cp free_size
	// must decrease while the agent is alive.
	size_t during = block_allocator_free_size(&cp_config->block_allocator);
	TEST_ASSERT(
		during < before,
		"cp free_size did not decrease after agent_attach: "
		"before=%zu during=%zu",
		before,
		during
	);

	// After cleanup, the agent's arenas are returned to cp. The only
	// permanent consumption is the cp_agent_registry slot for the new
	// agent name, which is a fixed small overhead.
	agent_cleanup(agent);

	size_t after = block_allocator_free_size(&cp_config->block_allocator);
	TEST_ASSERT(
		after > during,
		"cp free_size did not increase after agent_cleanup: "
		"during=%zu after=%zu",
		during,
		after
	);

	test_shm_destroy(shm);
	return TEST_SUCCESS;
}

static int
test_module_registration(void) {
	yanet_error *err = NULL;

	static const char *names[] = {"route", "decap"};
	struct test_shm *shm =
		test_shm_create(SMOKE_CP_SIZE, SMOKE_DP_SIZE, names, 2, &err);
	TEST_ASSERT_NOT_NULL(shm, "test_shm_create with modules failed");

	struct dp_config *dp_config =
		(struct dp_config *)test_shm_dp_config(shm);

	TEST_ASSERT_EQUAL(
		(long)dp_config->module_count,
		(long)2,
		"expected 2 registered modules, got %lu",
		dp_config->module_count
	);

	struct dp_module *modules = ADDR_OF(&dp_config->dp_modules);

	bool found_route = false;
	bool found_decap = false;
	for (uint64_t idx = 0; idx < dp_config->module_count; ++idx) {
		TEST_ASSERT_NOT_NULL(
			(void *)(uintptr_t)modules[idx].handler,
			"module[%lu] has NULL handler",
			idx
		);
		if (strncmp(modules[idx].name, "route", 80) == 0) {
			found_route = true;
		}
		if (strncmp(modules[idx].name, "decap", 80) == 0) {
			found_decap = true;
		}
	}

	TEST_ASSERT(found_route, "module 'route' not registered");
	TEST_ASSERT(found_decap, "module 'decap' not registered");

	test_shm_destroy(shm);
	return TEST_SUCCESS;
}

static int
test_attach_no_modules(void) {
	yanet_error *err = NULL;

	struct test_shm *shm =
		test_shm_create(SMOKE_CP_SIZE, SMOKE_DP_SIZE, NULL, 0, &err);
	TEST_ASSERT_NOT_NULL(shm, "test_shm_create failed");

	struct agent *agent = agent_attach(
		(struct yanet_shm *)test_shm_dp_config(shm),
		0,
		"smoke-no-modules",
		SMOKE_AGENT_MEM,
		&err
	);
	TEST_ASSERT_NOT_NULL(
		agent, "agent_attach should succeed even with no modules"
	);

	agent_cleanup(agent);
	test_shm_destroy(shm);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	int rc = 0;

	if (test_lifecycle() != TEST_SUCCESS) {
		LOG(ERROR, "test_lifecycle FAILED");
		rc = 1;
	}

	if (test_free_size_baseline() != TEST_SUCCESS) {
		LOG(ERROR, "test_free_size_baseline FAILED");
		rc = 1;
	}

	if (test_module_registration() != TEST_SUCCESS) {
		LOG(ERROR, "test_module_registration FAILED");
		rc = 1;
	}

	if (test_attach_no_modules() != TEST_SUCCESS) {
		LOG(ERROR, "test_attach_no_modules FAILED");
		rc = 1;
	}

	if (rc == 0) {
		LOG(INFO, "all smoke tests passed");
	}

	return rc;
}
