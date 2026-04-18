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
	combo_net4_port_proto_dst_compile, net4_fast_dst, port_fast_dst, proto
);
FILTER_QUERY_DECLARE(
	combo_net4_port_proto_dst, net4_fast_dst, port_fast_dst, proto
);

////////////////////////////////////////////////////////////////////////////////

enum { arena_size = 1 << 28 };

// Test: IP and port match but protocol doesn't
static int
test_no_match_proto_only(void *arena) {
	LOG(INFO, "=== Test No Match: Protocol Only ===");

	// Rule: dst IP 10.0.0.0/24, dst port 80-90, TCP only
	struct filter_rule_builder builder;
	builder_init(&builder);
	builder_add_net4_dst(&builder, ip(10, 0, 0, 0), ip(255, 255, 255, 0));
	builder_add_port_dst_range(&builder, 80, 90);
	builder_set_proto(&builder, IPPROTO_TCP, 0, 0);
	struct filter_rule rule = build_rule(&builder);

	// Test packets: IP and port match but protocol doesn't
	const struct {
		uint8_t ip[NET4_LEN];
		uint16_t port;
		uint8_t proto;
	} test_cases[] = {
		{{10, 0, 0, 1}, 85, IPPROTO_UDP},    // UDP instead of TCP
		{{10, 0, 0, 80}, 80, IPPROTO_ICMP},  // ICMP instead of TCP
		{{10, 0, 0, 255}, 90, IPPROTO_SCTP}, // SCTP instead of TCP
		{{10, 0, 0, 128}, 85, IPPROTO_GRE},  // GRE instead of TCP
	};
	const size_t test_count = sizeof(test_cases) / sizeof(test_cases[0]);

	struct packet *packets[test_count];
	for (size_t i = 0; i < test_count; ++i) {
		packets[i] = malloc(sizeof(struct packet));
		int fill_result = fill_packet_net4(
			packets[i],
			ip(192, 168, 0, 1),
			test_cases[i].ip,
			8080,
			test_cases[i].port,
			test_cases[i].proto,
			0
		);
		TEST_ASSERT_EQUAL(
			fill_result, 0, "failed to fill packet at index %zu", i
		);
	}

	// Expected: no matches
	uint32_t expected_ranges[test_count];
	for (size_t i = 0; i < test_count; ++i) {
		expected_ranges[i] = FILTER_RULE_INVALID;
	}

	struct block_allocator alloc;
	int res = block_allocator_init(&alloc);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize block allocator");
	block_allocator_put_arena(&alloc, arena, arena_size);

	struct memory_context mctx;
	res = memory_context_init(&mctx, "test", &alloc);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize memory context");

	const struct filter_rule *rule_ptr = &rule;

	struct filter filter;
	res = filter_init(
		&filter, combo_net4_port_proto_dst_compile, &rule_ptr, 1, &mctx
	);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize filter");

	uint32_t *actions = malloc(sizeof(uint32_t) * test_count);
	filter_query(
		&filter, combo_net4_port_proto_dst, packets, actions, test_count
	);

	res = compare_expected_ranges(actions, expected_ranges, test_count);
	TEST_ASSERT_SUCCESS(res, "some checks failed");

	for (size_t i = 0; i < test_count; ++i) {
		free_packet(packets[i]);
		free(packets[i]);
	}
	free(actions);

	return TEST_SUCCESS;
}

