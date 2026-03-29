#include "common/memory.h"
#include "common/memory_block.h"
#include "common/registry.h"
#include "common/test_assert.h"
#include "filter/compiler.h"
#include "filter/filter.h"
#include "filter/query.h"

#include "filter/tests/helpers.h"
#include "lib/utils/packet.h"

#include "logging/log.h"
#include "rte_byteorder.h"
#include "rule.h"
#include <assert.h>
#include <netinet/in.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <stddef.h>
#include <stdlib.h>
#include <time.h>

////////////////////////////////////////////////////////////////////////////////

FILTER_COMPILER_DECLARE(combo_net4_port_src, net4_fast_src, port_fast_src);
FILTER_QUERY_DECLARE(combo_net4_port_src, net4_fast_src, port_fast_src);

////////////////////////////////////////////////////////////////////////////////

static int
query_and_expect_actions(
	struct filter *filter,
	struct packet **packets,
	size_t packets_count,
	struct value_range **expected
) {
	struct value_range **ranges =
		malloc(sizeof(struct value_range *) * packets_count);

	filter_query(
		filter, combo_net4_port_src, packets, ranges, packets_count
	);

	TEST_ASSERT_SUCCESS(
		compare_expected_ranges(ranges, expected, packets_count),
		"got value ranges != expected"
	);

	free(ranges);

	return TEST_SUCCESS;
}

static uint32_t
prefix_mask(uint32_t prefix) {
	uint32_t mask = (uint32_t)(-1) ^ ((1 << (32 - prefix)) - 1);
	return rte_cpu_to_be_32(mask);
}

////////////////////////////////////////////////////////////////////////////////

enum { arena_size = 1 << 28 };

// Test: IP matches but port doesn't
static int
test_no_match_port_only(void *arena) {
	LOG(INFO, "=== Test No Match: Port Only ===");

	// Rule: src IP 10.0.0.0/24, src port 80-90
	struct filter_rule_builder builder;
	builder_init(&builder);
	uint32_t mask = prefix_mask(24);
	builder_add_net4_src(&builder, ip(10, 0, 0, 0), (const uint8_t *)&mask);
	builder_add_port_src_range(&builder, 80, 90);
	struct filter_rule rule = build_rule(&builder, 1);

	// Test packets: IP matches but port doesn't
	const struct {
		uint8_t ip[4];
		uint16_t port;
	} test_cases[] = {
		{{10, 0, 0, 1}, 79},   // Port one below range
		{{10, 0, 0, 100}, 91}, // Port one above range
		{{10, 0, 0, 50}, 100}, // Port way above
		{{10, 0, 0, 200}, 1},  // Port way below
	};
	const size_t test_count = sizeof(test_cases) / sizeof(test_cases[0]);

	struct packet *packets[test_count];
	for (size_t i = 0; i < test_count; ++i) {
		packets[i] = malloc(sizeof(struct packet));
		int fill_result = fill_packet_net4(
			packets[i],
			test_cases[i].ip,
			ip(192, 168, 1, 1),
			test_cases[i].port,
			80,
			IPPROTO_TCP,
			0
		);
		TEST_ASSERT_EQUAL(
			fill_result, 0, "failed to fill packet at index %zu", i
		);
	}

	// Expected: no matches
	struct value_range *expected_ranges[test_count];
	for (size_t i = 0; i < test_count; ++i) {
		expected_ranges[i] = malloc(sizeof(struct value_range));
		expected_ranges[i]->count = 0;
		expected_ranges[i]->values = malloc(sizeof(uint32_t));
	}

	struct block_allocator alloc;
	int res = block_allocator_init(&alloc);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize block allocator");
	block_allocator_put_arena(&alloc, arena, arena_size);

	struct memory_context mctx;
	res = memory_context_init(&mctx, "test", &alloc);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize memory context");

	struct filter filter;
	res = FILTER_INIT(&filter, combo_net4_port_src, &rule, 1, &mctx);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize filter");

	res = query_and_expect_actions(
		&filter, packets, test_count, expected_ranges
	);
	TEST_ASSERT_SUCCESS(res, "some checks failed");

	for (size_t i = 0; i < test_count; ++i) {
		free(expected_ranges[i]->values);
		free(expected_ranges[i]);
		free_packet(packets[i]);
		free(packets[i]);
	}

	return TEST_SUCCESS;
}

