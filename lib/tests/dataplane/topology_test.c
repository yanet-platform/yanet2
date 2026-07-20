#include <stdlib.h>
#include <string.h>

#include "common/memory.h"
#include "common/memory_block.h"
#include "common/test_assert.h"

#include "lib/dataplane/config/topology.h"
#include "lib/dataplane/config/zone.h"
#include "lib/logging/log.h"

#define ARENA_SIZE (1 << 20)

// A single dp_config wired up with a fresh arena-backed memory context and
// one topology slot, ready for dp_topology_set_device_rss calls.
struct topology_fixture {
	void *arena;
	struct block_allocator block_allocator;
	struct dp_config dp_config;
};

static int
fixture_init(struct topology_fixture *fixture) {
	memset(&fixture->dp_config, 0, sizeof(fixture->dp_config));

	fixture->arena = malloc(ARENA_SIZE);
	TEST_ASSERT_NOT_NULL(fixture->arena, "failed to allocate arena");

	block_allocator_init(&fixture->block_allocator);
	block_allocator_put_arena(
		&fixture->block_allocator, fixture->arena, ARENA_SIZE
	);
	memory_context_init(
		&fixture->dp_config.memory_context,
		"topology_test",
		&fixture->block_allocator
	);

	TEST_ASSERT_NOT_NULL(
		dp_topology_alloc_devices(&fixture->dp_config, 1),
		"dp_topology_alloc_devices failed"
	);

	return TEST_SUCCESS;
}

static void
fixture_fini(struct topology_fixture *fixture) {
	free(fixture->arena);
}

// Verifies that a reta_size over DP_TOPOLOGY_RSS_RETA_SIZE_MAX is rejected
// outright, leaving rss_valid false so the device falls back to its
// non-RSS-aware path instead of a stack overflow in a consumer sized for
// exactly 512 slots.
static int
test_reject_reta_size_too_large(void) {
	struct topology_fixture fixture;
	TEST_ASSERT_SUCCESS(fixture_init(&fixture), "fixture init failed");

	uint8_t key[40] = {0};
	uint16_t reta[DP_TOPOLOGY_RSS_RETA_SIZE_MAX + 1] = {0};

	int rc = dp_topology_set_device_rss(
		&fixture.dp_config,
		0,
		key,
		sizeof(key),
		reta,
		DP_TOPOLOGY_RSS_RETA_SIZE_MAX + 1
	);
	TEST_ASSERT(rc != 0, "oversized reta_size must be rejected");

	struct dp_port *devices =
		ADDR_OF(&fixture.dp_config.dp_topology.devices);
	TEST_ASSERT(
		!devices[0].rss_valid,
		"rss_valid must stay false after rejection"
	);

	fixture_fini(&fixture);
	return TEST_SUCCESS;
}

// Verifies that a zero reta_size is rejected, since a zero-length table
// carries no usable RSS state.
static int
test_reject_reta_size_zero(void) {
	struct topology_fixture fixture;
	TEST_ASSERT_SUCCESS(fixture_init(&fixture), "fixture init failed");

	uint8_t key[40] = {0};
	uint16_t reta[1] = {0};

	int rc = dp_topology_set_device_rss(
		&fixture.dp_config, 0, key, sizeof(key), reta, 0
	);
	TEST_ASSERT(rc != 0, "zero reta_size must be rejected");

	struct dp_port *devices =
		ADDR_OF(&fixture.dp_config.dp_topology.devices);
	TEST_ASSERT(
		!devices[0].rss_valid,
		"rss_valid must stay false after rejection"
	);

	fixture_fini(&fixture);
	return TEST_SUCCESS;
}

// Verifies that a key shorter than DP_TOPOLOGY_RSS_KEY_LEN_MIN is rejected,
// since thash_toeplitz reads its first four bytes unconditionally.
static int
test_reject_key_len_too_short(void) {
	struct topology_fixture fixture;
	TEST_ASSERT_SUCCESS(fixture_init(&fixture), "fixture init failed");

	uint8_t key[3] = {0};
	uint16_t reta[128] = {0};

	int rc = dp_topology_set_device_rss(
		&fixture.dp_config,
		0,
		key,
		sizeof(key),
		reta,
		sizeof(reta) / sizeof(reta[0])
	);
	TEST_ASSERT(rc != 0, "undersized key_len must be rejected");

	struct dp_port *devices =
		ADDR_OF(&fixture.dp_config.dp_topology.devices);
	TEST_ASSERT(
		!devices[0].rss_valid,
		"rss_valid must stay false after rejection"
	);

	fixture_fini(&fixture);
	return TEST_SUCCESS;
}

// Verifies that a reta_size and key_len within contract are accepted and
// stored verbatim, with rss_valid set.
static int
test_accept_valid_rss_state(void) {
	struct topology_fixture fixture;
	TEST_ASSERT_SUCCESS(fixture_init(&fixture), "fixture init failed");

	uint8_t key[40];
	for (size_t idx = 0; idx < sizeof(key); ++idx) {
		key[idx] = (uint8_t)idx;
	}
	uint16_t reta[128];
	for (size_t idx = 0; idx < sizeof(reta) / sizeof(reta[0]); ++idx) {
		reta[idx] = (uint16_t)idx;
	}

	int rc = dp_topology_set_device_rss(
		&fixture.dp_config,
		0,
		key,
		sizeof(key),
		reta,
		sizeof(reta) / sizeof(reta[0])
	);
	TEST_ASSERT_EQUAL(rc, 0, "valid RSS state must be accepted");

	struct dp_port *devices =
		ADDR_OF(&fixture.dp_config.dp_topology.devices);
	TEST_ASSERT(devices[0].rss_valid, "rss_valid must be set on success");
	TEST_ASSERT_EQUAL(
		devices[0].rss_key_len, sizeof(key), "rss_key_len mismatch"
	);
	TEST_ASSERT_EQUAL(
		devices[0].rss_reta_size,
		sizeof(reta) / sizeof(reta[0]),
		"rss_reta_size mismatch"
	);

	uint8_t *stored_key = ADDR_OF(&devices[0].rss_key);
	TEST_ASSERT(
		memcmp(stored_key, key, sizeof(key)) == 0,
		"stored key must match input"
	);

	uint16_t *stored_reta = ADDR_OF(&devices[0].rss_reta);
	TEST_ASSERT(
		memcmp(stored_reta, reta, sizeof(reta)) == 0,
		"stored reta must match input"
	);

	fixture_fini(&fixture);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("info");

	LOG(INFO, "=== Starting Topology Test Suite ===");

	struct {
		const char *name;
		int (*fn)(void);
	} tests[] = {
		{"reject_reta_size_too_large", test_reject_reta_size_too_large},
		{"reject_reta_size_zero", test_reject_reta_size_zero},
		{"reject_key_len_too_short", test_reject_key_len_too_short},
		{"accept_valid_rss_state", test_accept_valid_rss_state},
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
		LOG(INFO, "=== All %zu topology tests passed! ===", total);
	} else {
		LOG(ERROR,
		    "=== %zu/%zu topology tests failed ===",
		    failed,
		    total);
	}

	return failed == 0 ? TEST_SUCCESS : TEST_FAILED;
}
