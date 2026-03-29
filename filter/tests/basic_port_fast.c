#include "common/memory.h"
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

FILTER_COMPILER_DECLARE(
	sign_fast_src_dst_compile, port_fast_src, port_fast_dst
);
FILTER_QUERY_DECLARE(sign_fast_src_dst, port_fast_src, port_fast_dst);

FILTER_COMPILER_DECLARE(sign_fast_src_compile, port_fast_src);
FILTER_QUERY_DECLARE(sign_fast_src, port_fast_src);

FILTER_COMPILER_DECLARE(sign_fast_dst_compile, port_fast_dst);
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
		filter_query(
			filter, sign_fast_src, packets, ranges, packets_count
		);
		break;
	case dst:
		filter_query(
			filter, sign_fast_dst, packets, ranges, packets_count
		);
		break;
	case src_dst:
		filter_query(
			filter,
			sign_fast_src_dst,
			packets,
			ranges,
			packets_count
		);
		break;
	}

	TEST_ASSERT_SUCCESS(
		compare_expected_ranges(ranges, expected, packets_count),
		"got value ranges != expected"
	);

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
		{.from = 96, .to = 103},
		{.from = 80, .to = 95},
		{.from = 116, .to = 119},
		{.from = 1024, .to = 5000},
		{.from = 128, .to = 143},
		{.from = 49152, .to = 65535},
		{.from = 8080, .to = 8080},
		{.from = 88, .to = 91},
		{.from = 96, .to = 111},
		{.from = 43, .to = 79},
		{.from = 82, .to = 95},
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

		rules[range_idx] = build_rule(builder, (range_idx + 1));

		for (size_t check_idx = 0; check_idx < checks_count;
		     ++check_idx) {
			if (ranges[range_idx].from <= check_ports[check_idx] &&
			    check_ports[check_idx] <= ranges[range_idx].to &&
			    !expected_ranges[check_idx]->count) {
				expected_ranges[check_idx]->values
					[expected_ranges[check_idx]->count++] =
					(range_idx + 1);
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
			&filter,
			sign_fast_src_compile,
			rules,
			ranges_count,
			&mctx
		);
	} else {
		res = FILTER_INIT(
			&filter,
			sign_fast_dst_compile,
			rules,
			ranges_count,
			&mctx
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
		{1, 0, 0}, // Packet 0: Rule 1
		{1, 0, 0}, // Packet 1: Rule 1
		{1, 0, 0}, // Packet 2: Rule 1
		{2, 0, 0}, // Packet 3: Rule 2
		{2, 0, 0}, // Packet 4: Rule 2
		{3, 0, 0}, // Packet 5: Rule 3
		{3, 0, 0}, // Packet 6: Rule 3
		{0, 0, 0}, // Packet 7: No match
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
	rules[0] = build_rule(&builders[0], 1);

	// Rule 2: Add 2 port ranges (22, 3389)
	builder_init(&builders[1]);
	if (sign == src) {
		builder_add_port_src_range(&builders[1], 22, 22);
		builder_add_port_src_range(&builders[1], 3389, 3389);
	} else {
		builder_add_port_dst_range(&builders[1], 22, 22);
		builder_add_port_dst_range(&builders[1], 3389, 3389);
	}
	rules[1] = build_rule(&builders[1], 2);

	// Rule 3: Add 2 port ranges (3306, 5432)
	builder_init(&builders[2]);
	if (sign == src) {
		builder_add_port_src_range(&builders[2], 3306, 3306);
		builder_add_port_src_range(&builders[2], 5432, 5432);
	} else {
		builder_add_port_dst_range(&builders[2], 3306, 3306);
		builder_add_port_dst_range(&builders[2], 5432, 5432);
	}
	rules[2] = build_rule(&builders[2], 3);

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
			&filter, sign_fast_src_compile, rules, num_rules, &mctx
		);
	} else {
		res = FILTER_INIT(
			&filter, sign_fast_dst_compile, rules, num_rules, &mctx
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

		rules[rule_idx] =
			build_rule(&builders[rule_idx], (rule_idx + 1));
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
			sign_fast_src_compile,
			rules,
			num_rules,
			&memory_context
		);
		break;
	case dst:
		res = FILTER_INIT(
			&filter,
			sign_fast_dst_compile,
			rules,
			num_rules,
			&memory_context
		);
		break;
	case src_dst:
		res = FILTER_INIT(
			&filter,
			sign_fast_src_dst_compile,
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
				if (!range->count)
					range->values[range->count++] =
						(rule_idx + 1);
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
				uint32_t rule_idx = action - 1;
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

// Corner case tests

static int
test_no_match(void *arena, enum filter_sign sign) {
	assert(sign == src || sign == dst);
	const char *sign_name = filter_sign_to_string(sign);

	LOG(INFO, "=== Test No Match: %s ===", sign_name);

	// Define port ranges that won't match our test packets
	struct test_port_range {
		uint16_t from;
		uint16_t to;
	};

	struct test_port_range ranges[] = {
		{.from = 80, .to = 90},	      // HTTP range
		{.from = 443, .to = 443},     // HTTPS
		{.from = 1024, .to = 5000},   // Registered ports
		{.from = 49152, .to = 65535}, // Dynamic/private ports
	};
	const size_t ranges_count = sizeof(ranges) / sizeof(ranges[0]);

	// Test ports that don't match any range
	const uint16_t test_ports[] = {
		79,    // One below first range
		91,    // One above first range
		442,   // One below HTTPS
		444,   // One above HTTPS
		1023,  // One below registered
		5001,  // One above registered
		49151, // One below dynamic
		22,    // SSH - not in any range
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

	// Build rules
	struct filter_rule rules[ranges_count];
	struct filter_rule_builder builders[ranges_count];
	for (size_t range_idx = 0; range_idx < ranges_count; ++range_idx) {
		builder_init(&builders[range_idx]);
		if (sign == src) {
			builder_add_port_src_range(
				&builders[range_idx],
				ranges[range_idx].from,
				ranges[range_idx].to
			);
		} else {
			builder_add_port_dst_range(
				&builders[range_idx],
				ranges[range_idx].from,
				ranges[range_idx].to
			);
		}
		rules[range_idx] =
			build_rule(&builders[range_idx], (range_idx + 1));
	}

	// Expected: no matches for any packet
	struct value_range *expected_ranges[test_ports_count];
	for (size_t i = 0; i < test_ports_count; ++i) {
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
	if (sign == src) {
		res = FILTER_INIT(
			&filter,
			sign_fast_src_compile,
			rules,
			ranges_count,
			&mctx
		);
	} else {
		res = FILTER_INIT(
			&filter,
			sign_fast_dst_compile,
			rules,
			ranges_count,
			&mctx
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

static int
test_overlapping_ranges(void *arena, enum filter_sign sign) {
	assert(sign == src || sign == dst);
	const char *sign_name = filter_sign_to_string(sign);

	LOG(INFO, "=== Test Overlapping Ranges: %s ===", sign_name);

	// Define overlapping port ranges
	// 1000-2000 contains 1200-1500 contains 1300-1400
	struct test_port_range {
		uint16_t from;
		uint16_t to;
	};

	struct test_port_range ranges[] = {
		{.from = 1000, .to = 2000}, // Widest
		{.from = 1200, .to = 1500}, // Middle
		{.from = 1300, .to = 1400}, // Narrowest
		{.from = 3000, .to = 4000}, // Non-overlapping
	};
	const size_t ranges_count = sizeof(ranges) / sizeof(ranges[0]);

	// Test ports
	const uint16_t test_ports[] = {
		1350, // Matches rules 1, 2, 3 (all nested)
		1250, // Matches rules 1, 2 (not in narrowest)
		1100, // Matches rule 1 only (not in middle)
		3500, // Matches rule 4 only
		5000, // Matches nothing
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

	// Build rules
	struct filter_rule rules[ranges_count];
	struct filter_rule_builder builders[ranges_count];
	for (size_t range_idx = 0; range_idx < ranges_count; ++range_idx) {
		builder_init(&builders[range_idx]);
		if (sign == src) {
			builder_add_port_src_range(
				&builders[range_idx],
				ranges[range_idx].from,
				ranges[range_idx].to
			);
		} else {
			builder_add_port_dst_range(
				&builders[range_idx],
				ranges[range_idx].from,
				ranges[range_idx].to
			);
		}
		rules[range_idx] =
			build_rule(&builders[range_idx], (range_idx + 1));
	}

	// Expected matches
	uint32_t expected_actions[][4] = {
		{1, 0, 0, 0}, // Port 1350: rules 1,2,3
		{1, 0, 0, 0}, // Port 1250: rules 1,2
		{1, 0, 0, 0}, // Port 1100: rule 1
		{4, 0, 0, 0}, // Port 3500: rule 4
		{0, 0, 0, 0}, // Port 5000: no match
	};
	uint32_t expected_counts[] = {1, 1, 1, 1, 0};

	struct value_range *expected_ranges[test_ports_count];
	for (size_t i = 0; i < test_ports_count; ++i) {
		expected_ranges[i] = malloc(sizeof(struct value_range));
		expected_ranges[i]->count = expected_counts[i];
		expected_ranges[i]->values = malloc(sizeof(uint32_t) * 4);
		for (size_t j = 0; j < expected_counts[i]; ++j) {
			expected_ranges[i]->values[j] = expected_actions[i][j];
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
			&filter,
			sign_fast_src_compile,
			rules,
			ranges_count,
			&mctx
		);
	} else {
		res = FILTER_INIT(
			&filter,
			sign_fast_dst_compile,
			rules,
			ranges_count,
			&mctx
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

static int
test_boundary_conditions(void *arena, enum filter_sign sign) {
	assert(sign == src || sign == dst);
	const char *sign_name = filter_sign_to_string(sign);

	LOG(INFO, "=== Test Boundary Conditions: %s ===", sign_name);

	// Port range: 1000-2000
	struct test_port_range {
		uint16_t from;
		uint16_t to;
	};

	struct test_port_range ranges[] = {
		{.from = 1000, .to = 2000},
	};
	const size_t ranges_count = sizeof(ranges) / sizeof(ranges[0]);

	// Test ports at boundaries
	const uint16_t test_ports[] = {
		1000, // Exact start - should match
		1001, // Just after start - should match
		1999, // Just before end - should match
		2000, // Exact end - should match
		999,  // One before start - should NOT match
		2001, // One after end - should NOT match
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

	// Build rules
	struct filter_rule rules[ranges_count];
	struct filter_rule_builder builders[ranges_count];
	for (size_t range_idx = 0; range_idx < ranges_count; ++range_idx) {
		builder_init(&builders[range_idx]);
		if (sign == src) {
			builder_add_port_src_range(
				&builders[range_idx],
				ranges[range_idx].from,
				ranges[range_idx].to
			);
		} else {
			builder_add_port_dst_range(
				&builders[range_idx],
				ranges[range_idx].from,
				ranges[range_idx].to
			);
		}
		rules[range_idx] =
			build_rule(&builders[range_idx], (range_idx + 1));
	}

	// Expected: first 4 match, last 2 don't
	uint32_t expected_counts[] = {1, 1, 1, 1, 0, 0};
	struct value_range *expected_ranges[test_ports_count];
	for (size_t i = 0; i < test_ports_count; ++i) {
		expected_ranges[i] = malloc(sizeof(struct value_range));
		expected_ranges[i]->count = expected_counts[i];
		expected_ranges[i]->values = malloc(sizeof(uint32_t) * 2);
		if (expected_counts[i] > 0) {
			expected_ranges[i]->values[0] = 1;
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
			&filter,
			sign_fast_src_compile,
			rules,
			ranges_count,
			&mctx
		);
	} else {
		res = FILTER_INIT(
			&filter,
			sign_fast_dst_compile,
			rules,
			ranges_count,
			&mctx
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

static int
test_single_port_ranges(void *arena, enum filter_sign sign) {
	assert(sign == src || sign == dst);
	const char *sign_name = filter_sign_to_string(sign);

	LOG(INFO, "=== Test Single Port Ranges: %s ===", sign_name);

	// Define single-port ranges (from == to)
	struct test_port_range {
		uint16_t from;
		uint16_t to;
	};

	struct test_port_range ranges[] = {
		{.from = 80, .to = 80},	    // HTTP
		{.from = 443, .to = 443},   // HTTPS
		{.from = 22, .to = 22},	    // SSH
		{.from = 3306, .to = 3306}, // MySQL
	};
	const size_t ranges_count = sizeof(ranges) / sizeof(ranges[0]);

	// Test ports
	const uint16_t test_ports[] = {
		80,   // Exact match rule 1
		443,  // Exact match rule 2
		22,   // Exact match rule 3
		3306, // Exact match rule 4
		81,   // One off from rule 1 - no match
		442,  // One off from rule 2 - no match
		21,   // One off from rule 3 - no match
		3307, // One off from rule 4 - no match
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

	// Build rules
	struct filter_rule rules[ranges_count];
	struct filter_rule_builder builders[ranges_count];
	for (size_t range_idx = 0; range_idx < ranges_count; ++range_idx) {
		builder_init(&builders[range_idx]);
		if (sign == src) {
			builder_add_port_src_range(
				&builders[range_idx],
				ranges[range_idx].from,
				ranges[range_idx].to
			);
		} else {
			builder_add_port_dst_range(
				&builders[range_idx],
				ranges[range_idx].from,
				ranges[range_idx].to
			);
		}
		rules[range_idx] =
			build_rule(&builders[range_idx], (range_idx + 1));
	}

	// Expected: first 4 match their respective rules, last 4 don't match
	uint32_t expected_actions[][1] = {
		{1},
		{2},
		{3},
		{4},
		{0},
		{0},
		{0},
		{0},
	};
	uint32_t expected_counts[] = {1, 1, 1, 1, 0, 0, 0, 0};

	struct value_range *expected_ranges[test_ports_count];
	for (size_t i = 0; i < test_ports_count; ++i) {
		expected_ranges[i] = malloc(sizeof(struct value_range));
		expected_ranges[i]->count = expected_counts[i];
		expected_ranges[i]->values = malloc(sizeof(uint32_t) * 2);
		if (expected_counts[i] > 0) {
			expected_ranges[i]->values[0] = expected_actions[i][0];
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
			&filter,
			sign_fast_src_compile,
			rules,
			ranges_count,
			&mctx
		);
	} else {
		res = FILTER_INIT(
			&filter,
			sign_fast_dst_compile,
			rules,
			ranges_count,
			&mctx
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

static int
test_adjacent_ranges(void *arena, enum filter_sign sign) {
	assert(sign == src || sign == dst);
	const char *sign_name = filter_sign_to_string(sign);

	LOG(INFO, "=== Test Adjacent Ranges: %s ===", sign_name);

	// Define adjacent non-overlapping port ranges
	// 1000-1999 and 2000-2999
	struct test_port_range {
		uint16_t from;
		uint16_t to;
	};

	struct test_port_range ranges[] = {
		{.from = 1000, .to = 1999},
		{.from = 2000, .to = 2999},
	};
	const size_t ranges_count = sizeof(ranges) / sizeof(ranges[0]);

	// Test ports at boundaries
	const uint16_t test_ports[] = {
		1000, // Start of first range
		1999, // End of first range
		2000, // Start of second range
		2999, // End of second range
		1500, // Middle of first range
		2500, // Middle of second range
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

	// Build rules
	struct filter_rule rules[ranges_count];
	struct filter_rule_builder builders[ranges_count];
	for (size_t range_idx = 0; range_idx < ranges_count; ++range_idx) {
		builder_init(&builders[range_idx]);
		if (sign == src) {
			builder_add_port_src_range(
				&builders[range_idx],
				ranges[range_idx].from,
				ranges[range_idx].to
			);
		} else {
			builder_add_port_dst_range(
				&builders[range_idx],
				ranges[range_idx].from,
				ranges[range_idx].to
			);
		}
		rules[range_idx] =
			build_rule(&builders[range_idx], (range_idx + 1));
	}

	// Expected: ports 0,1,4 match rule 1; ports 2,3,5 match rule 2
	uint32_t expected_actions[][1] = {
		{1}, // Port 1000
		{1}, // Port 1999
		{2}, // Port 2000
		{2}, // Port 2999
		{1}, // Port 1500
		{2}, // Port 2500
	};
	uint32_t expected_counts[] = {1, 1, 1, 1, 1, 1};

	struct value_range *expected_ranges[test_ports_count];
	for (size_t i = 0; i < test_ports_count; ++i) {
		expected_ranges[i] = malloc(sizeof(struct value_range));
		expected_ranges[i]->count = expected_counts[i];
		expected_ranges[i]->values = malloc(sizeof(uint32_t) * 2);
		expected_ranges[i]->values[0] = expected_actions[i][0];
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
			&filter,
			sign_fast_src_compile,
			rules,
			ranges_count,
			&mctx
		);
	} else {
		res = FILTER_INIT(
			&filter,
			sign_fast_dst_compile,
			rules,
			ranges_count,
			&mctx
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

static int
test_extreme_ports(void *arena, enum filter_sign sign) {
	assert(sign == src || sign == dst);
	const char *sign_name = filter_sign_to_string(sign);

	LOG(INFO, "=== Test Extreme Ports: %s ===", sign_name);

	// Test extreme port values (0, 65535, and ranges including them)
	struct test_port_range {
		uint16_t from;
		uint16_t to;
	};

	struct test_port_range ranges[] = {
		{.from = 0, .to = 100},	      // Includes port 0
		{.from = 65400, .to = 65535}, // Includes port 65535
		{.from = 0, .to = 65535},     // Full range
	};
	const size_t ranges_count = sizeof(ranges) / sizeof(ranges[0]);

	// Test ports
	const uint16_t test_ports[] = {
		0,     // Minimum port - matches rules 1, 3
		1,     // Just above min - matches rules 1, 3
		100,   // End of first range - matches rules 1, 3
		101,   // Just after first range - matches rule 3 only
		65400, // Start of second range - matches rules 2, 3
		65534, // Just before max - matches rules 2, 3
		65535, // Maximum port - matches rules 2, 3
		500,   // Middle port - matches rule 3 only
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

	// Build rules
	struct filter_rule rules[ranges_count];
	struct filter_rule_builder builders[ranges_count];
	for (size_t range_idx = 0; range_idx < ranges_count; ++range_idx) {
		builder_init(&builders[range_idx]);
		if (sign == src) {
			builder_add_port_src_range(
				&builders[range_idx],
				ranges[range_idx].from,
				ranges[range_idx].to
			);
		} else {
			builder_add_port_dst_range(
				&builders[range_idx],
				ranges[range_idx].from,
				ranges[range_idx].to
			);
		}
		rules[range_idx] =
			build_rule(&builders[range_idx], (range_idx + 1));
	}

	// Expected matches
	uint32_t expected_actions[][3] = {
		{1, 0, 0}, // Port 0: rules 1
		{1, 0, 0}, // Port 1: rules 1
		{1, 0, 0}, // Port 100: rules 1
		{3, 0, 0}, // Port 101: rule 3
		{2, 0, 0}, // Port 65400: rules 2
		{2, 0, 0}, // Port 65534: rules 2
		{2, 0, 0}, // Port 65535: rules 2
		{3, 0, 0}, // Port 500: rule 3
	};
	uint32_t expected_counts[] = {1, 1, 1, 1, 1, 1, 1, 1};

	struct value_range *expected_ranges[test_ports_count];
	for (size_t i = 0; i < test_ports_count; ++i) {
		expected_ranges[i] = malloc(sizeof(struct value_range));
		expected_ranges[i]->count = expected_counts[i];
		expected_ranges[i]->values = malloc(sizeof(uint32_t) * 3);
		for (size_t j = 0; j < expected_counts[i]; ++j) {
			expected_ranges[i]->values[j] = expected_actions[i][j];
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
			&filter,
			sign_fast_src_compile,
			rules,
			ranges_count,
			&mctx
		);
	} else {
		res = FILTER_INIT(
			&filter,
			sign_fast_dst_compile,
			rules,
			ranges_count,
			&mctx
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

	// Corner case tests
	++tests;
	if (test_no_match(arena, src) != 0) {
		LOG(ERROR, "test_no_match (src) failed");
		++failed;
	}

	++tests;
	if (test_no_match(arena, dst) != 0) {
		LOG(ERROR, "test_no_match (dst) failed");
		++failed;
	}

	++tests;
	if (test_overlapping_ranges(arena, src) != 0) {
		LOG(ERROR, "test_overlapping_ranges (src) failed");
		++failed;
	}

	++tests;
	if (test_overlapping_ranges(arena, dst) != 0) {
		LOG(ERROR, "test_overlapping_ranges (dst) failed");
		++failed;
	}

	++tests;
	if (test_boundary_conditions(arena, src) != 0) {
		LOG(ERROR, "test_boundary_conditions (src) failed");
		++failed;
	}

	++tests;
	if (test_boundary_conditions(arena, dst) != 0) {
		LOG(ERROR, "test_boundary_conditions (dst) failed");
		++failed;
	}

	++tests;
	if (test_single_port_ranges(arena, src) != 0) {
		LOG(ERROR, "test_single_port_ranges (src) failed");
		++failed;
	}

	++tests;
	if (test_single_port_ranges(arena, dst) != 0) {
		LOG(ERROR, "test_single_port_ranges (dst) failed");
		++failed;
	}

	++tests;
	if (test_adjacent_ranges(arena, src) != 0) {
		LOG(ERROR, "test_adjacent_ranges (src) failed");
		++failed;
	}

	++tests;
	if (test_adjacent_ranges(arena, dst) != 0) {
		LOG(ERROR, "test_adjacent_ranges (dst) failed");
		++failed;
	}

	++tests;
	if (test_extreme_ports(arena, src) != 0) {
		LOG(ERROR, "test_extreme_ports (src) failed");
		++failed;
	}

	++tests;
	if (test_extreme_ports(arena, dst) != 0) {
		LOG(ERROR, "test_extreme_ports (dst) failed");
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
