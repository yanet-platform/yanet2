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

FILTER_COMPILER_DECLARE(sign_fast_src_dst, net4_fast_src, net4_fast_dst);
FILTER_QUERY_DECLARE(sign_fast_src_dst, net4_fast_src, net4_fast_dst);

FILTER_COMPILER_DECLARE(sign_fast_src, net4_fast_src);
FILTER_QUERY_DECLARE(sign_fast_src, net4_fast_src);

FILTER_COMPILER_DECLARE(sign_fast_dst, net4_fast_dst);
FILTER_QUERY_DECLARE(sign_fast_dst, net4_fast_dst);

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

////////////////////////////////////////////////////////////////////////////////]

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

static uint32_t
prefix_mask(uint32_t prefix) {
	uint32_t mask = (uint32_t)(-1) ^ ((1 << (32 - prefix)) - 1);
	return rte_cpu_to_be_32(mask);
}

////////////////////////////////////////////////////////////////////////////////

struct test_net {
	uint8_t addr[4];
	size_t prefix;
};

enum { arena_size = 1 << 28 };

static int
test_basic(void *arena, enum filter_sign sign) {
	assert(sign == src || sign == dst);
	const char *sign_name = filter_sign_to_string(sign);

	LOG(INFO, "=== Test Basic: %s ===", sign_name);

	const uint8_t checks[] = {10,  20,  30,	 79,  80,  87,	88,
				  89,  91,  92,	 95,  96,  100, 103,
				  105, 110, 111, 116, 119, 128, 143};
	const size_t checks_count = sizeof(checks) / sizeof(checks[0]);
	struct packet *packets[checks_count];
	for (size_t i = 0; i < checks_count; ++i) {
		packets[i] = malloc(sizeof(struct packet));
		uint8_t ip[4] = {0, 0, 0, checks[i]};
		int fill_result = fill_packet_net4(
			packets[i], ip, ip, 0, 0, IPPROTO_UDP, 0
		);
		TEST_ASSERT_EQUAL(
			fill_result,
			0,
			"failed to fill packet at index %zu (ip=0.0.0.%u)",
			i,
			checks[i]
		);
	}

	struct test_net nets[] = {
		{.addr = {0, 0, 0, 96}, // [96, 103]
		 .prefix = 29},
		{
			.addr = {0, 0, 0, 104}, // [96, 111]
			.prefix = 28,
		},
		{.addr = {0, 0, 0, 90}, // [80, 95]
		 .prefix = 28},
		{.addr = {0, 0, 0, 90}, // [88, 91]
		 .prefix = 30},
		{.addr = {0, 0, 0, 117}, // [116, 119]
		 .prefix = 30},
		{.addr = {0, 0, 0, 128}, // [128, 143]
		 .prefix = 28}
	};
	const size_t nets_count = sizeof(nets) / sizeof(nets[0]);

	struct value_range *expected_ranges[checks_count];
	for (size_t i = 0; i < checks_count; ++i) {
		expected_ranges[i] = malloc(sizeof(struct value_range));
		expected_ranges[i]->count = 0;
		expected_ranges[i]->values =
			malloc(sizeof(uint32_t) * nets_count); // reserve
	}

	struct filter_rule rules[nets_count];
	struct filter_rule_builder builders[nets_count];
	for (size_t net_idx = 0; net_idx < nets_count; ++net_idx) {
		struct filter_rule_builder *builder = &builders[net_idx];
		builder_init(builder);

		builder->net4_dst_count = builder->net4_src_count = 1;

		uint32_t mask = prefix_mask(nets[net_idx].prefix);

		memcpy(builder->net4_dst[0].addr, nets[net_idx].addr, 4);
		memcpy(builder->net4_dst[0].mask, &mask, 4);

		memcpy(builder->net4_src[0].addr, nets[net_idx].addr, 4);
		memcpy(builder->net4_src[0].mask, &mask, 4);

		rules[net_idx] = build_rule(
			builder, (net_idx + 1) | ACTION_NON_TERMINATE
		);

		uint8_t from = nets[net_idx].addr[3] & mask;
		uint8_t to = nets[net_idx].addr[3] | ~mask;

		for (size_t check_idx = 0; check_idx < checks_count;
		     ++check_idx) {
			if (from <= checks[check_idx] &&
			    checks[check_idx] <= to) {
				expected_ranges[check_idx]->values
					[expected_ranges[check_idx]->count++] =
					(net_idx + 1) | ACTION_NON_TERMINATE;
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
			&filter, sign_fast_src, rules, nets_count, &mctx
		);
	} else {
		res = FILTER_INIT(
			&filter, sign_fast_dst, rules, nets_count, &mctx
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
test_multiple_nets_per_rule(void *arena, enum filter_sign sign) {
	assert(sign == src || sign == dst);
	const char *sign_name = filter_sign_to_string(sign);

	LOG(INFO, "=== Test Multiple Nets Per Rule: %s ===", sign_name);

	// Test packets with specific IPs
	const uint8_t test_ips[][4] = {
		{192, 168, 1, 10}, // Rule 1, Net A
		{192, 168, 2, 20}, // Rule 1, Net B
		{192, 168, 3, 30}, // Rule 1, Net C
		{10, 0, 1, 10},	   // Rule 2, Net D
		{10, 1, 2, 20},	   // Rule 2, Net E
		{172, 16, 1, 10},  // Rule 3, Net F
		{172, 17, 2, 20},  // Rule 3, Net G
		{8, 8, 8, 8},	   // No match
	};
	const size_t test_ips_count = sizeof(test_ips) / sizeof(test_ips[0]);

	struct packet *packets[test_ips_count];
	for (size_t i = 0; i < test_ips_count; ++i) {
		packets[i] = malloc(sizeof(struct packet));
		int fill_result = fill_packet_net4(
			packets[i],
			test_ips[i],
			test_ips[i],
			0,
			0,
			IPPROTO_UDP,
			0
		);
		TEST_ASSERT_EQUAL(
			fill_result, 0, "failed to fill packet at index %zu", i
		);
	}

	// Define networks for each rule
	// Rule 1: 3 networks (192.168.1.0/24, 192.168.2.0/24, 192.168.3.0/24)
	struct test_net rule1_nets[] = {
		{.addr = {192, 168, 1, 0}, .prefix = 24}, // Net A
		{.addr = {192, 168, 2, 0}, .prefix = 24}, // Net B
		{.addr = {192, 168, 3, 0}, .prefix = 24}, // Net C
	};
	const size_t rule1_nets_count =
		sizeof(rule1_nets) / sizeof(rule1_nets[0]);

	// Rule 2: 2 networks (10.0.0.0/16, 10.1.0.0/16)
	struct test_net rule2_nets[] = {
		{.addr = {10, 0, 0, 0}, .prefix = 16}, // Net D
		{.addr = {10, 1, 0, 0}, .prefix = 16}, // Net E
	};
	const size_t rule2_nets_count =
		sizeof(rule2_nets) / sizeof(rule2_nets[0]);

	// Rule 3: 2 networks (172.16.0.0/20, 172.17.0.0/20)
	struct test_net rule3_nets[] = {
		{.addr = {172, 16, 0, 0}, .prefix = 20}, // Net F
		{.addr = {172, 17, 0, 0}, .prefix = 20}, // Net G
	};
	const size_t rule3_nets_count =
		sizeof(rule3_nets) / sizeof(rule3_nets[0]);

	// Expected actions for each test packet
	// Packets 0-2 match rule 1, packets 3-4 match rule 2, packets 5-6 match
	// rule 3, packet 7 matches nothing
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

	struct value_range *expected_ranges[test_ips_count];
	for (size_t i = 0; i < test_ips_count; ++i) {
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

	// Rule 1: Add 3 networks
	builder_init(&builders[0]);
	for (size_t i = 0; i < rule1_nets_count; ++i) {
		uint32_t mask = prefix_mask(rule1_nets[i].prefix);
		if (sign == src) {
			builder_add_net4_src(
				&builders[0],
				rule1_nets[i].addr,
				(const uint8_t *)&mask
			);
		} else {
			builder_add_net4_dst(
				&builders[0],
				rule1_nets[i].addr,
				(const uint8_t *)&mask
			);
		}
	}
	rules[0] = build_rule(&builders[0], 1 | ACTION_NON_TERMINATE);

	// Rule 2: Add 2 networks
	builder_init(&builders[1]);
	for (size_t i = 0; i < rule2_nets_count; ++i) {
		uint32_t mask = prefix_mask(rule2_nets[i].prefix);
		if (sign == src) {
			builder_add_net4_src(
				&builders[1],
				rule2_nets[i].addr,
				(const uint8_t *)&mask
			);
		} else {
			builder_add_net4_dst(
				&builders[1],
				rule2_nets[i].addr,
				(const uint8_t *)&mask
			);
		}
	}
	rules[1] = build_rule(&builders[1], 2 | ACTION_NON_TERMINATE);

	// Rule 3: Add 2 networks
	builder_init(&builders[2]);
	for (size_t i = 0; i < rule3_nets_count; ++i) {
		uint32_t mask = prefix_mask(rule3_nets[i].prefix);
		if (sign == src) {
			builder_add_net4_src(
				&builders[2],
				rule3_nets[i].addr,
				(const uint8_t *)&mask
			);
		} else {
			builder_add_net4_dst(
				&builders[2],
				rule3_nets[i].addr,
				(const uint8_t *)&mask
			);
		}
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
		&filter, sign, packets, test_ips_count, expected_ranges
	);
	TEST_ASSERT_SUCCESS(res, "some checks failed");

	for (size_t i = 0; i < test_ips_count; ++i) {
		free(expected_ranges[i]->values);
		free(expected_ranges[i]);
		free_packet(packets[i]);
		free(packets[i]);
	}

	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////

static int
is_ip_from_net(struct net4 *net, uint8_t *ip) {
	for (size_t i = 0; i < 4; i++) {
		if ((ip[i] & net->mask[i]) != (net->addr[i] & net->mask[i])) {
			return 0;
		}
	}
	return 1;
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

		for (size_t i = 0; i < 2; ++i) {
			uint8_t prefix_len = 16 + rng_next(&rng) % 17;

			uint8_t a = 1 + rng_next(&rng) % 10;
			uint8_t b = 1 + rng_next(&rng) % 10;
			uint8_t c = 128 + rng_next(&rng) % 10;
			uint8_t d = 128 + rng_next(&rng) % 10;
			uint32_t mask = prefix_mask(prefix_len);
			uint8_t addr[4] = {a, b, c, d};
			if (i == 0) {
				builder_add_net4_src(
					builder, addr, (const uint8_t *)&mask
				);
			} else {
				builder_add_net4_dst(
					builder, addr, (const uint8_t *)&mask
				);
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

	// Initialize both filters
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
	for (size_t packet_idx = 0; packet_idx < num_packets; ++packet_idx) {
		packets[packet_idx] = malloc(sizeof(struct packet));
		uint8_t src_ip[4], dst_ip[4];
		for (size_t i = 0; i < 2; ++i) {
			uint8_t a = 1 + rng_next(&rng) % 12;
			uint8_t b = 1 + rng_next(&rng) % 10;
			uint8_t c = 128 + rng_next(&rng) % 12;
			uint8_t d = 128 + rng_next(&rng) % 10;
			if (i == 0) {
				src_ip[0] = a;
				src_ip[1] = b;
				src_ip[2] = c;
				src_ip[3] = d;
			} else {
				dst_ip[0] = a;
				dst_ip[1] = b;
				dst_ip[2] = c;
				dst_ip[3] = d;
			}
		}
		int fill_result = fill_packet_net4(
			packets[packet_idx],
			src_ip,
			dst_ip,
			0,
			0,
			IPPROTO_UDP,
			0
		);
		assert(fill_result == 0);
		const int check_src = sign == src || sign == src_dst;
		const int check_dst = sign == dst || sign == src_dst;
		for (size_t rule_idx = 0; rule_idx < num_rules; ++rule_idx) {
			struct filter_rule *rule = &rules[rule_idx];
			int ok = 1;
			if (check_src &&
			    !is_ip_from_net(&rule->net4.srcs[0], src_ip)) {
				ok = 0;
			}
			if (check_dst &&
			    !is_ip_from_net(&rule->net4.dsts[0], dst_ip)) {
				ok = 0;
			}
			if (ok) {
				struct value_range *range =
					expected_ranges[packet_idx];
				range->values[range->count++] =
					(rule_idx + 1) | ACTION_NON_TERMINATE;
			}
		}
	}

	int result = query_and_expect_actions(
		&filter, sign, packets, num_packets, expected_ranges
	);
	TEST_ASSERT_SUCCESS(
		result, "failed to query packets and compare with old filter"
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
	if (test_multiple_nets_per_rule(arena, src) != 0) {
		LOG(ERROR, "Test_multiple_nets_per_rule (src) failed");
		++failed;
	}

	++tests;
	if (test_multiple_nets_per_rule(arena, dst) != 0) {
		LOG(ERROR, "Test_multiple_nets_per_rule (dst) failed");
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