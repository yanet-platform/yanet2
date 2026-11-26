/*
 * A refused function or pipeline update returns every byte it took.
 *
 * Each scenario fills its registry to capacity, so that one more name must
 * grow it, then exhausts the control-plane zone and retries the update with a
 * budget raised by one smallest block at a time until the registry upsert
 * refuses it, where the item just built belongs to no registry and only the
 * update itself can release it. Every refused attempt must leave the zone's
 * free bytes exactly as they were.
 */

#include "api/agent.h"
#include "api/config.h"

#include "common/memory.h"
#include "common/memory_block.h"
#include "common/test_assert.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/zone.h"

#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"

#include "lib/logging/log.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define ZONE_UPDATE_TEST_AGENT_MEMORY (1u << 20)

// The largest budget the sweep may release; the update must reach the
// registry upsert within it.
#define ZONE_UPDATE_TEST_BUDGET_BLOCK (1u << 14)

// The smallest block a request can take: a red zone on each side of a
// sanitized allocation leaves the pools below unreachable.
#define ZONE_UPDATE_TEST_MIN_BLOCK                                             \
	(ASAN_RED_ZONE ? 4 * ASAN_RED_ZONE : MEMORY_BLOCK_ALLOCATOR_MIN_SIZE)

// The allocations a scenario holds so that the zone has no room left.
struct zone_hog {
	struct memory_context *memory_context;
	void **blocks;
	size_t *sizes;
	size_t count;
	size_t capacity;
};

// Take one allocator block of the given internal size from the zone.
//
// Returns 0 with the block held, or -1 when the zone has no block of that
// size left or the host cannot record another one.
static int
zone_hog_take(struct zone_hog *hog, size_t block_size) {
	if (hog->count == hog->capacity) {
		size_t capacity = hog->capacity ? hog->capacity * 2 : 1024;
		void **blocks =
			realloc(hog->blocks, capacity * sizeof(*blocks));
		if (blocks == NULL) {
			return -1;
		}
		hog->blocks = blocks;
		size_t *sizes = realloc(hog->sizes, capacity * sizeof(*sizes));
		if (sizes == NULL) {
			return -1;
		}
		hog->sizes = sizes;
		hog->capacity = capacity;
	}

	size_t size = block_size - 2 * ASAN_RED_ZONE;
	void *block = memory_balloc(hog->memory_context, size);
	if (block == NULL) {
		return -1;
	}
	hog->blocks[hog->count] = block;
	hog->sizes[hog->count] = size;
	++hog->count;
	return 0;
}

// Gives back the block taken last, so blocks return in the reverse order
// the hog took them.
static void
zone_hog_put_last(struct zone_hog *hog) {
	--hog->count;
	memory_bfree(
		hog->memory_context,
		hog->blocks[hog->count],
		hog->sizes[hog->count]
	);
}

// Leaves the zone no free block of the given internal size or below, so any
// request that small fails until a block is given back.
static void
zone_hog_drain(struct zone_hog *hog, size_t top_block_size) {
	for (size_t block_size = top_block_size;
	     block_size >= ZONE_UPDATE_TEST_MIN_BLOCK;
	     block_size >>= 1) {
		while (zone_hog_take(hog, block_size) == 0) {
		}
	}
}

// Gives every held block back and leaves the hog empty for another hold.
static void
zone_hog_release(struct zone_hog *hog) {
	while (hog->count > 0) {
		zone_hog_put_last(hog);
	}
	free(hog->blocks);
	free(hog->sizes);
	memset(hog, 0, sizeof(*hog));
}

// Leave exactly budget bytes free by carving one budget block.
//
// The zone must hold no other block a request can take than the budget
// block the caller has just returned. Each level splits the remainder in half,
// keeping the free half whenever the budget has that bit; the last remainder is
// one smallest block, so the budget counts it.
static void
zone_hog_carve(struct zone_hog *hog, size_t budget) {
	size_t keep = budget - ZONE_UPDATE_TEST_MIN_BLOCK;
	for (size_t block_size = ZONE_UPDATE_TEST_BUDGET_BLOCK / 2;
	     block_size >= ZONE_UPDATE_TEST_MIN_BLOCK;
	     block_size >>= 1) {
		if (zone_hog_take(hog, block_size) == 0 &&
		    (keep & block_size)) {
			zone_hog_put_last(hog);
		}
	}
}

// One update a sweep retries: it publishes a configuration that adds one
// name to a full registry.
typedef int (*zone_update_func)(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	void *data,
	yanet_error **err
);

