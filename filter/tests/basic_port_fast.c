#include "common/memory.h"
#include "common/memory_address.h"
#include "common/memory_block.h"
#include "common/network.h"
#include "common/registry.h"
#include "common/rng.h"
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
#include <time.h>

////////////////////////////////////////////////////////////////////////////////

FILTER_COMPILER_DECLARE(sign_fast_src_dst, port_fast_src, port_fast_dst);
FILTER_QUERY_DECLARE(sign_fast_src_dst, port_fast_src, port_fast_dst);

FILTER_COMPILER_DECLARE(sign_fast_src, port_fast_src);
FILTER_QUERY_DECLARE(sign_fast_src, port_fast_src);

FILTER_COMPILER_DECLARE(sign_fast_dst, port_fast_dst);
FILTER_QUERY_DECLARE(sign_fast_dst, port_fast_dst);

////////////////////////////////////////////////////////////////////////////////

enum filter_sign { src = 0, dst = 1, src_dst = 2 };

const char *
filter_sign_to_string(enum filter_sign sign) {
	switch (sign) {
	case src:
		return "src";
	case dst:
		return "dst";
	case src_dst:
		return "src_dst";
	}
	assert(false);
	return "";
}

////////////////////////////////////////////////////////////////////////////////

static int
query_and_expect_actions(
	struct filter *filter,
	enum filter_sign type,
	struct packet **packets,
	size_t packets_count,
	struct value_range **expected
) {
	struct value_range **ranges =
		malloc(sizeof(struct value_range *) * packets_count);

	switch (type) {
	case src:
		FILTER_QUERY(
			filter, sign_fast_src, packets, ranges, packets_count
		);
		break;
	case dst:
		FILTER_QUERY(
			filter, sign_fast_dst, packets, ranges, packets_count
		);
		break;
	case src_dst:
		FILTER_QUERY(
			filter,
			sign_fast_src_dst,
			packets,
			ranges,
			packets_count
		);
		break;
	}

	for (size_t packet_idx = 0; packet_idx < packets_count; ++packet_idx) {
		struct value_range *range = ranges[packet_idx];
		uint32_t *range_values = ADDR_OF(&range->values);

		struct value_range *expected_range = expected[packet_idx];
		uint32_t *expected_range_values = expected_range->values;

		for (size_t expected_value_idx = 0;
		     expected_value_idx < expected_range->count;
		     ++expected_value_idx) {
			int found = 0;
			for (size_t got_idx = 0; got_idx < range->count;
			     ++got_idx) {
				if (expected_range_values[expected_value_idx] ==
				    range_values[got_idx]) {
					found = 1;
					break;
				}
			}

			if (!found) {
				LOG(ERROR,
				    "packet at idx %zu: expected action %u, "
				    "got %zu actions:",
				    packet_idx,
				    expected_range_values[expected_value_idx],
				    range->count);
				for (size_t got_idx = 0; got_idx < range->count;
				     ++got_idx) {
					LOG(ERROR,
					    "  action[%zu] = %u",
					    got_idx,
					    range_values[got_idx]);
				}
			}

			TEST_ASSERT(
				found,
				"packet at idx %zu: not got expected action %u",
				packet_idx,
				expected_range_values[expected_value_idx]
			);
		}
	}

	free(ranges);

	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////

enum { arena_size = 1 << 28 };

static int
test_basic(void *arena, enum filter_sign sign) {
	assert(sign == src || sign == dst);
	const char *sign_name = filter_sign_to_string(sign);

	LOG(INFO, "=== Test Basic: %s ===", sign_name);

	const uint16_t check_ports[] = {10,   20,   30,	  79,	 80,   87,  88,
					89,   91,   92,	  95,	 96,   100, 103,
					105,  110,  111,  116,	 119,  128, 143,
					1024, 5000, 8080, 49152, 65535};
	const size_t checks_count =
		sizeof(check_ports) / sizeof(check_ports[0]);
	struct packet *packets[checks_count];
	uint8_t sip[NET4_LEN] = {0, 0, 0, 0};
	uint8_t dip[NET4_LEN] = {0, 0, 0, 0};

	for (size_t i = 0; i < checks_count; ++i) {
		packets[i] = malloc(sizeof(struct packet));
		int fill_result = fill_packet_net4(
			packets[i],
			sip,
			dip,
			check_ports[i],
			check_ports[i],
			IPPROTO_UDP,
			0
		);
		TEST_ASSERT_EQUAL(
			fill_result,
			0,
			"failed to fill packet at index %zu (port=%u)",
			i,
			check_ports[i]
		);
	}

	struct test_port_range {
		uint16_t from;
		uint16_t to;
	};

	struct test_port_range ranges[] = {
		{.from = 96, .to = 103},     // [96, 103]
		{.from = 96, .to = 111},     // [96, 111]
		{.from = 80, .to = 95},	     // [80, 95]
		{.from = 88, .to = 91},	     // [88, 91]
		{.from = 116, .to = 119},    // [116, 119]
		{.from = 128, .to = 143},    // [128, 143]
		{.from = 1024, .to = 5000},  // [1024, 5000]
		{.from = 8080, .to = 8080},  // [8080, 8080] - single port
		{.from = 49152, .to = 65535} // [49152, 65535] - high ports
	};
	const size_t ranges_count = sizeof(ranges) / sizeof(ranges[0]);

	struct value_range *expected_ranges[checks_count];
	for (size_t i = 0; i < checks_count; ++i) {
		expected_ranges[i] = malloc(sizeof(struct value_range));
		expected_ranges[i]->count = 0;
		expected_ranges[i]->values =
			malloc(sizeof(uint32_t) * ranges_count); // reserve
	}

	struct filter_rule rules[ranges_count];
	struct filter_rule_builder builders[ranges_count];
	for (size_t range_idx = 0; range_idx < ranges_count; ++range_idx) {
		struct filter_rule_builder *builder = &builders[range_idx];
		builder_init(builder);

		if (sign == src) {
			builder_add_port_src_range(
				builder,
				ranges[range_idx].from,
				ranges[range_idx].to
			);
		} else {
			builder_add_port_dst_range(
				builder,
				ranges[range_idx].from,
				ranges[range_idx].to
			);
		}

		rules[range_idx] = build_rule(
			builder, (range_idx + 1) | ACTION_NON_TERMINATE
		);

		for (size_t check_idx = 0; check_idx < checks_count;
		     ++check_idx) {
			if (ranges[range_idx].from <= check_ports[check_idx] &&
			    check_ports[check_idx] <= ranges[range_idx].to) {
				expected_ranges[check_idx]->values
					[expected_ranges[check_idx]->count++] =
					(range_idx + 1) | ACTION_NON_TERMINATE;
			}
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
	if (sign == src) {
		res = FILTER_INIT(
			&filter, sign_fast_src, rules, ranges_count, &mctx
		);
	} else {
		res = FILTER_INIT(
			&filter, sign_fast_dst, rules, ranges_count, &mctx
		);
	}
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize filter");

	res = query_and_expect_actions(
		&filter, sign, packets, checks_count, expected_ranges
	);
	TEST_ASSERT_SUCCESS(res, "some checks failed");

	for (size_t i = 0; i < checks_count; ++i) {
		free(expected_ranges[i]->values);
		free(expected_ranges[i]);
		free_packet(packets[i]);
		free(packets[i]);
	}

	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////

static int
test_multiple_ranges_per_rule(void *arena, enum filter_sign sign) {
	assert(sign == src || sign == dst);
	const char *sign_name = filter_sign_to_string(sign);

	LOG(INFO, "=== Test Multiple Ranges Per Rule: %s ===", sign_name);

	// Test packets with specific ports
	const uint16_t test_ports[] = {
		80,    // Rule 1, Range A
		443,   // Rule 1, Range B
		8080,  // Rule 1, Range C
		22,    // Rule 2, Range D
		3389,  // Rule 2, Range E
		3306,  // Rule 3, Range F
		5432,  // Rule 3, Range G
		12345, // No match
	};
	const size_t test_ports_count =
		sizeof(test_ports) / sizeof(test_ports[0]);

	struct packet *packets[test_ports_count];
	uint8_t sip[NET4_LEN] = {0, 0, 0, 0};
	uint8_t dip[NET4_LEN] = {0, 0, 0, 0};

	for (size_t i = 0; i < test_ports_count; ++i) {
		packets[i] = malloc(sizeof(struct packet));
		int fill_result = fill_packet_net4(
			packets[i],
			sip,
			dip,
			test_ports[i],
			test_ports[i],
			IPPROTO_TCP,
			0
		);
		TEST_ASSERT_EQUAL(
			fill_result, 0, "failed to fill packet at index %zu", i
		);
	}

	// Expected actions for each test packet
	uint32_t expected_actions[][3] = {
		{1 | ACTION_NON_TERMINATE, 0, 0}, // Packet 0: Rule 1
		{1 | ACTION_NON_TERMINATE, 0, 0}, // Packet 1: Rule 1
		{1 | ACTION_NON_TERMINATE, 0, 0}, // Packet 2: Rule 1
		{2 | ACTION_NON_TERMINATE, 0, 0}, // Packet 3: Rule 2
		{2 | ACTION_NON_TERMINATE, 0, 0}, // Packet 4: Rule 2
		{3 | ACTION_NON_TERMINATE, 0, 0}, // Packet 5: Rule 3
		{3 | ACTION_NON_TERMINATE, 0, 0}, // Packet 6: Rule 3
		{0, 0, 0},			  // Packet 7: No match
	};
	uint32_t expected_counts[] = {1, 1, 1, 1, 1, 1, 1, 0};

	struct value_range *expected_ranges[test_ports_count];
	for (size_t i = 0; i < test_ports_count; ++i) {
		expected_ranges[i] = malloc(sizeof(struct value_range));
		expected_ranges[i]->count = expected_counts[i];
		expected_ranges[i]->values = malloc(sizeof(uint32_t) * 3);
		for (size_t j = 0; j < expected_counts[i]; ++j) {
			expected_ranges[i]->values[j] = expected_actions[i][j];
		}
	}

	// Build the 3 rules
	const size_t num_rules = 3;
	struct filter_rule rules[num_rules];
	struct filter_rule_builder builders[num_rules];

	// Rule 1: Add 3 port ranges (80, 443, 8080)
	builder_init(&builders[0]);
	if (sign == src) {
		builder_add_port_src_range(&builders[0], 80, 80);
		builder_add_port_src_range(&builders[0], 443, 443);
		builder_add_port_src_range(&builders[0], 8080, 8080);
	} else {
		builder_add_port_dst_range(&builders[0], 80, 80);
		builder_add_port_dst_range(&builders[0], 443, 443);
		builder_add_port_dst_range(&builders[0], 8080, 8080);
	}
	rules[0] = build_rule(&builders[0], 1 | ACTION_NON_TERMINATE);

	// Rule 2: Add 2 port ranges (22, 3389)
	builder_init(&builders[1]);
	if (sign == src) {
		builder_add_port_src_range(&builders[1], 22, 22);
		builder_add_port_src_range(&builders[1], 3389, 3389);
	} else {
		builder_add_port_dst_range(&builders[1], 22, 22);
		builder_add_port_dst_range(&builders[1], 3389, 3389);
	}
	rules[1] = build_rule(&builders[1], 2 | ACTION_NON_TERMINATE);

	// Rule 3: Add 2 port ranges (3306, 5432)
	builder_init(&builders[2]);
	if (sign == src) {
		builder_add_port_src_range(&builders[2], 3306, 3306);
		builder_add_port_src_range(&builders[2], 5432, 5432);
	} else {
		builder_add_port_dst_range(&builders[2], 3306, 3306);
		builder_add_port_dst_range(&builders[2], 5432, 5432);
	}
	rules[2] = build_rule(&builders[2], 3 | ACTION_NON_TERMINATE);

	struct block_allocator alloc;
	int res = block_allocator_init(&alloc);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize block allocator");
	block_allocator_put_arena(&alloc, arena, arena_size);

	struct memory_context mctx;
	res = memory_context_init(&mctx, "test", &alloc);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize memory context");

	struct filter filter;
	if (sign == src) {
		res = FILTER_INIT(
			&filter, sign_fast_src, rules, num_rules, &mctx
		);
	} else {
		res = FILTER_INIT(
			&filter, sign_fast_dst, rules, num_rules, &mctx
		);
	}
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize filter");

	res = query_and_expect_actions(
		&filter, sign, packets, test_ports_count, expected_ranges
	);
	TEST_ASSERT_SUCCESS(res, "some checks failed");

	for (size_t i = 0; i < test_ports_count; ++i) {
		free(expected_ranges[i]->values);
		free(expected_ranges[i]);
		free_packet(packets[i]);
		free(packets[i]);
	}

	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////

static int
is_port_in_range(uint16_t port, struct filter_port_range *range) {
	return port >= range->from && port <= range->to;
}

static int
stress(void *arena,
       enum filter_sign sign,
       size_t num_rules,
       size_t num_packets,
       uint64_t seed) {
	const char *sign_name = filter_sign_to_string(sign);

	LOG(INFO,
	    "=== Stress Test: Correctness comparison (sign=%s, rules=%zu, "
	    "queries=%zu, seed=%lu) "
	    "===",
	    sign_name,
	    num_rules,
	    num_packets,
	    seed);

	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, arena, arena_size);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	// Generate random rules
	struct filter_rule *rules =
		malloc(sizeof(struct filter_rule) * num_rules);
	struct filter_rule_builder *builders =
		malloc(sizeof(struct filter_rule_builder) * num_rules);
	TEST_ASSERT_NOT_NULL(rules, "failed to allocate rules");
	TEST_ASSERT_NOT_NULL(builders, "failed to allocate builders");

	uint64_t rng = seed;

	for (size_t rule_idx = 0; rule_idx < num_rules; rule_idx++) {
		struct filter_rule_builder *builder = &builders[rule_idx];
		builder_init(builder);

		// Generate 1-3 port ranges per rule
		size_t num_ranges = 1 + rng_next(&rng) % 3;
		for (size_t i = 0; i < num_ranges; ++i) {
			uint16_t from = rng_next(&rng) % 60000;
			uint16_t range_size = 1 + rng_next(&rng) % 100;
			uint16_t to = from + range_size;

			if (sign == src || sign == src_dst) {
				builder_add_port_src_range(builder, from, to);
			}
			if (sign == dst || sign == src_dst) {
				builder_add_port_dst_range(builder, from, to);
			}
		}

		rules[rule_idx] = build_rule(
			&builders[rule_idx],
			(rule_idx + 1) | ACTION_NON_TERMINATE
		);
	}

	struct value_range **expected_ranges =
		malloc(sizeof(struct value_range *) * num_packets);
	for (size_t range_idx = 0; range_idx < num_packets; ++range_idx) {
		expected_ranges[range_idx] = malloc(sizeof(struct value_range));
		expected_ranges[range_idx]->count = 0;
		expected_ranges[range_idx]->values =
			malloc(sizeof(uint32_t) * num_rules); // reserve
	}

	// Initialize filter
	struct filter filter;
	switch (sign) {
	case src:
		res = FILTER_INIT(
			&filter,
			sign_fast_src,
			rules,
			num_rules,
			&memory_context
		);
		break;
	case dst:
		res = FILTER_INIT(
			&filter,
			sign_fast_dst,
			rules,
			num_rules,
			&memory_context
		);
		break;
	case src_dst:
		res = FILTER_INIT(
			&filter,
			sign_fast_src_dst,
			rules,
			num_rules,
			&memory_context
		);
		break;
	}

	TEST_ASSERT_EQUAL(res, 0, "failed to initialize filter");

	struct packet **packets = malloc(sizeof(struct packet *) * num_packets);
	uint8_t sip[NET4_LEN] = {0, 0, 0, 0};
	uint8_t dip[NET4_LEN] = {0, 0, 0, 0};

	for (size_t packet_idx = 0; packet_idx < num_packets; ++packet_idx) {
		packets[packet_idx] = malloc(sizeof(struct packet));
		uint16_t src_port = rng_next(&rng) % 65536;
		uint16_t dst_port = rng_next(&rng) % 65536;

		int fill_result = fill_packet_net4(
			packets[packet_idx],
			sip,
			dip,
			src_port,
			dst_port,
			IPPROTO_UDP,
			0
		);
		assert(fill_result == 0);

		const int check_src = sign == src || sign == src_dst;
		const int check_dst = sign == dst || sign == src_dst;

		for (size_t rule_idx = 0; rule_idx < num_rules; ++rule_idx) {
			struct filter_rule *rule = &rules[rule_idx];
			int ok = 1;

			if (check_src) {
				int src_match = 0;
				for (size_t i = 0;
				     i < rule->transport.src_count;
				     ++i) {
					if (is_port_in_range(
						    src_port,
						    &rule->transport.srcs[i]
					    )) {
						src_match = 1;
						break;
					}
				}
				if (!src_match)
					ok = 0;
			}

			if (check_dst) {
				int dst_match = 0;
				for (size_t i = 0;
				     i < rule->transport.dst_count;
				     ++i) {
					if (is_port_in_range(
						    dst_port,
						    &rule->transport.dsts[i]
					    )) {
						dst_match = 1;
						break;
					}
				}
				if (!dst_match)
					ok = 0;
			}

			if (ok) {
				struct value_range *range =
					expected_ranges[packet_idx];
				range->values[range->count++] =
					(rule_idx + 1) | ACTION_NON_TERMINATE;
			}
		}

		// Debug log for first failure
		if (packet_idx < 10 && expected_ranges[packet_idx]->count > 0) {
			LOG(DEBUG,
			    "Packet %zu: src_port=%u, dst_port=%u, expected "
			    "%zu actions",
			    packet_idx,
			    src_port,
			    dst_port,
			    expected_ranges[packet_idx]->count);
			for (size_t i = 0;
			     i < expected_ranges[packet_idx]->count;
			     ++i) {
				uint32_t action =
					expected_ranges[packet_idx]->values[i];
				uint32_t rule_idx =
					(action & ~ACTION_NON_TERMINATE) - 1;
				if (rule_idx < num_rules) {
					struct filter_rule *rule =
						&rules[rule_idx];
					LOG(DEBUG,
					    "  Rule %u has %u src ranges, %u "
					    "dst ranges",
					    (unsigned)rule_idx,
					    rule->transport.src_count,
					    rule->transport.dst_count);
				}
			}
		}
	}

	int result = query_and_expect_actions(
		&filter, sign, packets, num_packets, expected_ranges
	);
	TEST_ASSERT_SUCCESS(
		result, "failed to query packets and compare with expected"
	);

	free(rules);
	free(builders);
	for (size_t packet_idx = 0; packet_idx < num_packets; ++packet_idx) {
		free(expected_ranges[packet_idx]->values);
		free(expected_ranges[packet_idx]);
		free_packet(packets[packet_idx]);
		free(packets[packet_idx]);
	}
	free(packets);
	free(expected_ranges);

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
	if (test_basic(arena, src) != 0) {
		LOG(ERROR, "Test_basic (src) failed");
		++failed;
	}

	++tests;
	if (test_basic(arena, dst) != 0) {
		LOG(ERROR, "Test_basic (dst) failed");
		++failed;
	}

	++tests;
	if (test_multiple_ranges_per_rule(arena, src) != 0) {
		LOG(ERROR, "Test_multiple_ranges_per_rule (src) failed");
		++failed;
	}

	++tests;
	if (test_multiple_ranges_per_rule(arena, dst) != 0) {
		LOG(ERROR, "Test_multiple_ranges_per_rule (dst) failed");
		++failed;
	}

	struct stress_case {
		enum filter_sign sign;
		size_t num_rules;
		size_t num_packets;
		uint64_t seed;
	};

	struct stress_case cases[] = {
		{src, 10, 10000, 1},
		{dst, 10, 10000, 2},
		{src_dst, 10, 10000, 3},
		{src, 100, 10000, 4},
		{dst, 100, 10000, 5},
		{src_dst, 20, 10000, 6},
		{src, 10, 10000, 7},
		{dst, 10, 10000, 8},
		{src_dst, 10, 10000, 9},
		{src, 100, 10000, 10},
		{dst, 100, 10000, 11},
		{src_dst, 20, 3, 12},
		{src, 10, 10000, 13},
		{dst, 10, 10000, 14},
		{src_dst, 10, 10000, 15},
		{src, 100, 10000, 16},
		{dst, 100, 10000, 17},
		{src_dst, 20, 10000, 18},
	};

	for (size_t test_idx = 0;
	     test_idx < sizeof(cases) / sizeof(struct stress_case);
	     ++test_idx) {
		struct stress_case *stress_case = &cases[test_idx];
		++tests;
		if (stress(arena,
			   stress_case->sign,
			   stress_case->num_rules,
			   stress_case->num_packets,
			   stress_case->seed)) {
			++failed;
			LOG(ERROR,
			    "Stress test (sign %s, %zu rules, %zu packets, "
			    "seed %lu) failed",
			    filter_sign_to_string(stress_case->sign),
			    stress_case->num_rules,
			    stress_case->num_packets,
			    stress_case->seed);
		}
	}

	free(arena);

	if (failed == 0) {
		LOG(INFO, "All %zu tests passed", tests);
	} else {
		LOG(ERROR, "%zu/%zu tests failed", failed, tests);
	}

	return (failed == 0 ? 0 : 1);
}