// Test: All three match (IP, port, protocol)
static int
test_all_match(void *arena) {
	LOG(INFO, "=== Test All Match ===");

	// Rule: dst IP 10.0.0.0/24, dst port 80-90, TCP only
	struct filter_rule_builder builder;
	builder_init(&builder);
	builder_add_net4_dst(&builder, ip(10, 0, 0, 0), ip(255, 255, 255, 0));
	builder_add_port_dst_range(&builder, 80, 90);
	builder_set_proto(&builder, IPPROTO_TCP, 0, 0);
	struct filter_rule rule = build_rule(&builder);

	// Test packets: All match
	const struct {
		uint8_t ip[NET4_LEN];
		uint16_t port;
		uint8_t proto;
	} test_cases[] = {
		{{10, 0, 0, 1}, 80, IPPROTO_TCP},   // All at start
		{{10, 0, 0, 255}, 90, IPPROTO_TCP}, // All at end
		{{10, 0, 0, 128}, 85, IPPROTO_TCP}, // All in middle
		{{10, 0, 0, 16}, 82, IPPROTO_TCP},  // Various combinations
	};
	const size_t test_count = sizeof(test_cases) / sizeof(test_cases[0]);

	struct packet *packets[test_count];
	for (size_t i = 0; i < test_count; ++i) {
		packets[i] = malloc(sizeof(struct packet));
		int fill_result = fill_packet_net4(
			packets[i],
			ip(192, 168, 0, 1),
			test_cases[i].ip,
			8080,
			test_cases[i].port,
			test_cases[i].proto,
			0
		);
		TEST_ASSERT_EQUAL(
			fill_result, 0, "failed to fill packet at index %zu", i
		);
	}

	// Expected: all match
	uint32_t expected_ranges[test_count];
	for (size_t i = 0; i < test_count; ++i) {
		expected_ranges[i] = 0;
	}

	struct block_allocator alloc;
	int res = block_allocator_init(&alloc);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize block allocator");
	block_allocator_put_arena(&alloc, arena, arena_size);

	struct memory_context mctx;
	res = memory_context_init(&mctx, "test", &alloc);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize memory context");

	const struct filter_rule *rule_ptr = &rule;

	struct filter filter;
	res = filter_init(
		&filter, combo_net4_port_proto_dst_compile, &rule_ptr, 1, &mctx
	);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize filter");

	uint32_t *ranges = malloc(sizeof(uint32_t) * test_count);
	filter_query(
		&filter, combo_net4_port_proto_dst, packets, ranges, test_count
	);

	res = compare_expected_ranges(ranges, expected_ranges, test_count);
	TEST_ASSERT_SUCCESS(res, "some checks failed");

	for (size_t i = 0; i < test_count; ++i) {
		free_packet(packets[i]);
		free(packets[i]);
	}
	free(ranges);

	return TEST_SUCCESS;
}