// Returns the zone's free bytes, an exact balance because the allocator
// never merges blocks it gets back.
static size_t
zone_free_size(struct cp_config *cp_config) {
	return block_allocator_free_size(&cp_config->block_allocator);
}

// Retry an update on an exhausted zone with a growing budget until the
// registry upsert refuses it.
//
// Verifies that every refused attempt up to and including the one refused
// at the upsert, told by a top error frame starting with the given prefix,
// leaves the zone's free bytes as they were.
static int
run_update_sweep(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	zone_update_func update,
	void *data,
	const char *upsert_refusal
) {
	struct zone_hog hog = {.memory_context = &cp_config->memory_context};
	struct zone_hog budgets = {
		.memory_context = &cp_config->memory_context
	};

	while (zone_hog_take(&budgets, ZONE_UPDATE_TEST_BUDGET_BLOCK) == 0) {
	}
	zone_hog_drain(&hog, ZONE_UPDATE_TEST_BUDGET_BLOCK / 2);

	int result = TEST_FAILED;
	for (size_t budget = ZONE_UPDATE_TEST_MIN_BLOCK;
	     budget <= ZONE_UPDATE_TEST_BUDGET_BLOCK;
	     budget += ZONE_UPDATE_TEST_MIN_BLOCK) {
		if (budgets.count == 0) {
			LOG(ERROR, "the zone ran out of budget blocks");
			goto out;
		}
		zone_hog_put_last(&budgets);
		zone_hog_carve(&hog, budget);

		size_t free_before = zone_free_size(cp_config);
		yanet_error *err = NULL;
		if (update(dp_config, cp_config, data, &err) == 0) {
			yanet_error_free(err);
			LOG(ERROR,
			    "the update was published without a refusal at "
			    "the registry upsert");
			goto out;
		}

		size_t free_after = zone_free_size(cp_config);
		const char *message = yanet_error_message(err);
		if (free_after != free_before) {
			LOG(ERROR,
			    "an update refused with a budget of %zu bytes "
			    "kept %zu bytes: %s",
			    budget,
			    free_before - free_after,
			    message ? message : "?");
			yanet_error_free(err);
			goto out;
		}
		bool refused_at_upsert =
			message != NULL &&
			!strncmp(
				message, upsert_refusal, strlen(upsert_refusal)
			);
		yanet_error_free(err);
		if (refused_at_upsert) {
			result = TEST_SUCCESS;
			goto out;
		}

		zone_hog_drain(&hog, ZONE_UPDATE_TEST_BUDGET_BLOCK / 2);
	}
	LOG(ERROR, "no budget was refused at the registry upsert");

out:
	zone_hog_release(&hog);
	zone_hog_release(&budgets);
	return result;
}

// Count the live items of a registry.
static uint64_t
registry_live_count(struct registry *registry) {
	uint64_t count = 0;
	for (uint64_t idx = 0; idx < registry->capacity; ++idx) {
		if (registry_get(registry, idx) != NULL) {
			++count;
		}
	}
	return count;
}

// Build a function with one empty chain.
static struct cp_function_config *
function_config_new(const char *name) {
	struct cp_function_config *function =
		cp_function_config_create(name, 1);
	if (function == NULL) {
		return NULL;
	}
	struct cp_chain_config *chain =
		cp_chain_config_create("chain", 0, NULL, NULL);
	if (chain == NULL ||
	    cp_function_config_set_chain(function, 0, chain, 1)) {
		cp_chain_config_free(chain);
		cp_function_config_free(function);
		return NULL;
	}
	return function;
}

// The update a function sweep retries: one function configuration, passed
// by the sweep.
static int
update_function(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	void *data,
	yanet_error **err
) {
	struct cp_function_config *configs[] = {data};
	return cp_config_update_functions(
		dp_config, cp_config, 1, configs, err
	);
}

