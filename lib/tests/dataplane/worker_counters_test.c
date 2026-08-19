#include <assert.h>
#include <stdlib.h>

#include "common/memory_address.h"
#include "common/test_assert.h"

#include "lib/counters/counters.h"
#include "lib/dataplane/config/counter_storage.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/worker/counters.h"
#include "lib/logging/log.h"
#include "lib/tests/dataplane/fixture.h"

static uint64_t **
field_pointer(struct dp_worker *dp_worker, size_t idx) {
	return (uint64_t **)((char *)dp_worker +
			     worker_counter_fields[idx].offset);
}

// Registers the standard counters with the entry at the given position
// overridden, then spawns storage for one worker. A NULL name or a zero size
// keeps what the specification declares.
static int
setup_registry(
	struct dataplane_test_fixture *fixture,
	const char *fixture_name,
	size_t position,
	const char *name,
	uint64_t size
) {
	TEST_ASSERT_SUCCESS(
		dataplane_test_fixture_init(fixture, fixture_name, 1),
		"fixture init failed"
	);

	struct dp_config *cfg = &fixture->dp_config;
	TEST_ASSERT_SUCCESS(
		counter_registry_init(
			&cfg->worker_counters, &cfg->memory_context, 0
		),
		"registry init failed"
	);

	for (size_t idx = 0; idx < worker_counter_spec_count; ++idx) {
		const struct worker_counter_spec *spec =
			worker_counter_specs + idx;
		const char *counter_name = spec->name;
		uint64_t counter_size = spec->size;
		if (idx == position) {
			if (name != NULL) {
				counter_name = name;
			}
			if (size != 0) {
				counter_size = size;
			}
		}

		yanet_error *err = NULL;
		TEST_ASSERT(
			counter_registry_register(
				&cfg->worker_counters,
				counter_name,
				counter_size,
				&err
			) != COUNTER_INVALID,
			"failed to register counter '%s'",
			counter_name
		);
	}

	TEST_ASSERT_SUCCESS(
		dp_counter_storage_init(cfg, NULL, 1),
		"counter storage init failed"
	);

	return TEST_SUCCESS;
}

// Verifies that registering the standard counters and binding a worker points
// every field at the value its own counter name and value index resolve to.
static int
test_bind_resolves_every_field_by_name(void) {
	struct dataplane_test_fixture fixture;
	TEST_ASSERT_SUCCESS(
		dataplane_test_fixture_init(
			&fixture, "worker_counters_bind", 1
		),
		"fixture init failed"
	);
	struct dp_config *cfg = &fixture.dp_config;

	TEST_ASSERT_SUCCESS(
		counter_registry_init(
			&cfg->worker_counters, &cfg->memory_context, 0
		),
		"registry init failed"
	);

	TEST_ASSERT_SUCCESS(
		worker_counters_register(cfg), "registration failed"
	);
	TEST_ASSERT_SUCCESS(
		dp_counter_storage_init(cfg, NULL, 1),
		"counter storage init failed"
	);

	struct dp_worker dp_worker;
	memset(&dp_worker, 0, sizeof(dp_worker));
	dp_worker.idx = 0;

	TEST_ASSERT_SUCCESS(
		worker_counters_bind(&dp_worker, cfg), "bind failed"
	);

	struct counter_storage *storage = ADDR_OF_NONNULL(
		ADDR_OF_NONNULL(&cfg->worker_counter_storages) + dp_worker.idx
	);

	for (size_t idx = 0; idx < worker_counter_field_count; ++idx) {
		const struct worker_counter_field *field =
			worker_counter_fields + idx;

		uint64_t counter_idx = counter_registry_lookup_index(
			&cfg->worker_counters, field->counter
		);
		TEST_ASSERT(
			counter_idx != COUNTER_INVALID,
			"counter '%s' is missing from the registry",
			field->counter
		);

		TEST_ASSERT_EQUAL(
			(uintptr_t)*field_pointer(&dp_worker, idx),
			(uintptr_t)(counter_get_address(counter_idx, storage) +
				    field->value_idx),
			"field bound to counter '%s' value %lu holds a foreign "
			"address",
			field->counter,
			field->value_idx
		);
	}

	dataplane_test_fixture_fini(&fixture);
	return TEST_SUCCESS;
}

// Verifies that binding rejects a registry that does not match the
// specification and leaves every field as it was instead of half wiring the
// worker.
static int
test_bind_rejects_mismatched_registration(void) {
	static struct {
		const char *description;
		size_t position;
		const char *name;
		uint64_t size;
	} cases[] = {
		{"counter registered under a different name", 4, "renamed", 0},
		{"counter registered with too few values", 1, NULL, 1},
	};

	for (size_t case_idx = 0; case_idx < sizeof(cases) / sizeof(cases[0]);
	     ++case_idx) {
		struct dataplane_test_fixture fixture;
		TEST_ASSERT_SUCCESS(
			setup_registry(
				&fixture,
				"worker_counters_mismatch",
				cases[case_idx].position,
				cases[case_idx].name,
				cases[case_idx].size
			),
			"setup failed for %s",
			cases[case_idx].description
		);

		struct dp_worker dp_worker;
		memset(&dp_worker, 0, sizeof(dp_worker));
		dp_worker.idx = 0;
		uint64_t sentinel = 0;
		for (size_t idx = 0; idx < worker_counter_field_count; ++idx) {
			*field_pointer(&dp_worker, idx) = &sentinel;
		}

		TEST_ASSERT(
			worker_counters_bind(&dp_worker, &fixture.dp_config) !=
				0,
			"bind must fail for %s",
			cases[case_idx].description
		);

		for (size_t idx = 0; idx < worker_counter_field_count; ++idx) {
			TEST_ASSERT_EQUAL(
				(uintptr_t)*field_pointer(&dp_worker, idx),
				(uintptr_t)&sentinel,
				"field bound to counter '%s' must be left "
				"unchanged for %s",
				worker_counter_fields[idx].counter,
				cases[case_idx].description
			);
		}

		dataplane_test_fixture_fini(&fixture);
	}

	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("info");

	LOG(INFO, "=== Starting Worker Counters Test Suite ===");

	struct {
		const char *name;
		int (*fn)(void);
	} tests[] = {
		{"bind_resolves_every_field_by_name",
		 test_bind_resolves_every_field_by_name},
		{"bind_rejects_mismatched_registration",
		 test_bind_rejects_mismatched_registration},
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
			++failed;
		} else {
			LOG(INFO, "%s passed", tests[idx].name);
		}
	}

	if (failed == 0) {
		LOG(INFO, "=== All %zu worker counters tests passed! ===", total
		);
	} else {
		LOG(ERROR,
		    "=== %zu/%zu worker counters tests failed ===",
		    failed,
		    total);
	}

	return failed == 0 ? TEST_SUCCESS : TEST_FAILED;
}
