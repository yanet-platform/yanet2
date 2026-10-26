/*
 * Counter fixtures release temporary pipeline configurations on every path.
 *
 * Allocation failures leave the installed pipelines intact. Successful setup
 * copies the pipeline names into the live configuration before releasing each
 * temporary allocation exactly once.
 */

#include "api/agent.h"
#include "api/info.h"

#include "common/test_assert.h"
#include "devices/plain/api/controlplane.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_pipeline.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"

#include <errno.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define COUNTER_SURFACE_PIPELINE_COUNT 3
#define COUNTER_SURFACE_ALLOCATION_COUNT (COUNTER_SURFACE_PIPELINE_COUNT + 1)
#define COUNTER_SURFACE_AGENT_MEMORY (4u * 1024u * 1024u)

struct allocation_record {
	uintptr_t address;
	size_t releases;
};

static struct {
	size_t fail_at;
	size_t attempts;
	size_t count;
	size_t unknown_releases;
	struct allocation_record records[COUNTER_SURFACE_ALLOCATION_COUNT];
} allocation_ledger;

// Deny one helper allocation while keeping successful allocations native.
static void *
fixture_calloc(size_t count, size_t size) {
	if (allocation_ledger.attempts++ == allocation_ledger.fail_at) {
		return NULL;
	}
	if (allocation_ledger.count == COUNTER_SURFACE_ALLOCATION_COUNT) {
		return NULL;
	}
	void *pointer = calloc(count, size);
	if (pointer != NULL) {
		allocation_ledger.records[allocation_ledger.count++].address =
			(uintptr_t)pointer;
	}
	return pointer;
}

// Record ownership releases without changing the allocator's behavior.
static void
fixture_free(void *pointer) {
	if (pointer == NULL) {
		return;
	}
	bool found = false;
	for (size_t idx = 0; idx < allocation_ledger.count; ++idx) {
		if (allocation_ledger.records[idx].address ==
		    (uintptr_t)pointer) {
			++allocation_ledger.records[idx].releases;
			found = true;
			break;
		}
	}
	if (!found) {
		++allocation_ledger.unknown_releases;
	}
	free(pointer);
}

// All dependencies are already included, so only fixture allocations change.
#define calloc fixture_calloc
#define free fixture_free
#include "lib/controlplane/tests/counter_surface.h"
#undef free
#undef calloc

struct counter_surface_fixture {
	struct agent *agent;
	struct dp_config *dp_config;
	struct cp_config *cp_config;
};

// Attach one owner to the scenario's fresh shared-memory configuration.
static int
counter_surface_fixture_init(
	struct yanet_shm *shm, struct counter_surface_fixture *fixture
) {
	yanet_error *err = NULL;
	fixture->agent = agent_attach(
		shm, 0, "counter-surface", COUNTER_SURFACE_AGENT_MEMORY, &err
	);
	if (fixture->agent == NULL) {
		LOG(ERROR,
		    "agent_attach failed: %s",
		    err ? yanet_error_message(err) : "?");
		yanet_error_free(err);
		return TEST_FAILED;
	}
	fixture->dp_config = agent_dp_config(fixture->agent);
	fixture->cp_config = ADDR_OF(&fixture->agent->cp_config);
	return TEST_SUCCESS;
}

// Require every acquired allocation to have exactly one matching release.
static int
check_allocation_ownership(size_t attempts, size_t count) {
	TEST_ASSERT_EQUAL(
		allocation_ledger.attempts,
		attempts,
		"unexpected allocation count"
	);
	TEST_ASSERT_EQUAL(
		allocation_ledger.count,
		count,
		"unexpected acquired allocation count"
	);
	TEST_ASSERT_EQUAL(
		allocation_ledger.unknown_releases,
		0,
		"released an allocation the fixture does not own"
	);
	for (size_t idx = 0; idx < allocation_ledger.count; ++idx) {
		TEST_ASSERT_EQUAL(
			allocation_ledger.records[idx].releases,
			1,
			"allocation %zu must be released exactly once",
			idx
		);
	}
	return TEST_SUCCESS;
}

