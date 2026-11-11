#include <stdlib.h>

#include "common/test_assert.h"

#include "lib/dataplane/config/topology.h"
#include "lib/dataplane/config/zone.h"
#include "lib/logging/log.h"
#include "lib/tests/dataplane/fixture.h"

#define ZONE_TEST_DEVICE_COUNT 3

// Verifies that the worker count and the RSS queue count each read back
// per device, apart from one another, and that a device_id at device_count
// reads zero for both.
static int
test_device_counts(void) {
	struct dataplane_test_fixture fixture;
	TEST_ASSERT_SUCCESS(
		dataplane_test_fixture_init(
			&fixture, "zone_test", ZONE_TEST_DEVICE_COUNT
		),
		"fixture init failed"
	);
	struct dp_config *cfg = &fixture.dp_config;

	static const uint64_t worker_counts[ZONE_TEST_DEVICE_COUNT] = {3, 2, 1};
	static const uint64_t rss_queue_counts[ZONE_TEST_DEVICE_COUNT] = {
		2, 1, 4
	};
	for (uint32_t device_id = 0; device_id < ZONE_TEST_DEVICE_COUNT;
	     ++device_id) {
		TEST_ASSERT_SUCCESS(
			dp_topology_set_device_queue_counts(
				cfg,
				device_id,
				worker_counts[device_id],
				rss_queue_counts[device_id]
			),
			"set failed for device %u",
			device_id
		);
	}

	for (uint32_t device_id = 0; device_id < ZONE_TEST_DEVICE_COUNT;
	     ++device_id) {
		TEST_ASSERT_EQUAL(
			dp_config_device_worker_count(cfg, device_id),
			worker_counts[device_id],
			"worker count mismatch for device %u",
			device_id
		);
		TEST_ASSERT_EQUAL(
			dp_config_device_rss_queue_count(cfg, device_id),
			rss_queue_counts[device_id],
			"RSS queue count mismatch for device %u",
			device_id
		);
	}

	TEST_ASSERT_EQUAL(
		dp_config_device_worker_count(cfg, ZONE_TEST_DEVICE_COUNT),
		0,
		"a device_id at device_count must read a zero worker count"
	);
	TEST_ASSERT_EQUAL(
		dp_config_device_rss_queue_count(cfg, ZONE_TEST_DEVICE_COUNT),
		0,
		"a device_id at device_count must read a zero RSS queue count"
	);

	dataplane_test_fixture_fini(&fixture);
	return TEST_SUCCESS;
}

// Verifies that setting the queue counts rejects a device_id out of range
// for dp_topology, and that an in-range device whose counts were never set
// reads back zero rather than garbage.
static int
test_set_device_counts_out_of_range(void) {
	struct dataplane_test_fixture fixture;
	TEST_ASSERT_SUCCESS(
		dataplane_test_fixture_init(&fixture, "zone_test", 2),
		"fixture init failed"
	);
	struct dp_config *cfg = &fixture.dp_config;

	TEST_ASSERT(
		dp_topology_set_device_queue_counts(cfg, 5, 1, 1) != 0,
		"set must reject an out-of-range device_id"
	);
	TEST_ASSERT_EQUAL(
		dp_config_device_worker_count(cfg, 1),
		0,
		"an in-range device whose count was never set must read zero"
	);
	TEST_ASSERT_EQUAL(
		dp_config_device_rss_queue_count(cfg, 1),
		0,
		"an in-range device whose RSS queue count was never set must "
		"read zero"
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
		{"device_counts", test_device_counts},
		{"set_device_counts_out_of_range",
		 test_set_device_counts_out_of_range},
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
