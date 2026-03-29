#include "common/memory.h"
#include "common/memory_block.h"
#include "common/network.h"
#include "common/registry.h"
#include "common/test_assert.h"
#include "filter/compiler.h"
#include "filter/filter.h"
#include "filter/query.h"

#include "filter/tests/helpers.h"
#include "lib/utils/packet.h"

#include "logging/log.h"
#include "rule.h"
#include <assert.h>
#include <netinet/in.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <stddef.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

////////////////////////////////////////////////////////////////////////////////

FILTER_COMPILER_DECLARE(
	combo_net6_port_src_compile, net6_fast_src, port_fast_src
);
FILTER_QUERY_DECLARE(combo_net6_port_src, net6_fast_src, port_fast_src);

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
		filter, combo_net6_port_src, packets, ranges, packets_count
	);

	TEST_ASSERT_SUCCESS(
		compare_expected_ranges(ranges, expected, packets_count),
		"got value ranges != expected"
	);

	free(ranges);

	return TEST_SUCCESS;
}

static void
prefix_mask(uint8_t mask[NET6_LEN], uint32_t prefix) {
	memset(mask, 0, NET6_LEN);
	for (uint32_t i = 0; i < prefix / 8; ++i) {
		mask[i] = 0xff;
	}
	if (prefix % 8 != 0) {
		mask[prefix / 8] = (uint8_t)(0xff << (8 - (prefix % 8)));
	}
}

////////////////////////////////////////////////////////////////////////////////

enum { arena_size = 1 << 28 };

// Test: IPv6 matches but port doesn't
static int
test_no_match_port_only(void *arena) {
	LOG(INFO, "=== Test No Match: Port Only (IPv6) ===");

	// Rule: src IP 2001:db8::/64, src port 80-90
	struct filter_rule_builder builder;
	builder_init(&builder);
	uint8_t mask[NET6_LEN];
	prefix_mask(mask, 64);
	struct net6 net;
	uint8_t addr[NET6_LEN] = {
		0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0
	};
	memcpy(net.addr, addr, NET6_LEN);
	memcpy(net.mask, mask, NET6_LEN);
	builder_add_net6_src(&builder, net);
	builder_add_port_src_range(&builder, 80, 90);
	struct filter_rule rule = build_rule(&builder, 1);

	// Test packets: IP matches but port doesn't
	const struct {
		uint8_t ip[NET6_LEN];
		uint16_t port;
	} test_cases[] = {
		{{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
		 79}, // Port one below
		{{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 100},
		 91}, // Port one above
		{{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 50},
		 100}, // Port way above
		{{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 200},
		 1}, // Port way below
	};
	const size_t test_count = sizeof(test_cases) / sizeof(test_cases[0]);

	struct packet *packets[test_count];
	uint8_t dst_ip[NET6_LEN] = {
		0xfd, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1
	};
	for (size_t i = 0; i < test_count; ++i) {
		packets[i] = malloc(sizeof(struct packet));
		int fill_result = fill_packet_net6(
			packets[i],
			test_cases[i].ip,
			dst_ip,
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
	res = FILTER_INIT(
		&filter, combo_net6_port_src_compile, &rule, 1, &mctx
	);
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

// Test: Port matches but IPv6 doesn't
static int
test_no_match_ip_only(void *arena) {
	LOG(INFO, "=== Test No Match: IP Only (IPv6) ===");

	// Rule: src IP 2001:db8::/64, src port 80-90
	struct filter_rule_builder builder;
	builder_init(&builder);
	uint8_t mask[NET6_LEN];
	prefix_mask(mask, 64);
	struct net6 net;
	uint8_t addr[NET6_LEN] = {
		0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0
	};
	memcpy(net.addr, addr, NET6_LEN);
	memcpy(net.mask, mask, NET6_LEN);
	builder_add_net6_src(&builder, net);
	builder_add_port_src_range(&builder, 80, 90);
	struct filter_rule rule = build_rule(&builder, 1);

	// Test packets: Port matches but IP doesn't
	const struct {
		uint8_t ip[NET6_LEN];
		uint16_t port;
	} test_cases[] = {
		{{0x20,
		  0x01,
		  0x0d,
		  0xb7,
		  0xff,
		  0xff,
		  0xff,
		  0xff,
		  0xff,
		  0xff,
		  0xff,
		  0xff,
		  0xff,
		  0xff,
		  0xff,
		  0xff},
		 85}, // IP one below
		{{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0},
		 85}, // IP one above
		{{0xfd, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}, 80
		}, // Different IP, port at start
		{{0x20, 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}, 90
		}, // Different IP, port at end
	};
	const size_t test_count = sizeof(test_cases) / sizeof(test_cases[0]);

	struct packet *packets[test_count];
	uint8_t dst_ip[NET6_LEN] = {
		0xfd, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1
	};
	for (size_t i = 0; i < test_count; ++i) {
		packets[i] = malloc(sizeof(struct packet));
		int fill_result = fill_packet_net6(
			packets[i],
			test_cases[i].ip,
			dst_ip,
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
	res = FILTER_INIT(
		&filter, combo_net6_port_src_compile, &rule, 1, &mctx
	);
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

// Test: Both IPv6 and port match
static int
test_both_match(void *arena) {
	LOG(INFO, "=== Test Both Match (IPv6) ===");

	// Rule: src IP 2001:db8::/64, src port 80-90
	struct filter_rule_builder builder;
	builder_init(&builder);
	uint8_t mask[NET6_LEN];
	prefix_mask(mask, 64);
	struct net6 net;
	uint8_t addr[NET6_LEN] = {
		0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0
	};
	memcpy(net.addr, addr, NET6_LEN);
	memcpy(net.mask, mask, NET6_LEN);
	builder_add_net6_src(&builder, net);
	builder_add_port_src_range(&builder, 80, 90);
	struct filter_rule rule = build_rule(&builder, 1);

	// Test packets: Both IP and port match
	const struct {
		uint8_t ip[NET6_LEN];
		uint16_t port;
	} test_cases[] = {
		{{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		 80}, // Both at start
		{{0x20,
		  0x01,
		  0x0d,
		  0xb8,
		  0,
		  0,
		  0,
		  0,
		  0xff,
		  0xff,
		  0xff,
		  0xff,
		  0xff,
		  0xff,
		  0xff,
		  0xff},
		 90}, // Both at end
		{{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0x80, 0, 0, 0, 0, 0, 0, 0
		 },
		 85}, // Both in middle
		{{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
		 89}, // Various combinations
	};
	const size_t test_count = sizeof(test_cases) / sizeof(test_cases[0]);

	struct packet *packets[test_count];
	uint8_t dst_ip[NET6_LEN] = {
		0xfd, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1
	};
	for (size_t i = 0; i < test_count; ++i) {
		packets[i] = malloc(sizeof(struct packet));
		int fill_result = fill_packet_net6(
			packets[i],
			test_cases[i].ip,
			dst_ip,
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
	res = FILTER_INIT(
		&filter, combo_net6_port_src_compile, &rule, 1, &mctx
	);
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

	free(arena);

	if (failed == 0) {
		LOG(INFO, "All %zu tests passed", tests);
	} else {
		LOG(ERROR, "%zu/%zu tests failed", failed, tests);
	}

	return (failed == 0 ? 0 : 1);
}
