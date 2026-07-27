#include <stdlib.h>

#include "common/test_assert.h"

#include "lib/dataplane/config/topology.h"
#include "lib/dataplane/config/zone.h"
#include "lib/logging/log.h"
#include "lib/tests/dataplane/fixture.h"

#define ZONE_TEST_DEVICE_COUNT 3

// Verifies that dp_topology_set_device_worker_count writes a distinct count
// per device, dp_config_device_worker_count reads each back, and a
// device_id at device_count reads zero.
static int
test_worker_counts(void) {
	struct dataplane_test_fixture fixture;
	TEST_ASSERT_SUCCESS(
		dataplane_test_fixture_init(
			&fixture, "zone_test", ZONE_TEST_DEVICE_COUNT
		),
		"fixture init failed"
	);
	struct dp_config *cfg = &fixture.dp_config;

	static const uint64_t expected_counts[ZONE_TEST_DEVICE_COUNT] = {
		3, 2, 1
	};
	for (uint32_t device_id = 0; device_id < ZONE_TEST_DEVICE_COUNT;
	     ++device_id) {
		TEST_ASSERT_SUCCESS(
			dp_topology_set_device_worker_count(
				cfg, device_id, expected_counts[device_id]
			),
			"set failed for device %u",
			device_id
		);
	}

	for (uint32_t device_id = 0; device_id < ZONE_TEST_DEVICE_COUNT;
	     ++device_id) {
		TEST_ASSERT_EQUAL(
			dp_config_device_worker_count(cfg, device_id),
			expected_counts[device_id],
			"count mismatch for device %u",
			device_id
		);
	}

	TEST_ASSERT_EQUAL(
		dp_config_device_worker_count(cfg, ZONE_TEST_DEVICE_COUNT),
		0,
		"a device_id at device_count must read zero"
	);

	dataplane_test_fixture_fini(&fixture);
	return TEST_SUCCESS;
}

// Verifies that dp_topology_set_device_worker_count rejects a device_id out
// of range for dp_topology, and that an in-range device whose count was
// never set reads back zero rather than garbage.
static int
test_set_worker_count_out_of_range(void) {
	struct dataplane_test_fixture fixture;
	TEST_ASSERT_SUCCESS(
		dataplane_test_fixture_init(&fixture, "zone_test", 2),
		"fixture init failed"
	);
	struct dp_config *cfg = &fixture.dp_config;

	TEST_ASSERT(
		dp_topology_set_device_worker_count(cfg, 5, 1) != 0,
		"set must reject an out-of-range device_id"
	);
	TEST_ASSERT_EQUAL(
		dp_config_device_worker_count(cfg, 1),
		0,
		"an in-range device whose count was never set must read zero"
	);

	dataplane_test_fixture_fini(&fixture);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("info");

	LOG(INFO, "=== Starting Zone Test Suite ===");

	struct {
		const char *name;
		int (*fn)(void);
	} tests[] = {
		{"worker_counts", test_worker_counts},
		{"set_worker_count_out_of_range",
		 test_set_worker_count_out_of_range},
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
		LOG(INFO, "=== All %zu zone tests passed! ===", total);
	} else {
		LOG(ERROR, "=== %zu/%zu zone tests failed ===", failed, total);
	}

	return failed == 0 ? TEST_SUCCESS : TEST_FAILED;
}