// Test: Multiple rules with overlapping conditions
static int
test_multiple_rules_overlap(void *arena) {
	LOG(INFO, "=== Test Multiple Rules with Overlap ===");

	// Rule 1: dst IP 10.0.0.0/24, dst port 80-90, TCP
	struct filter_rule_builder builder1;
	builder_init(&builder1);
	builder_add_net4_dst(&builder1, ip(10, 0, 0, 0), ip(255, 255, 255, 0));
	builder_add_port_dst_range(&builder1, 80, 90);
	builder_set_proto(&builder1, IPPROTO_TCP, 0, 0);
	struct filter_rule rule1 = build_rule(&builder1);

	// Rule 2: dst IP 10.0.0.0/16, dst port 85-95, TCP
	struct filter_rule_builder builder2;
	builder_init(&builder2);
	builder_add_net4_dst(&builder2, ip(10, 0, 0, 0), ip(255, 255, 0, 0));
	builder_add_port_dst_range(&builder2, 85, 95);
	builder_set_proto(&builder2, IPPROTO_TCP, 0, 0);
	struct filter_rule rule2 = build_rule(&builder2);

	// Rule 3: dst IP 10.0.0.0/24, dst port 80-90, UDP
	struct filter_rule_builder builder3;
	builder_init(&builder3);
	builder_add_net4_dst(&builder3, ip(10, 0, 0, 0), ip(255, 255, 255, 0));
	builder_add_port_dst_range(&builder3, 80, 90);
	builder_set_proto(&builder3, IPPROTO_UDP, 0, 0);
	struct filter_rule rule3 = build_rule(&builder3);

	struct filter_rule rules[] = {rule1, rule2, rule3};

	// Test packets with different overlap scenarios
	const struct {
		uint8_t ip[NET4_LEN];
		uint16_t port;
		uint8_t proto;
		size_t expected_count;
		uint32_t expected_actions[3];
	} test_cases[] = {
		// IP: 10.0.0.50, Port: 85, TCP -> matches rule1 and rule2
		{{10, 0, 0, 50}, 85, IPPROTO_TCP, 1, {0, 0, 0}},
		// IP: 10.0.0.50, Port: 80, TCP -> matches rule1 only
		{{10, 0, 0, 50}, 80, IPPROTO_TCP, 1, {0, 0, 0}},
		// IP: 10.0.1.50, Port: 85, TCP -> matches rule2 only
		{{10, 0, 1, 50}, 85, IPPROTO_TCP, 1, {1, 0, 0}},
		// IP: 10.0.0.50, Port: 85, UDP -> matches rule3 only
		{{10, 0, 0, 50}, 85, IPPROTO_UDP, 1, {2, 0, 0}},
		// IP: 10.0.0.50, Port: 95, TCP -> matches rule2 only
		{{10, 0, 0, 50}, 95, IPPROTO_TCP, 1, {1, 0, 0}},
		// IP: 10.0.0.50, Port: 100, TCP -> no match
		{{10, 0, 0, 50},
		 100,
		 IPPROTO_TCP,
		 0,
		 {FILTER_RULE_INVALID, 0, 0}},
		// IP: 10.1.0.50, Port: 85, TCP -> no match (outside /16)
		{{10, 1, 0, 50}, 85, IPPROTO_TCP, 0, {FILTER_RULE_INVALID, 0, 0}
		},
	};
	const size_t test_count = sizeof(test_cases) / sizeof(test_cases[0]);

	struct packet *packets[test_count];
	for (size_t i = 0; i < test_count; ++i) {
		packets[i] = malloc(sizeof(struct packet));
		int fill_result = fill_packet_net4(
			packets[i],
			ip(192, 168, 0, 1),
			test_cases[i].ip,
			8080,
			test_cases[i].port,
			test_cases[i].proto,
			0
		);
		TEST_ASSERT_EQUAL(
			fill_result, 0, "failed to fill packet at index %zu", i
		);
	}

	// Expected ranges
	uint32_t expected_ranges[test_count];
	for (size_t i = 0; i < test_count; ++i) {
		expected_ranges[i] = test_cases[i].expected_actions[0];
	}

	struct block_allocator alloc;
	int res = block_allocator_init(&alloc);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize block allocator");
	block_allocator_put_arena(&alloc, arena, arena_size);

	struct memory_context mctx;
	res = memory_context_init(&mctx, "test", &alloc);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize memory context");

	const struct filter_rule *rule_ptrs[3] = {
		&rules[0], &rules[1], &rules[2]
	};

	struct filter filter;
	res = filter_init(
		&filter, combo_net4_port_proto_dst_compile, rule_ptrs, 3, &mctx
	);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize filter");

	uint32_t *ranges = malloc(sizeof(uint32_t) * test_count);
	filter_query(
		&filter, combo_net4_port_proto_dst, packets, ranges, test_count
	);

	res = compare_expected_ranges(ranges, expected_ranges, test_count);
	TEST_ASSERT_SUCCESS(res, "some checks failed");

	for (size_t i = 0; i < test_count; ++i) {
		free_packet(packets[i]);
		free(packets[i]);
	}
	free(ranges);

	return TEST_SUCCESS;
}