// Preserve ownership diagnostics before reclaiming leaked test allocations.
static int
check_and_reclaim_allocations(size_t attempts, size_t count) {
	int rc = check_allocation_ownership(attempts, count);
	for (size_t idx = 0; idx < allocation_ledger.count; ++idx) {
		if (allocation_ledger.records[idx].releases == 0) {
			free((void *)allocation_ledger.records[idx].address);
		}
	}
	return rc;
}

// Read installed names through the native configuration snapshot surface.
static int
check_pipeline_names(
	struct dp_config *dp_config, const char *const *names, size_t count
) {
	struct cp_pipeline_list_info *list =
		yanet_get_cp_pipeline_list_info(dp_config);
	TEST_ASSERT_NOT_NULL(list, "failed to read installed pipelines");

	uint64_t actual_count = list->count;
	bool names_match = actual_count == count;
	for (size_t expected = 0; expected < count && names_match; ++expected) {
		size_t matches = 0;
		for (uint64_t idx = 0; idx < actual_count; ++idx) {
			struct cp_pipeline_info *pipeline =
				yanet_get_cp_pipeline_info(list, idx);
			if (pipeline != NULL && pipeline->length == 0 &&
			    strcmp(pipeline->name, names[expected]) == 0) {
				++matches;
			}
		}
		names_match = matches == 1;
	}
	cp_pipeline_list_info_free(list);
	TEST_ASSERT_EQUAL(
		actual_count, count, "unexpected installed pipeline count"
	);
	TEST_ASSERT(names_match, "installed empty pipeline names do not match");
	return TEST_SUCCESS;
}

// Seed a live pipeline before denying one allocation in a replacement setup.
static int
run_pipeline_allocation_failure_test(struct yanet_shm *shm, size_t fail_at) {
	struct counter_surface_fixture fixture;
	TEST_ASSERT_SUCCESS(
		counter_surface_fixture_init(shm, &fixture),
		"failed to prepare counter fixture"
	);
	struct cp_pipeline_config retained = {.name = "retained"};
	struct cp_pipeline_config *configs[] = {&retained};
	yanet_error *err = NULL;
	int rc = cp_config_update_pipelines(
		fixture.dp_config, fixture.cp_config, 1, configs, &err
	);
	if (rc != TEST_SUCCESS) {
		LOG(ERROR,
		    "failed to seed retained pipeline: %s",
		    err ? yanet_error_message(err) : "?");
		yanet_error_free(err);
		agent_detach(fixture.agent);
		return TEST_FAILED;
	}
	struct cp_config_gen *before = cp_config_current_gen(fixture.cp_config);

	memset(&allocation_ledger, 0, sizeof(allocation_ledger));
	allocation_ledger.fail_at = fail_at;
	rc = install_pipelines(
		fixture.dp_config,
		fixture.cp_config,
		"replacement",
		COUNTER_SURFACE_PIPELINE_COUNT
	);
	int ownership_rc = check_and_reclaim_allocations(fail_at + 1, fail_at);
	bool unchanged = before == cp_config_current_gen(fixture.cp_config);
	const char *names[] = {"retained"};
	int snapshot_rc = check_pipeline_names(fixture.dp_config, names, 1);
	agent_detach(fixture.agent);

	TEST_ASSERT_EQUAL(
		rc, TEST_FAILED, "allocation failure was not propagated"
	);
	TEST_ASSERT_SUCCESS(
		ownership_rc, "failed setup leaked a temporary allocation"
	);
	TEST_ASSERT(unchanged, "failed setup published a new generation");
	TEST_ASSERT_SUCCESS(
		snapshot_rc, "failed setup changed the active pipelines"
	);
	return TEST_SUCCESS;
}