// Verifies that a function update refused at the registry upsert releases
// the function it built.
static int
run_function_upsert_refusal_test(struct yanet_shm *shm) {
	yanet_error *attach_err = NULL;
	struct agent *agent = agent_attach(
		shm,
		0,
		"zone-update-function",
		ZONE_UPDATE_TEST_AGENT_MEMORY,
		&attach_err
	);
	TEST_ASSERT_NOT_NULL(
		agent,
		"agent_attach failed: %s",
		attach_err ? yanet_error_message(attach_err) : "?"
	);
	struct dp_config *dp_config = agent_dp_config(agent);
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	struct registry *registry =
		&cp_config_current_gen(cp_config)->function_registry.registry;
	uint64_t fill = registry->capacity - registry_live_count(registry);
	for (uint64_t idx = 0; idx < fill; ++idx) {
		char name[CP_FUNCTION_NAME_LEN];
		snprintf(name, sizeof(name), "fill%lu", idx);
		struct cp_function_config *config = function_config_new(name);
		TEST_ASSERT_NOT_NULL(config, "failed to build function config");
		yanet_error *err = NULL;
		int rc = update_function(dp_config, cp_config, config, &err);
		cp_function_config_free(config);
		TEST_ASSERT_SUCCESS(
			rc,
			"failed to fill the function registry: %s",
			err ? yanet_error_message(err) : "?"
		);
		yanet_error_free(err);
	}

	struct cp_function_config *config = function_config_new("grow");
	TEST_ASSERT_NOT_NULL(config, "failed to build function config");
	int rc = run_update_sweep(
		dp_config,
		cp_config,
		update_function,
		config,
		"failed to upsert function"
	);
	cp_function_config_free(config);
	return rc;
}

// The update a pipeline sweep retries: one pipeline configuration, passed
// by the sweep.
static int
update_pipeline(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	void *data,
	yanet_error **err
) {
	struct cp_pipeline_config *configs[] = {data};
	return cp_config_update_pipelines(
		dp_config, cp_config, 1, configs, err
	);
}

// Verifies that a pipeline update refused at the registry upsert releases
// the pipeline it built.
static int
run_pipeline_upsert_refusal_test(struct yanet_shm *shm) {
	yanet_error *attach_err = NULL;
	struct agent *agent = agent_attach(
		shm,
		0,
		"zone-update-pipeline",
		ZONE_UPDATE_TEST_AGENT_MEMORY,
		&attach_err
	);
	TEST_ASSERT_NOT_NULL(
		agent,
		"agent_attach failed: %s",
		attach_err ? yanet_error_message(attach_err) : "?"
	);
	struct dp_config *dp_config = agent_dp_config(agent);
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	struct registry *registry =
		&cp_config_current_gen(cp_config)->pipeline_registry.registry;
	uint64_t fill = registry->capacity - registry_live_count(registry);
	for (uint64_t idx = 0; idx < fill; ++idx) {
		char name[CP_PIPELINE_NAME_LEN];
		snprintf(name, sizeof(name), "fill%lu", idx);
		struct cp_pipeline_config *config =
			cp_pipeline_config_create(name, 0);
		TEST_ASSERT_NOT_NULL(config, "failed to build pipeline config");
		yanet_error *err = NULL;
		int rc = update_pipeline(dp_config, cp_config, config, &err);
		cp_pipeline_config_free(config);
		TEST_ASSERT_SUCCESS(
			rc,
			"failed to fill the pipeline registry: %s",
			err ? yanet_error_message(err) : "?"
		);
		yanet_error_free(err);
	}

	struct cp_pipeline_config *config =
		cp_pipeline_config_create("grow", 0);
	TEST_ASSERT_NOT_NULL(config, "failed to build pipeline config");
	int rc = run_update_sweep(
		dp_config,
		cp_config,
		update_pipeline,
		config,
		"failed to upsert pipeline"
	);
	cp_pipeline_config_free(config);
	return rc;
}

// Run one scenario on a harness of its own: a sweep leaves its zone split
// into budget-sized blocks for good.
static int
run_on_fresh_zone(int (*scenario)(struct yanet_shm *shm)) {
	const char *port_names[] = {"01:00.0"};
	const char *devs_to_load[] = {"plain"};

	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 26,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices = port_names,
		.device_count = 1,
		.modules = NULL,
		.module_count = 0,
		.devices_to_load = devs_to_load,
		.devices_to_load_count = 1,
	};

	struct dataplane_ut *ut = dataplane_ut_new(&cfg);
	if (ut == NULL) {
		fprintf(stderr, "dataplane_ut_new failed\n");
		return TEST_FAILED;
	}

	struct yanet_shm *shm = dataplane_ut_shm(ut);
	if (shm == NULL) {
		fprintf(stderr, "dataplane_ut_shm returned NULL\n");
		dataplane_ut_free(ut);
		return TEST_FAILED;
	}

	int res = scenario(shm);

	dataplane_ut_free(ut);
	return res;
}

int
main(void) {
	log_enable_name("debug");

	int res = run_on_fresh_zone(run_function_upsert_refusal_test);
	if (res == TEST_SUCCESS) {
		res = run_on_fresh_zone(run_pipeline_upsert_refusal_test);
	}

	return (res == TEST_SUCCESS) ? 0 : 1;
}
