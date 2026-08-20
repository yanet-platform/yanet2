// verifies that worker counter bindings follow registered names, not
// registration order.
//
// The dataplane and the control plane address the standard worker counters
// through one registry: the dataplane binds per-worker address pointers
// once at startup, while the control plane resolves values by name. A
// counter inserted or reordered in the standard registration sequence used
// to shift every later slot silently while every name still resolved, so
// an operator saw one counter's numbers under another counter's name. The
// scenarios pin the contract from both sides: the identifiers captured at
// registration must resolve back to their names, and a binding made after
// a foreign earlier registration must still address exactly the slots the
// names resolve to.

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "common/memory.h"
#include "common/memory_block.h"
#include "common/test_assert.h"
#include "lib/counters/counters.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/worker/counters.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"

#define ARENA_SIZE (64U * 1024U)

struct counters_fixture {
	struct block_allocator allocator;
	void *arena;
	struct dp_config *dp_config;
};

// Builds an unlinked registry inside a heap dp_config, with the standard
// counter registration sequence not yet run.
static int
counters_fixture_init(struct counters_fixture *fixture) {
	memset(fixture, 0, sizeof(*fixture));
	fixture->arena =
		aligned_alloc(MEMORY_BLOCK_ALLOCATOR_MAX_ALIGN, ARENA_SIZE);
	TEST_ASSERT_NOT_NULL(fixture->arena, "failed to allocate arena");

	block_allocator_init(&fixture->allocator);
	block_allocator_put_arena(
		&fixture->allocator, fixture->arena, ARENA_SIZE
	);

	fixture->dp_config = calloc(1, sizeof(struct dp_config));
	TEST_ASSERT_NOT_NULL(
		fixture->dp_config, "failed to allocate dp_config"
	);
	memory_context_init(
		&fixture->dp_config->memory_context,
		"worker-counters-test",
		&fixture->allocator
	);

	TEST_ASSERT_SUCCESS(
		counter_registry_init(
			&fixture->dp_config->worker_counters,
			&fixture->dp_config->memory_context,
			0
		),
		"failed to init counter registry"
	);
	return TEST_SUCCESS;
}

// Releases the fixture and its arena.
static void
counters_fixture_fini(struct counters_fixture *fixture) {
	counter_registry_fini(&fixture->dp_config->worker_counters);
	free(fixture->dp_config);
	block_allocator_fini(&fixture->allocator);
	free(fixture->arena);
}

static int
run_registered_ids_resolve_to_names_test(void) {
	struct counters_fixture fixture;
	TEST_ASSERT_SUCCESS(
		counters_fixture_init(&fixture), "fixture init failed"
	);

	struct worker_counters counters;
	TEST_ASSERT_SUCCESS(
		worker_counters_register(fixture.dp_config, &counters),
		"failed to register standard worker counters"
	);

	const struct {
		const char *name;
		uint64_t id;
	} cases[] = {
		{"iterations", counters.iterations},
		{"rx", counters.rx},
		{"tx", counters.tx},
		{"remote_rx", counters.remote_rx},
		{"remote_tx", counters.remote_tx},
		{"rx_bursts", counters.rx_bursts},
		{"local_tx_drops", counters.local_tx_drops},
		{"remote_tx_drops", counters.remote_tx_drops},
		{"drops", counters.drops},
	};

	int failed = 0;
	for (size_t idx = 0; idx < sizeof(cases) / sizeof(cases[0]); ++idx) {
		uint64_t resolved = counter_registry_lookup_index(
			&fixture.dp_config->worker_counters, cases[idx].name
		);
		if (resolved != cases[idx].id) {
			LOG(ERROR,
			    "'%s' resolves to id %lu, registration captured "
			    "%lu",
			    cases[idx].name,
			    (unsigned long)resolved,
			    (unsigned long)cases[idx].id);
			failed = 1;
		}
	}

	counters_fixture_fini(&fixture);
	TEST_ASSERT(failed == 0, "a captured id does not resolve to its name");
	return TEST_SUCCESS;
}