static int
run_outer_allocation_failure_test(struct yanet_shm *shm) {
	return run_pipeline_allocation_failure_test(shm, 0);
}

static int
run_first_pipeline_allocation_failure_test(struct yanet_shm *shm) {
	return run_pipeline_allocation_failure_test(shm, 1);
}

static int
run_later_pipeline_allocation_failure_test(struct yanet_shm *shm) {
	return run_pipeline_allocation_failure_test(shm, 3);
}

// Verifies that installed names outlive every temporary configuration
// allocation.
static int
run_successful_pipeline_installation_test(struct yanet_shm *shm) {
	struct counter_surface_fixture fixture;
	TEST_ASSERT_SUCCESS(
		counter_surface_fixture_init(shm, &fixture),
		"failed to prepare counter fixture"
	);
	memset(&allocation_ledger, 0, sizeof(allocation_ledger));
	allocation_ledger.fail_at = SIZE_MAX;
	int rc = install_counter_surface(
		fixture.agent,
		fixture.dp_config,
		fixture.cp_config,
		"fixture-device",
		"installed",
		COUNTER_SURFACE_PIPELINE_COUNT
	);
	int ownership_rc = check_and_reclaim_allocations(
		COUNTER_SURFACE_ALLOCATION_COUNT,
		COUNTER_SURFACE_ALLOCATION_COUNT
	);
	const char *names[] = {"installed-0", "installed-1", "installed-2"};
	int snapshot_rc = check_pipeline_names(
		fixture.dp_config, names, COUNTER_SURFACE_PIPELINE_COUNT
	);
	agent_detach(fixture.agent);

	TEST_ASSERT_SUCCESS(rc, "failed to install the real counter surface");
	TEST_ASSERT_SUCCESS(
		ownership_rc, "successful setup leaked a temporary allocation"
	);
	TEST_ASSERT_SUCCESS(
		snapshot_rc, "installed pipelines did not retain their names"
	);
	return TEST_SUCCESS;
}

typedef int (*counter_surface_scenario)(struct yanet_shm *shm);

// Give each scenario an independent configuration and reclaim it on failure.
static int
run_counter_surface_scenario(
	const char *name, counter_surface_scenario scenario
) {
	struct dataplane_ut_config config = {
		.cp_memory = 1u << 25,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices = (const char *[]){"01:00.0"},
		.device_count = 1,
		.devices_to_load = (const char *[]){"plain"},
		.devices_to_load_count = 1,
	};
	struct dataplane_ut *ut = dataplane_ut_new(&config);
	if (ut == NULL) {
		LOG(ERROR, "%s: dataplane_ut_new failed", name);
		return TEST_FAILED;
	}
	struct yanet_shm *shm = dataplane_ut_shm(ut);
	int rc = TEST_FAILED;
	if (shm != NULL) {
		rc = scenario(shm);
	}
	if (rc != TEST_SUCCESS) {
		LOG(ERROR, "%s failed", name);
	}
	dataplane_ut_free(ut);
	return rc;
}

int
main(void) {
	log_enable_name("info");
	struct {
		const char *name;
		counter_surface_scenario run;
	} scenarios[] = {
		{"outer allocation failure releases nothing",
		 run_outer_allocation_failure_test},
		{"first pipeline allocation failure releases the vector",
		 run_first_pipeline_allocation_failure_test},
		{"later pipeline allocation failure releases every earlier "
		 "allocation",
		 run_later_pipeline_allocation_failure_test},
		{"successful installation retains names after temporary "
		 "releases",
		 run_successful_pipeline_installation_test},
	};
	int failed = 0;
	for (size_t idx = 0; idx < sizeof(scenarios) / sizeof(scenarios[0]);
	     ++idx) {
		if (run_counter_surface_scenario(
			    scenarios[idx].name, scenarios[idx].run
		    ) != TEST_SUCCESS) {
			failed = 1;
		}
	}
	return failed;
}