// Test: Port matches but IP doesn't
static int
test_no_match_ip_only(void *arena) {
	LOG(INFO, "=== Test No Match: IP Only ===");

	// Rule: src IP 10.0.0.0/24, src port 80-90
	struct filter_rule_builder builder;
	builder_init(&builder);
	uint32_t mask = prefix_mask(24);
	builder_add_net4_src(&builder, ip(10, 0, 0, 0), (const uint8_t *)&mask);
	builder_add_port_src_range(&builder, 80, 90);
	struct filter_rule rule = build_rule(&builder, 1);

	// Test packets: Port matches but IP doesn't
	const struct {
		uint8_t ip[4];
		uint16_t port;
	} test_cases[] = {
		{{9, 255, 255, 255}, 85}, // IP one below range
		{{10, 0, 1, 0}, 85},	  // IP one above range
		{{192, 168, 1, 1}, 80},	  // Different IP, port at start
		{{172, 16, 0, 1}, 90},	  // Different IP, port at end
	};
	const size_t test_count = sizeof(test_cases) / sizeof(test_cases[0]);

	struct packet *packets[test_count];
	for (size_t i = 0; i < test_count; ++i) {
		packets[i] = malloc(sizeof(struct packet));
		int fill_result = fill_packet_net4(
			packets[i],
			test_cases[i].ip,
			ip(192, 168, 1, 1),
			test_cases[i].port,
			80,
			IPPROTO_TCP,
			0
		);
		TEST_ASSERT_EQUAL(
			fill_result, 0, "failed to fill packet at index %zu", i
		);
	}

	// Expected: no matches
	struct value_range *expected_ranges[test_count];
	for (size_t i = 0; i < test_count; ++i) {
		expected_ranges[i] = malloc(sizeof(struct value_range));
		expected_ranges[i]->count = 0;
		expected_ranges[i]->values = malloc(sizeof(uint32_t));
	}

	struct block_allocator alloc;
	int res = block_allocator_init(&alloc);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize block allocator");
	block_allocator_put_arena(&alloc, arena, arena_size);

	struct memory_context mctx;
	res = memory_context_init(&mctx, "test", &alloc);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize memory context");

	struct filter filter;
	res = FILTER_INIT(&filter, combo_net4_port_src, &rule, 1, &mctx);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize filter");

	res = query_and_expect_actions(
		&filter, packets, test_count, expected_ranges
	);
	TEST_ASSERT_SUCCESS(res, "some checks failed");

	for (size_t i = 0; i < test_count; ++i) {
		free(expected_ranges[i]->values);
		free(expected_ranges[i]);
		free_packet(packets[i]);
		free(packets[i]);
	}

	return TEST_SUCCESS;
}

// Test: Both IP and port match
static int
test_both_match(void *arena) {
	LOG(INFO, "=== Test Both Match ===");

	// Rule: src IP 10.0.0.0/24, src port 80-90
	struct filter_rule_builder builder;
	builder_init(&builder);
	uint32_t mask = prefix_mask(24);
	builder_add_net4_src(&builder, ip(10, 0, 0, 0), (const uint8_t *)&mask);
	builder_add_port_src_range(&builder, 80, 90);
	struct filter_rule rule = build_rule(&builder, 1);

	// Test packets: Both IP and port match
	const struct {
		uint8_t ip[4];
		uint16_t port;
	} test_cases[] = {
		{{10, 0, 0, 0}, 80},   // Both at start
		{{10, 0, 0, 255}, 90}, // Both at end
		{{10, 0, 0, 128}, 85}, // Both in middle
		{{10, 0, 0, 1}, 89},   // Various combinations
	};
	const size_t test_count = sizeof(test_cases) / sizeof(test_cases[0]);

	struct packet *packets[test_count];
	for (size_t i = 0; i < test_count; ++i) {
		packets[i] = malloc(sizeof(struct packet));
		int fill_result = fill_packet_net4(
			packets[i],
			test_cases[i].ip,
			ip(192, 168, 1, 1),
			test_cases[i].port,
			80,
			IPPROTO_TCP,
			0
		);
		TEST_ASSERT_EQUAL(
			fill_result, 0, "failed to fill packet at index %zu", i
		);
	}

	// Expected: all match
	struct value_range *expected_ranges[test_count];
	for (size_t i = 0; i < test_count; ++i) {
		expected_ranges[i] = malloc(sizeof(struct value_range));
		expected_ranges[i]->count = 1;
		expected_ranges[i]->values = malloc(sizeof(uint32_t) * 2);
		expected_ranges[i]->values[0] = 1;
	}

	struct block_allocator alloc;
	int res = block_allocator_init(&alloc);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize block allocator");
	block_allocator_put_arena(&alloc, arena, arena_size);

	struct memory_context mctx;
	res = memory_context_init(&mctx, "test", &alloc);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize memory context");

	struct filter filter;
	res = FILTER_INIT(&filter, combo_net4_port_src, &rule, 1, &mctx);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize filter");

	res = query_and_expect_actions(
		&filter, packets, test_count, expected_ranges
	);
	TEST_ASSERT_SUCCESS(res, "some checks failed");

	for (size_t i = 0; i < test_count; ++i) {
		free(expected_ranges[i]->values);
		free(expected_ranges[i]);
		free_packet(packets[i]);
		free(packets[i]);
	}

	return TEST_SUCCESS;
}