static int
run_binding_survives_earlier_registration_test(void) {
	struct counters_fixture fixture;
	TEST_ASSERT_SUCCESS(
		counters_fixture_init(&fixture), "fixture init failed"
	);

	// A foreign counter registered before the standard sequence plays the
	// role of an insertion at the head: every standard slot shifts by one
	// while the names stay resolvable.
	yanet_error *err = NULL;
	uint64_t probe_id = counter_registry_register(
		&fixture.dp_config->worker_counters, "probe", 1, &err
	);
	TEST_ASSERT(
		probe_id != COUNTER_INVALID, "failed to register probe counter"
	);
	yanet_error_free(err);

	struct worker_counters counters;
	TEST_ASSERT_SUCCESS(
		worker_counters_register(fixture.dp_config, &counters),
		"failed to register standard worker counters"
	);
	TEST_ASSERT(
		probe_id != counters.iterations,
		"probe did not shift the standard slots; scenario is vacuous"
	);

	TEST_ASSERT_SUCCESS(
		counter_registry_link(
			&fixture.dp_config->worker_counters, NULL, &err
		),
		"failed to link counter registry"
	);
	yanet_error_free(err);

	struct counter_storage *storage = counter_storage_spawn(
		&fixture.dp_config->memory_context,
		NULL,
		&fixture.dp_config->worker_counters
	);
	TEST_ASSERT_NOT_NULL(storage, "failed to spawn counter storage");

	struct dp_worker dp_worker;
	memset(&dp_worker, 0, sizeof(dp_worker));
	dp_worker.counters = counters;
	worker_counters_bind(&dp_worker, storage);

	struct counter_registry *registry = &fixture.dp_config->worker_counters;
	uint64_t id_rx = counter_registry_lookup_index(registry, "rx");
	uint64_t id_tx = counter_registry_lookup_index(registry, "tx");

	int failed = 0;
	const struct {
		const char *what;
		uint64_t *bound;
		const char *name;
		uint64_t subslot;
	} cases[] = {
		{"iterations", dp_worker.iterations, "iterations", 0},
		{"rx packets", dp_worker.rx_count, "rx", 0},
		{"rx bytes", dp_worker.rx_size, "rx", 1},
		{"tx packets", dp_worker.tx_count, "tx", 0},
		{"tx bytes", dp_worker.tx_size, "tx", 1},
		{"remote rx", dp_worker.remote_rx_count, "remote_rx", 0},
		{"remote tx", dp_worker.remote_tx_count, "remote_tx", 0},
		{"rx bursts", dp_worker.rx_bursts, "rx_bursts", 0},
		{"local tx drops",
		 dp_worker.local_tx_drops,
		 "local_tx_drops",
		 0},
		{"remote tx drops",
		 dp_worker.remote_tx_drops,
		 "remote_tx_drops",
		 0},
		{"drops", dp_worker.drop_count, "drops", 0},
	};

	for (size_t idx = 0; idx < sizeof(cases) / sizeof(cases[0]); ++idx) {
		uint64_t id = counter_registry_lookup_index(
			registry, cases[idx].name
		);
		uint64_t *expected =
			counter_get_address(id, storage) + cases[idx].subslot;
		if (cases[idx].bound != expected) {
			LOG(ERROR,
			    "'%s' bound to a slot other than '%s' subslot %lu",
			    cases[idx].what,
			    cases[idx].name,
			    (unsigned long)cases[idx].subslot);
			failed = 1;
		}
	}

	// The packet and byte sub-slots must stay distinct addresses so a
	// write through one never lands in the other.
	if (dp_worker.rx_count == dp_worker.rx_size ||
	    dp_worker.tx_count == dp_worker.tx_size || id_rx == id_tx) {
		LOG(ERROR, "packet and byte sub-slots collapse to one address");
		failed = 1;
	}

	counter_storage_free(storage);
	counters_fixture_fini(&fixture);
	TEST_ASSERT(failed == 0, "a bound pointer misses its named slot");
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("info");

	struct {
		const char *name;
		int (*fn)(void);
	} tests[] = {
		{"registered_ids_resolve_to_names",
		 run_registered_ids_resolve_to_names_test},
		{"binding_survives_earlier_registration",
		 run_binding_survives_earlier_registration_test},
	};

	size_t total = sizeof(tests) / sizeof(tests[0]);
	size_t failed = 0;
	for (size_t idx = 0; idx < total; ++idx) {
		LOG(INFO,
		    "[%zu/%zu] running %s...",
		    idx + 1,
		    total,
		    tests[idx].name);
		if (tests[idx].fn() != TEST_SUCCESS) {
			LOG(ERROR, "%s FAILED", tests[idx].name);
			failed++;
		} else {
			LOG(INFO, "%s passed", tests[idx].name);
		}
	}

	if (failed == 0) {
		LOG(INFO, "all %zu worker_counters tests passed", total);
	} else {
		LOG(ERROR,
		    "%zu/%zu worker_counters tests failed",
		    failed,
		    total);
	}

	return failed == 0 ? TEST_SUCCESS : TEST_FAILED;
}