// Test: TCP flags matching
static int
test_tcp_flags(void *arena) {
	LOG(INFO, "=== Test TCP Flags ===");

	// Rule 1: dst IP 10.0.0.0/24, dst port 80, TCP with SYN flag
	struct filter_rule_builder builder1;
	builder_init(&builder1);
	builder_add_net4_dst(&builder1, ip(10, 0, 0, 0), ip(255, 255, 255, 0));
	builder_add_port_dst_range(&builder1, 80, 80);
	builder_set_proto(&builder1, IPPROTO_TCP, 0x02, 0); // SYN flag
	struct filter_rule rule1 = build_rule(&builder1);

	// Rule 2: dst IP 10.0.0.0/24, dst port 80, TCP with ACK flag
	struct filter_rule_builder builder2;
	builder_init(&builder2);
	builder_add_net4_dst(&builder2, ip(10, 0, 0, 0), ip(255, 255, 255, 0));
	builder_add_port_dst_range(&builder2, 80, 80);
	builder_set_proto(&builder2, IPPROTO_TCP, 0x10, 0); // ACK flag
	struct filter_rule rule2 = build_rule(&builder2);

	struct filter_rule rules[] = {rule1, rule2};

	// Test packets with different TCP flags
	const struct {
		uint8_t ip[NET4_LEN];
		uint16_t port;
		uint8_t proto;
		uint8_t flags;
		size_t expected_count;
		uint32_t expected_actions[2];
	} test_cases[] = {
		// SYN flag -> matches rule1
		{{10, 0, 0, 1}, 80, IPPROTO_TCP, 0x02, 1, {0, 0}},
		// ACK flag -> matches rule2
		{{10, 0, 0, 1}, 80, IPPROTO_TCP, 0x10, 1, {1, 0}},
		// SYN+ACK flags -> matches both rules
		{{10, 0, 0, 1}, 80, IPPROTO_TCP, 0x12, 1, {0, 0}},
		// FIN flag -> no match
		{{10, 0, 0, 1},
		 80,
		 IPPROTO_TCP,
		 0x01,
		 0,
		 {FILTER_RULE_INVALID, 0}},
		// No flags -> no match
		{{10, 0, 0, 1},
		 80,
		 IPPROTO_TCP,
		 0x00,
		 0,
		 {FILTER_RULE_INVALID, 0}},
	};
	const size_t test_count = sizeof(test_cases) / sizeof(test_cases[0]);

	struct packet *packets[test_count];
	for (size_t i = 0; i < test_count; ++i) {
		packets[i] = malloc(sizeof(struct packet));
		int fill_result = fill_packet_net4(
			packets[i],
			ip(192, 168, 0, 1),
			test_cases[i].ip,
			8080,
			test_cases[i].port,
			test_cases[i].proto,
			test_cases[i].flags
		);
		TEST_ASSERT_EQUAL(
			fill_result, 0, "failed to fill packet at index %zu", i
		);
	}

	// Expected ranges
	uint32_t expected_ranges[test_count];
	for (size_t i = 0; i < test_count; ++i) {
		expected_ranges[i] = test_cases[i].expected_actions[0];
	}

	struct block_allocator alloc;
	int res = block_allocator_init(&alloc);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize block allocator");
	block_allocator_put_arena(&alloc, arena, arena_size);

	struct memory_context mctx;
	res = memory_context_init(&mctx, "test", &alloc);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize memory context");

	const struct filter_rule *rule_ptrs[2] = {&rules[0], &rules[1]};

	struct filter filter;
	res = filter_init(
		&filter, combo_net4_port_proto_dst_compile, rule_ptrs, 2, &mctx
	);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize filter");

	uint32_t *ranges = malloc(sizeof(uint32_t) * test_count);
	filter_query(
		&filter, combo_net4_port_proto_dst, packets, ranges, test_count
	);

	res = compare_expected_ranges(ranges, expected_ranges, test_count);
	TEST_ASSERT_SUCCESS(res, "some checks failed");

	for (size_t i = 0; i < test_count; ++i) {
		free_packet(packets[i]);
		free(packets[i]);
	}
	free(ranges);

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
	if (test_no_match_proto_only(arena) != 0) {
		LOG(ERROR, "test_no_match_proto_only failed");
		++failed;
	}

	++tests;
	if (test_all_match(arena) != 0) {
		LOG(ERROR, "test_all_match failed");
		++failed;
	}

	++tests;
	if (test_multiple_rules_overlap(arena) != 0) {
		LOG(ERROR, "test_multiple_rules_overlap failed");
		++failed;
	}

	++tests;
	if (test_tcp_flags(arena) != 0) {
		LOG(ERROR, "test_tcp_flags failed");
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