// Test: Overlapping networks and port ranges
static int
test_overlapping(void *arena) {
	LOG(INFO, "=== Test Overlapping Networks and Ports ===");

	// Rule 1: 10.0.0.0/8, ports 80-100
	// Rule 2: 10.1.0.0/16, ports 90-110
	// Rule 3: 10.1.1.0/24, ports 95-105
	struct filter_rule_builder builders[3];
	struct filter_rule rules[3];

	builder_init(&builders[0]);
	uint32_t mask1 = prefix_mask(8);
	builder_add_net4_src(
		&builders[0], ip(10, 0, 0, 0), (const uint8_t *)&mask1
	);
	builder_add_port_src_range(&builders[0], 80, 100);
	rules[0] = build_rule(&builders[0], 1);

	builder_init(&builders[1]);
	uint32_t mask2 = prefix_mask(16);
	builder_add_net4_src(
		&builders[1], ip(10, 1, 0, 0), (const uint8_t *)&mask2
	);
	builder_add_port_src_range(&builders[1], 90, 110);
	rules[1] = build_rule(&builders[1], 2);

	builder_init(&builders[2]);
	uint32_t mask3 = prefix_mask(24);
	builder_add_net4_src(
		&builders[2], ip(10, 1, 1, 0), (const uint8_t *)&mask3
	);
	builder_add_port_src_range(&builders[2], 95, 105);
	rules[2] = build_rule(&builders[2], 3);

	// Test packets
	const struct {
		uint8_t ip[4];
		uint16_t port;
		uint32_t expected_actions[3];
		size_t expected_count;
	} test_cases[] = {
		{{10, 2, 0, 1}, 85, {1, 0, 0}, 1}, // Only rule 1 matches
		{{10, 1, 1, 100}, 79, {0, 0, 0}, 0
		}, // IP matches all but port matches none
		{{10, 1, 1, 100}, 106, {2, 0, 0}, 1
		}, // Only rule 2 matches (port 106 in 90-110, not in 80-100 or
		   // 95-105)
	};
	const size_t test_count = sizeof(test_cases) / sizeof(test_cases[0]);

	struct packet *packets[test_count];
	for (size_t i = 0; i < test_count; ++i) {
		packets[i] = malloc(sizeof(struct packet));
		int fill_result = fill_packet_net4(
			packets[i],
			test_cases[i].ip,
			ip(192, 168, 1, 1),
			test_cases[i].port,
			80,
			IPPROTO_TCP,
			0
		);
		TEST_ASSERT_EQUAL(
			fill_result, 0, "failed to fill packet at index %zu", i
		);
	}

	struct value_range *expected_ranges[test_count];
	for (size_t i = 0; i < test_count; ++i) {
		expected_ranges[i] = malloc(sizeof(struct value_range));
		expected_ranges[i]->count = test_cases[i].expected_count;
		expected_ranges[i]->values = malloc(sizeof(uint32_t) * 4);
		for (size_t j = 0; j < test_cases[i].expected_count; ++j) {
			expected_ranges[i]->values[j] =
				test_cases[i].expected_actions[j];
		}
	}

	struct block_allocator alloc;
	int res = block_allocator_init(&alloc);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize block allocator");
	block_allocator_put_arena(&alloc, arena, arena_size);

	struct memory_context mctx;
	res = memory_context_init(&mctx, "test", &alloc);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize memory context");

	struct filter filter;
	res = FILTER_INIT(&filter, combo_net4_port_src, rules, 3, &mctx);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize filter");

	res = query_and_expect_actions(
		&filter, packets, test_count, expected_ranges
	);
	TEST_ASSERT_SUCCESS(res, "some checks failed");

	for (size_t i = 0; i < test_count; ++i) {
		free(expected_ranges[i]->values);
		free(expected_ranges[i]);
		free_packet(packets[i]);
		free(packets[i]);
	}

	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////

int
main() {
	log_enable_name("debug");

	size_t tests = 0;
	size_t failed = 0;

	void *arena = malloc(arena_size);

	++tests;
	if (test_no_match_port_only(arena) != 0) {
		LOG(ERROR, "test_no_match_port_only failed");
		++failed;
	}

	++tests;
	if (test_no_match_ip_only(arena) != 0) {
		LOG(ERROR, "test_no_match_ip_only failed");
		++failed;
	}

	++tests;
	if (test_both_match(arena) != 0) {
		LOG(ERROR, "test_both_match failed");
		++failed;
	}

	++tests;
	if (test_overlapping(arena) != 0) {
		LOG(ERROR, "test_overlapping failed");
		++failed;
	}

	free(arena);

	if (failed == 0) {
		LOG(INFO, "All %zu tests passed", tests);
	} else {
		LOG(ERROR, "%zu/%zu tests failed", failed, tests);
	}

	return (failed == 0 ? 0 : 1);
}
