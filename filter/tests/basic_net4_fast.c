
#include "common/rng.h"
#include "common/test_assert.h"
#include "filter/compiler.h"
#include "filter/filter.h"
#include "filter/query.h"

#include "filter/tests/helpers.h"
#include "lib/utils/packet.h"

#include "logging/log.h"
#include "rte_byteorder.h"
#include <assert.h>
#include <netinet/in.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <stdlib.h>
#include <time.h>

////////////////////////////////////////////////////////////////////////////////

// Declare both fast and old implementations for comparison
FILTER_COMPILER_DECLARE(sign_net4_fast, net4_fast_src, net4_fast_dst);
FILTER_QUERY_DECLARE(sign_net4_fast, net4_fast_src, net4_fast_dst);

FILTER_COMPILER_DECLARE(sign_net4_old, net4_src, net4_dst);
FILTER_QUERY_DECLARE(sign_net4_old, net4_src, net4_dst);

// Separate declarations for testing src and dst independently
FILTER_COMPILER_DECLARE(sign_src_only, net4_fast_src);
FILTER_QUERY_DECLARE(sign_src_only, net4_fast_src);

FILTER_COMPILER_DECLARE(sign_dst_only, net4_fast_dst);
FILTER_QUERY_DECLARE(sign_dst_only, net4_fast_dst);

////////////////////////////////////////////////////////////////////////////////

static int
query_and_expect_action(
	struct filter *filter,
	uint8_t sip[NET4_LEN],
	uint8_t dip[NET4_LEN],
	uint32_t expected
) {
	struct packet p = {0};
	int res = fill_packet_net4(&p, sip, dip, 0, 0, IPPROTO_UDP, 0);
	TEST_ASSERT_EQUAL(res, 0, "failed to fill packet net4");

	struct packet *packet_ptr = &p;
	struct value_range *actions;
	FILTER_QUERY(filter, sign_net4_fast, &packet_ptr, &actions, 1);

	free_packet(&p);
	TEST_ASSERT(actions->count >= 1, "at least one action expected");
	TEST_ASSERT_EQUAL(
		expected, ADDR_OF(&actions->values)[0], "expected mismatch"
	);
	return TEST_SUCCESS;
}

static int
query_and_expect_no_action(
	struct filter *filter, uint8_t sip[NET4_LEN], uint8_t dip[NET4_LEN]
) {
	struct packet p = {0};
	int res = fill_packet_net4(&p, sip, dip, 0, 0, IPPROTO_UDP, 0);
	TEST_ASSERT_EQUAL(res, 0, "failed to fill packet net4");
	struct packet *packet_ptr = &p;
	struct value_range *actions;
	FILTER_QUERY(filter, sign_net4_fast, &packet_ptr, &actions, 1);
	free_packet(&p);
	TEST_ASSERT_EQUAL(actions->count, 0, "got unexpected actions");
	return TEST_SUCCESS;
}

static int
query_src_only_and_expect_action(
	struct filter *filter,
	uint8_t sip[NET4_LEN],
	uint8_t dip[NET4_LEN],
	uint32_t expected
) {
	struct packet p = {0};
	int res = fill_packet_net4(&p, sip, dip, 0, 0, IPPROTO_UDP, 0);
	TEST_ASSERT_EQUAL(res, 0, "failed to fill packet net4");

	struct packet *packet_ptr = &p;
	struct value_range *actions;
	FILTER_QUERY(filter, sign_src_only, &packet_ptr, &actions, 1);

	free_packet(&p);
	TEST_ASSERT(actions->count >= 1, "at least one action expected");
	TEST_ASSERT_EQUAL(
		ADDR_OF(&actions->values)[0], expected, "expected mismatch"
	);
	return TEST_SUCCESS;
}

static int
query_src_only_and_expect_no_action(
	struct filter *filter, uint8_t sip[NET4_LEN], uint8_t dip[NET4_LEN]
) {
	struct packet p = {0};
	int res = fill_packet_net4(&p, sip, dip, 0, 0, IPPROTO_UDP, 0);
	TEST_ASSERT_EQUAL(res, 0, "failed to fill packet net4");
	struct packet *packet_ptr = &p;
	struct value_range *actions;
	FILTER_QUERY(filter, sign_src_only, &packet_ptr, &actions, 1);
	free_packet(&p);
	TEST_ASSERT_EQUAL(actions->count, 0, "got unexpected actions");
	return TEST_SUCCESS;
}

static int
query_dst_only_and_expect_action(
	struct filter *filter,
	uint8_t sip[NET4_LEN],
	uint8_t dip[NET4_LEN],
	uint32_t expected
) {
	struct packet p = {0};
	int res = fill_packet_net4(&p, sip, dip, 0, 0, IPPROTO_UDP, 0);
	TEST_ASSERT_EQUAL(res, 0, "failed to fill packet net4");

	struct packet *packet_ptr = &p;
	struct value_range *actions;
	FILTER_QUERY(filter, sign_dst_only, &packet_ptr, &actions, 1);

	free_packet(&p);
	TEST_ASSERT(actions->count >= 1, "at least one action expected");
	TEST_ASSERT_EQUAL(
		expected, ADDR_OF(&actions->values)[0], "expected mismatch"
	);
	return TEST_SUCCESS;
}

static int
query_dst_only_and_expect_no_action(
	struct filter *filter, uint8_t sip[NET4_LEN], uint8_t dip[NET4_LEN]
) {
	struct packet p = {0};
	int res = fill_packet_net4(&p, sip, dip, 0, 0, IPPROTO_UDP, 0);
	TEST_ASSERT_EQUAL(res, 0, "failed to fill packet net4");
	struct packet *packet_ptr = &p;
	struct value_range *actions;
	FILTER_QUERY(filter, sign_dst_only, &packet_ptr, &actions, 1);
	free_packet(&p);
	TEST_ASSERT_EQUAL(actions->count, 0, "got unexpected actions");
	return TEST_SUCCESS;
}

#define MEMORY_SIZE (1 << 26)

// Original test - combined src and dst
static int
test_combined_basic(void *memory) {
	LOG(INFO, "=== Test: Combined src and dst basic ===");
	
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, MEMORY_SIZE);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	struct filter_rule_builder builder1;
	builder_init(&builder1);
	uint8_t *src_net = ip(192, 255, 168, 0);
	uint8_t *src_mask = ip(255, 255, 255, 0);
	uint8_t *dst_net = ip(192, 255, 168, 0);
	uint8_t *dst_mask = ip(255, 255, 255, 0);

	builder_add_net4_src(&builder1, src_net, src_mask);
	builder_add_net4_dst(&builder1, dst_net, dst_mask);
	struct filter_rule action1 = build_rule(&builder1, 1);

	struct filter filter;
	res = FILTER_INIT(&filter, sign_net4_fast, &action1, 1, &memory_context);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize filter");

	res = query_and_expect_action(
		&filter, ip(192, 255, 168, 1), ip(192, 255, 168, 10), 1
	);
	TEST_ASSERT_SUCCESS(res, "failed to query matching packet");

	res = query_and_expect_no_action(
		&filter, ip(195, 255, 168, 1), ip(192, 255, 168, 10)
	);
	TEST_ASSERT_SUCCESS(res, "failed to query src mismatch");

	res = query_and_expect_no_action(
		&filter, ip(192, 255, 168, 10), ip(195, 255, 168, 1)
	);
	TEST_ASSERT_SUCCESS(res, "failed to query dst mismatch");

	FILTER_FREE(&filter, sign_net4_fast);

	return TEST_SUCCESS;
}

// Test source classifier only with multiple networks
static int
test_src_only_multiple_networks(void *memory) {
	(void)test_combined_basic;
	LOG(INFO, "=== Test: Source only - multiple networks ===");
	
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, MEMORY_SIZE);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	// Create 3 rules with different source networks
	struct filter_rule rules[3];
	
	// Rule 1: 10.0.0.0/8 -> action 1
	struct filter_rule_builder builder1;
	builder_init(&builder1);
	builder_add_net4_src(&builder1, ip(10, 0, 0, 0), ip(255, 0, 0, 0));
	rules[0] = build_rule(&builder1, 1);

	// Rule 2: 172.16.0.0/16 -> action 2
	struct filter_rule_builder builder2;
	builder_init(&builder2);
	builder_add_net4_src(&builder2, ip(172, 16, 0, 0), ip(255, 255, 0, 0));
	rules[1] = build_rule(&builder2, 2);

	// Rule 3: 192.168.1.0/24 -> action 3
	struct filter_rule_builder builder3;
	builder_init(&builder3);
	builder_add_net4_src(&builder3, ip(192, 168, 1, 0), ip(255, 255, 255, 0));
	rules[2] = build_rule(&builder3, 3);

	struct filter filter;
	res = FILTER_INIT(&filter, sign_src_only, rules, 3, &memory_context);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize filter");

	// Test each network
	res = query_src_only_and_expect_action(
		&filter, ip(10, 5, 10, 20), ip(1, 2, 3, 4), 1
	);
	TEST_ASSERT_SUCCESS(res, "failed to match 10.0.0.0/8");

	res = query_src_only_and_expect_action(
		&filter, ip(172, 16, 50, 100), ip(1, 2, 3, 4), 2
	);
	TEST_ASSERT_SUCCESS(res, "failed to match 172.16.0.0/16");

	res = query_src_only_and_expect_action(
		&filter, ip(192, 168, 1, 50), ip(1, 2, 3, 4), 3
	);
	TEST_ASSERT_SUCCESS(res, "failed to match 192.168.1.0/24");

	// Test non-matching
	res = query_src_only_and_expect_no_action(
		&filter, ip(8, 8, 8, 8), ip(1, 2, 3, 4)
	);
	TEST_ASSERT_SUCCESS(res, "incorrectly matched non-matching IP");

	FILTER_FREE(&filter, sign_src_only);

	return TEST_SUCCESS;
}

// Test destination classifier only with multiple networks
static int
test_dst_only_multiple_networks(void *memory) {
	LOG(INFO, "=== Test: Destination only - multiple networks ===");
	
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, MEMORY_SIZE);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	// Create 3 rules with different destination networks
	struct filter_rule rules[3];
	
	// Rule 1: dst 10.0.0.0/8 -> action 1
	struct filter_rule_builder builder1;
	builder_init(&builder1);
	builder_add_net4_dst(&builder1, ip(10, 0, 0, 0), ip(255, 0, 0, 0));
	rules[0] = build_rule(&builder1, 1);

	// Rule 2: dst 172.16.0.0/12 -> action 2
	struct filter_rule_builder builder2;
	builder_init(&builder2);
	builder_add_net4_dst(&builder2, ip(172, 16, 0, 0), ip(255, 240, 0, 0));
	rules[1] = build_rule(&builder2, 2);

	// Rule 3: dst 192.168.0.0/16 -> action 3
	struct filter_rule_builder builder3;
	builder_init(&builder3);
	builder_add_net4_dst(&builder3, ip(192, 168, 0, 0), ip(255, 255, 0, 0));
	rules[2] = build_rule(&builder3, 3);

	struct filter filter;
	res = FILTER_INIT(&filter, sign_dst_only, rules, 3, &memory_context);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize filter");

	// Test each network
	res = query_dst_only_and_expect_action(
		&filter, ip(1, 2, 3, 4), ip(10, 100, 200, 50), 1
	);
	TEST_ASSERT_SUCCESS(res, "failed to match dst 10.0.0.0/8");

	res = query_dst_only_and_expect_action(
		&filter, ip(1, 2, 3, 4), ip(172, 20, 5, 10), 2
	);
	TEST_ASSERT_SUCCESS(res, "failed to match dst 172.16.0.0/12");

	res = query_dst_only_and_expect_action(
		&filter, ip(1, 2, 3, 4), ip(192, 168, 100, 200), 3
	);
	TEST_ASSERT_SUCCESS(res, "failed to match dst 192.168.0.0/16");

	// Test non-matching
	res = query_dst_only_and_expect_no_action(
		&filter, ip(1, 2, 3, 4), ip(8, 8, 8, 8)
	);
	TEST_ASSERT_SUCCESS(res, "incorrectly matched non-matching dst IP");

	FILTER_FREE(&filter, sign_dst_only);

	return TEST_SUCCESS;
}

// Test edge cases: single IP, boundaries
static int
test_edge_cases(void *memory) {
	(void)test_dst_only_multiple_networks;
	LOG(INFO, "=== Test: Edge cases ===");
	
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, MEMORY_SIZE);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	struct filter_rule rules[3];
	
	// Rule 1: Single IP /32
	struct filter_rule_builder builder1;
	builder_init(&builder1);
	builder_add_net4_src(&builder1, ip(192, 168, 1, 100), ip(255, 255, 255, 255));
	rules[0] = build_rule(&builder1, 1);

	// Rule 2: First IP in /24 range
	struct filter_rule_builder builder2;
	builder_init(&builder2);
	builder_add_net4_src(&builder2, ip(10, 0, 0, 0), ip(255, 255, 255, 0));
	rules[1] = build_rule(&builder2, 2);

	// Rule 3: Adjacent network
	struct filter_rule_builder builder3;
	builder_init(&builder3);
	builder_add_net4_src(&builder3, ip(10, 0, 1, 0), ip(255, 255, 255, 0));
	rules[2] = build_rule(&builder3, 3);

	struct filter filter;
	res = FILTER_INIT(&filter, sign_src_only, rules, 3, &memory_context);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize filter");

	// Test single IP match
	res = query_src_only_and_expect_action(
		&filter, ip(192, 168, 1, 100), ip(1, 2, 3, 4), 1
	);
	TEST_ASSERT_SUCCESS(res, "failed to match single IP");

	// Test single IP non-match (off by one)
	res = query_src_only_and_expect_no_action(
		&filter, ip(192, 168, 1, 101), ip(1, 2, 3, 4)
	);
	TEST_ASSERT_SUCCESS(res, "incorrectly matched IP off by one");

	// Test first IP in range
	res = query_src_only_and_expect_action(
		&filter, ip(10, 0, 0, 0), ip(1, 2, 3, 4), 2
	);
	TEST_ASSERT_SUCCESS(res, "failed to match first IP in range");

	// Test last IP in range
	res = query_src_only_and_expect_action(
		&filter, ip(10, 0, 0, 255), ip(1, 2, 3, 4), 2
	);
	TEST_ASSERT_SUCCESS(res, "failed to match last IP in range");

	// Test just outside range
	res = query_src_only_and_expect_action(
		&filter, ip(10, 0, 1, 0), ip(1, 2, 3, 4), 3
	);
	TEST_ASSERT_SUCCESS(res, "failed to match adjacent network");

	// Test between ranges
	res = query_src_only_and_expect_no_action(
		&filter, ip(10, 0, 2, 0), ip(1, 2, 3, 4)
	);
	TEST_ASSERT_SUCCESS(res, "incorrectly matched IP between ranges");

	FILTER_FREE(&filter, sign_src_only);

	return TEST_SUCCESS;
}

// Test overlapping ranges
static int
test_overlapping_ranges(void *memory) {
	(void)test_edge_cases;
	LOG(INFO, "=== Test: Overlapping ranges ===");
	
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, MEMORY_SIZE);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	struct filter_rule rules[2];
	
	// Rule 1: 192.168.0.0/16 (larger range) -> action 1
	struct filter_rule_builder builder1;
	builder_init(&builder1);
	builder_add_net4_src(&builder1, ip(192, 168, 0, 0), ip(255, 255, 0, 0));
	rules[0] = build_rule(&builder1, 1);

	// Rule 2: 192.168.1.0/24 (subset) -> action 2
	struct filter_rule_builder builder2;
	builder_init(&builder2);
	builder_add_net4_src(&builder2, ip(192, 168, 1, 0), ip(255, 255, 255, 0));
	rules[1] = build_rule(&builder2, 2);

	struct filter filter;
	res = FILTER_INIT(&filter, sign_src_only, rules, 2, &memory_context);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize filter");

	// IP in both ranges should match both actions
	struct packet p = {0};
	res = fill_packet_net4(&p, ip(192, 168, 1, 50), ip(1, 2, 3, 4), 0, 0, IPPROTO_UDP, 0);
	TEST_ASSERT_EQUAL(res, 0, "failed to fill packet");
	
	struct packet *packet_ptr = &p;
	struct value_range *actions;
	FILTER_QUERY(&filter, sign_src_only, &packet_ptr, &actions, 1);
	
	// Should match both rules
	TEST_ASSERT(actions->count >= 1, "expected at least one action");
	LOG(INFO, "Overlapping range matched %zu actions", actions->count);
	
	free_packet(&p);

	// IP only in larger range
	res = query_src_only_and_expect_action(
		&filter, ip(192, 168, 2, 50), ip(1, 2, 3, 4), 1
	);
	TEST_ASSERT_SUCCESS(res, "failed to match larger range only");

	FILTER_FREE(&filter, sign_src_only);

	return TEST_SUCCESS;
}

// Simple debug test with explicit values
static int
test_debug_simple(void *memory) {
	LOG(INFO, "=== Test: Debug simple comparison ===");
	
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, MEMORY_SIZE);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	// Create a single simple rule: 192.168.0.0/16 for both src and dst
	struct filter_rule rules[1];
	struct filter_rule_builder builder;
	builder_init(&builder);
	builder_add_net4_src(&builder, ip(192, 168, 0, 0), ip(255, 255, 0, 0));
	builder_add_net4_dst(&builder, ip(192, 168, 0, 0), ip(255, 255, 0, 0));
	rules[0] = build_rule(&builder, 1);

	// Initialize both filters
	struct filter filter_fast, filter_old;
	res = FILTER_INIT(&filter_fast, sign_net4_fast, rules, 1, &memory_context);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize fast filter");

	res = FILTER_INIT(&filter_old, sign_net4_old, rules, 1, &memory_context);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize old filter");

	// Test a few specific cases
	struct {
		uint8_t sip[4];
		uint8_t dip[4];
		int should_match;
	} test_cases[] = {
		{{192, 168, 1, 1}, {192, 168, 2, 2}, 1},  // Both in range
		{{192, 168, 1, 1}, {10, 0, 0, 1}, 0},     // Src in range, dst not
		{{10, 0, 0, 1}, {192, 168, 2, 2}, 0},     // Src not, dst in range
		{{10, 0, 0, 1}, {10, 0, 0, 2}, 0},        // Both out of range
	};

	for (size_t i = 0; i < sizeof(test_cases) / sizeof(test_cases[0]); i++) {
		struct packet p = {0};
		res = fill_packet_net4(&p, test_cases[i].sip, test_cases[i].dip, 0, 0, IPPROTO_UDP, 0);
		TEST_ASSERT_EQUAL(res, 0, "failed to fill packet");

		struct packet *packet_ptr = &p;
		struct value_range *actions_fast, *actions_old;
		
		FILTER_QUERY(&filter_fast, sign_net4_fast, &packet_ptr, &actions_fast, 1);
		FILTER_QUERY(&filter_old, sign_net4_old, &packet_ptr, &actions_old, 1);

		LOG(INFO, "Test case %zu: sip=%u.%u.%u.%u, dip=%u.%u.%u.%u",
		    i, test_cases[i].sip[0], test_cases[i].sip[1], test_cases[i].sip[2], test_cases[i].sip[3],
		    test_cases[i].dip[0], test_cases[i].dip[1], test_cases[i].dip[2], test_cases[i].dip[3]);
		LOG(INFO, "  Fast: %zu actions, Old: %zu actions, Expected: %s",
		    actions_fast->count, actions_old->count,
		    test_cases[i].should_match ? "match" : "no match");

		if (actions_fast->count != actions_old->count) {
			LOG(ERROR, "  MISMATCH!");
			free_packet(&p);
			FILTER_FREE(&filter_fast, sign_net4_fast);
			FILTER_FREE(&filter_old, sign_net4_old);
			return TEST_FAILED;
		}

		free_packet(&p);
	}

	FILTER_FREE(&filter_fast, sign_net4_fast);
	FILTER_FREE(&filter_old, sign_net4_old);

	LOG(INFO, "Debug test passed");
	return TEST_SUCCESS;
}

// Test with multiple non-overlapping rules
static int
test_debug_multiple_rules(void *memory) {
	(void)test_debug_simple;

	LOG(INFO, "=== Test: Debug multiple non-overlapping rules ===");
	
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, MEMORY_SIZE);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	// Create 3 rules with different networks
	struct filter_rule rules[3];
	struct filter_rule_builder builders[3];
	
	// Rule 1: 10.0.0.0/16 for both src and dst
	builder_init(&builders[0]);
	builder_add_net4_src(&builders[0], ip(10, 0, 0, 0), ip(255, 255, 0, 0));
	builder_add_net4_dst(&builders[0], ip(10, 0, 0, 0), ip(255, 255, 0, 0));
	rules[0] = build_rule(&builders[0], 1);

	// Rule 2: 172.16.0.0/16 for both src and dst
	builder_init(&builders[1]);
	builder_add_net4_src(&builders[1], ip(172, 16, 0, 0), ip(255, 255, 0, 0));
	builder_add_net4_dst(&builders[1], ip(172, 16, 0, 0), ip(255, 255, 0, 0));
	rules[1] = build_rule(&builders[1], 2);

	// Rule 3: 192.168.0.0/24 for both src and dst
	builder_init(&builders[2]);
	builder_add_net4_src(&builders[2], ip(192, 168, 0, 0), ip(255, 255, 255, 0));
	builder_add_net4_dst(&builders[2], ip(192, 168, 0, 0), ip(255, 255, 255, 0));
	rules[2] = build_rule(&builders[2], 3);

	// Initialize both filters
	struct filter filter_fast, filter_old;
	res = FILTER_INIT(&filter_fast, sign_net4_fast, rules, 3, &memory_context);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize fast filter");

	res = FILTER_INIT(&filter_old, sign_net4_old, rules, 3, &memory_context);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize old filter");

	// Test cases
	struct {
		uint8_t sip[4];
		uint8_t dip[4];
		const char *desc;
	} test_cases[] = {
		{{10, 0, 1, 1}, {10, 0, 2, 2}, "Rule 1 match"},
		{{172, 16, 1, 1}, {172, 16, 2, 2}, "Rule 2 match"},
		{{192, 168, 0, 1}, {192, 168, 0, 2}, "Rule 3 match"},
		{{10, 0, 1, 1}, {172, 16, 2, 2}, "Src rule 1, dst rule 2 - no match"},
		{{8, 8, 8, 8}, {8, 8, 4, 4}, "No match"},
	};

	for (size_t i = 0; i < sizeof(test_cases) / sizeof(test_cases[0]); i++) {
		struct packet p = {0};
		res = fill_packet_net4(&p, test_cases[i].sip, test_cases[i].dip, 0, 0, IPPROTO_UDP, 0);
		TEST_ASSERT_EQUAL(res, 0, "failed to fill packet");

		struct packet *packet_ptr = &p;
		struct value_range *actions_fast, *actions_old;
		
		FILTER_QUERY(&filter_fast, sign_net4_fast, &packet_ptr, &actions_fast, 1);
		FILTER_QUERY(&filter_old, sign_net4_old, &packet_ptr, &actions_old, 1);

		LOG(INFO, "Test case %zu (%s): sip=%u.%u.%u.%u, dip=%u.%u.%u.%u",
		    i, test_cases[i].desc,
		    test_cases[i].sip[0], test_cases[i].sip[1], test_cases[i].sip[2], test_cases[i].sip[3],
		    test_cases[i].dip[0], test_cases[i].dip[1], test_cases[i].dip[2], test_cases[i].dip[3]);
		LOG(INFO, "  Fast: %zu actions, Old: %zu actions",
		    actions_fast->count, actions_old->count);

		if (actions_fast->count != actions_old->count) {
			LOG(ERROR, "  MISMATCH!");
			free_packet(&p);
			FILTER_FREE(&filter_fast, sign_net4_fast);
			FILTER_FREE(&filter_old, sign_net4_old);
			return TEST_FAILED;
		}

		free_packet(&p);
	}

	FILTER_FREE(&filter_fast, sign_net4_fast);
	FILTER_FREE(&filter_old, sign_net4_old);

	LOG(INFO, "Multiple rules test passed");
	return TEST_SUCCESS;
}

// Small stress test: 100 iterations, each with 5 random rules and 100 queries
static int
test_debug_stress_small(void *memory) {
	(void)test_debug_multiple_rules;

	LOG(INFO, "=== Test: Debug stress with 100 sets of 5 rules, 100 queries each ===");
	
	srand(12345); // Fixed seed for reproducibility

	size_t total_mismatches = 0;
	size_t total_queries = 0;
	
	for (size_t iteration = 0; iteration < 1000; iteration++) {
		struct block_allocator allocator;
		block_allocator_init(&allocator);
		block_allocator_put_arena(&allocator, memory, MEMORY_SIZE);

		struct memory_context memory_context;
		int res = memory_context_init(&memory_context, "test", &allocator);
		assert(res == 0);

		const size_t num_rules = 100;

		// Generate 5 random rules
		struct filter_rule rules[num_rules];
		struct filter_rule_builder builders[num_rules];
		
	
		for (size_t i = 0; i < num_rules; i++) {
			builder_init(&builders[i]);
			
			// Generate random network with /16 or /24 prefix
			uint8_t prefix_len = (rand() % 2) ? 16 : 24;
			uint8_t a = rand() % 256;
			uint8_t b = rand() % 256;
			uint8_t c = (prefix_len == 24) ? (rand() % 256) : 0;
			
			if (prefix_len == 16) {
				builder_add_net4_src(&builders[i], ip(a, b, 0, 0), ip(255, 255, 0, 0));
				builder_add_net4_dst(&builders[i], ip(a, b, 0, 0), ip(255, 255, 0, 0));
			} else {
				builder_add_net4_src(&builders[i], ip(a, b, c, 0), ip(255, 255, 255, 0));
				builder_add_net4_dst(&builders[i], ip(a, b, c, 0), ip(255, 255, 255, 0));
			}
			
			rules[i] = build_rule(&builders[i], i + 1);
		}

		// Initialize both filters
		struct filter filter_fast, filter_old;
		res = FILTER_INIT(&filter_fast, sign_net4_fast, rules, 5, &memory_context);
		if (res != 0) {
			LOG(ERROR, "Failed to initialize fast filter at iteration %zu", iteration);
			return TEST_FAILED;
		}

		res = FILTER_INIT(&filter_old, sign_net4_old, rules, 5, &memory_context);
		if (res != 0) {
			FILTER_FREE(&filter_fast, sign_net4_fast);
			LOG(ERROR, "Failed to initialize old filter at iteration %zu", iteration);
			return TEST_FAILED;
		}

		// Generate 100 random queries
		for (size_t i = 0; i < 1000; i++) {
			uint8_t sip[4] = {rand() % 256, rand() % 256, rand() % 256, rand() % 256};
			uint8_t dip[4] = {rand() % 256, rand() % 256, rand() % 256, rand() % 256};
			
			struct packet p = {0};
			res = fill_packet_net4(&p, sip, dip, 0, 0, IPPROTO_UDP, 0);
			if (res != 0) {
				free_packet(&p);
				continue;
			}

			struct packet *packet_ptr = &p;
			struct value_range *actions_fast, *actions_old;
			
			FILTER_QUERY(&filter_fast, sign_net4_fast, &packet_ptr, &actions_fast, 1);
			FILTER_QUERY(&filter_old, sign_net4_old, &packet_ptr, &actions_old, 1);

			if (actions_fast->count != actions_old->count) {
				LOG(ERROR, "Mismatch at iteration %zu, query %zu: sip=%u.%u.%u.%u, dip=%u.%u.%u.%u",
				    iteration, i, sip[0], sip[1], sip[2], sip[3], dip[0], dip[1], dip[2], dip[3]);
				LOG(ERROR, "  Fast: %zu actions, Old: %zu actions",
				    actions_fast->count, actions_old->count);
				total_mismatches++;
			}

			total_queries++;
			free_packet(&p);
		}

		FILTER_FREE(&filter_fast, sign_net4_fast);
		FILTER_FREE(&filter_old, sign_net4_old);
	}

	if (total_mismatches > 0) {
		LOG(ERROR, "Found %zu mismatches out of %zu queries across 100 iterations",
		    total_mismatches, total_queries);
		return TEST_FAILED;
	}

	LOG(INFO, "Small stress test passed: %zu/%zu queries matched across 100 iterations",
	    total_queries, total_queries);
	return TEST_SUCCESS;
}

// Test with overlapping rules
static int
test_debug_overlapping(void *memory) {
	LOG(INFO, "=== Test: Debug overlapping rules ===");
	
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, MEMORY_SIZE);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	// Create overlapping rules
	struct filter_rule rules[3];
	struct filter_rule_builder builders[3];
	
	// Rule 1: 192.168.0.0/16 (large range)
	builder_init(&builders[0]);
	builder_add_net4_src(&builders[0], ip(192, 168, 0, 0), ip(255, 255, 0, 0));
	builder_add_net4_dst(&builders[0], ip(192, 168, 0, 0), ip(255, 255, 0, 0));
	rules[0] = build_rule(&builders[0], 1);

	// Rule 2: 192.168.1.0/24 (subset of rule 1)
	builder_init(&builders[1]);
	builder_add_net4_src(&builders[1], ip(192, 168, 1, 0), ip(255, 255, 255, 0));
	builder_add_net4_dst(&builders[1], ip(192, 168, 1, 0), ip(255, 255, 255, 0));
	rules[1] = build_rule(&builders[1], 2);

	// Rule 3: 192.168.2.0/24 (another subset of rule 1)
	builder_init(&builders[2]);
	builder_add_net4_src(&builders[2], ip(192, 168, 2, 0), ip(255, 255, 255, 0));
	builder_add_net4_dst(&builders[2], ip(192, 168, 2, 0), ip(255, 255, 255, 0));
	rules[2] = build_rule(&builders[2], 3);

	// Initialize both filters
	struct filter filter_fast, filter_old;
	res = FILTER_INIT(&filter_fast, sign_net4_fast, rules, 3, &memory_context);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize fast filter");

	res = FILTER_INIT(&filter_old, sign_net4_old, rules, 3, &memory_context);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize old filter");

	// Test cases
	struct {
		uint8_t sip[4];
		uint8_t dip[4];
		const char *desc;
	} test_cases[] = {
		{{192, 168, 1, 50}, {192, 168, 1, 100}, "In both rule 1 and 2"},
		{{192, 168, 2, 50}, {192, 168, 2, 100}, "In both rule 1 and 3"},
		{{192, 168, 3, 50}, {192, 168, 3, 100}, "Only in rule 1"},
		{{192, 168, 1, 50}, {192, 168, 2, 100}, "Src in 1&2, dst in 1&3"},
		{{10, 0, 0, 1}, {10, 0, 0, 2}, "No match"},
	};

	for (size_t i = 0; i < sizeof(test_cases) / sizeof(test_cases[0]); i++) {
		struct packet p = {0};
		res = fill_packet_net4(&p, test_cases[i].sip, test_cases[i].dip, 0, 0, IPPROTO_UDP, 0);
		TEST_ASSERT_EQUAL(res, 0, "failed to fill packet");

		struct packet *packet_ptr = &p;
		struct value_range *actions_fast, *actions_old;
		
		FILTER_QUERY(&filter_fast, sign_net4_fast, &packet_ptr, &actions_fast, 1);
		FILTER_QUERY(&filter_old, sign_net4_old, &packet_ptr, &actions_old, 1);

		LOG(INFO, "Test case %zu (%s): sip=%u.%u.%u.%u, dip=%u.%u.%u.%u",
		    i, test_cases[i].desc,
		    test_cases[i].sip[0], test_cases[i].sip[1], test_cases[i].sip[2], test_cases[i].sip[3],
		    test_cases[i].dip[0], test_cases[i].dip[1], test_cases[i].dip[2], test_cases[i].dip[3]);
		LOG(INFO, "  Fast: %zu actions, Old: %zu actions",
		    actions_fast->count, actions_old->count);

		if (actions_fast->count != actions_old->count) {
			LOG(ERROR, "  MISMATCH!");
			free_packet(&p);
			FILTER_FREE(&filter_fast, sign_net4_fast);
			FILTER_FREE(&filter_old, sign_net4_old);
			return TEST_FAILED;
		}

		free_packet(&p);
	}

	FILTER_FREE(&filter_fast, sign_net4_fast);
	FILTER_FREE(&filter_old, sign_net4_old);

	LOG(INFO, "Overlapping rules test passed");
	return TEST_SUCCESS;
}

// Stress test: Compare net4_fast vs net4 (old implementation)
static int
test_stress_correctness(void *memory, size_t num_rules, size_t num_queries) {
	LOG(INFO, "=== Stress Test: Correctness comparison (rules=%zu, queries=%zu) ===", 
	    num_rules, num_queries);
	
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, MEMORY_SIZE);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	// Generate random rules
	struct filter_rule *rules = malloc(sizeof(struct filter_rule) * num_rules);
	struct filter_rule_builder *builders = malloc(sizeof(struct filter_rule_builder) * num_rules);
	TEST_ASSERT_NOT_NULL(rules, "failed to allocate rules");
	TEST_ASSERT_NOT_NULL(builders, "failed to allocate builders");

	srand(12345); // Fixed seed for reproducibility
	
	for (size_t i = 0; i < num_rules; i++) {
		builder_init(&builders[i]);
		
		// Generate random network with /16 or /24 prefix
		uint8_t prefix_len = (rand() % 2) ? 16 : 24;
		uint8_t a = rand() % 256;
		uint8_t b = rand() % 256;
		uint8_t c = (prefix_len == 24) ? (rand() % 256) : 0;
		
		if (prefix_len == 16) {
			builder_add_net4_src(&builders[i], ip(a, b, 0, 0), ip(255, 255, 0, 0));
			builder_add_net4_dst(&builders[i], ip(a, b, 0, 0), ip(255, 255, 0, 0));
		} else {
			builder_add_net4_src(&builders[i], ip(a, b, c, 0), ip(255, 255, 255, 0));
			builder_add_net4_dst(&builders[i], ip(a, b, c, 0), ip(255, 255, 255, 0));
		}
		
		rules[i] = build_rule(&builders[i], i + 1);

		LOG(INFO, "Rule %zu: %u.%u.%u.%u/%u.%u.%u.%u [%u, %u]", i, rules[i].net4.srcs[0].addr[0], rules[i].net4.srcs[0].addr[1], rules[i].net4.srcs[0].addr[2], rules[i].net4.srcs[0].addr[3], rules[i].net4.dsts[0].mask[0], rules[i].net4.dsts[0].mask[1], rules[i].net4.dsts[0].mask[2], rules[i].net4.dsts[0].mask[3], rte_cpu_to_be_32(*(uint32_t *)rules[i].net4.srcs[0].addr), rte_cpu_to_be_32(*(uint32_t *)rules[i].net4.srcs[0].addr + (prefix_len == 16 ? 0xFFFF : 0xFF)));
	}

	// Initialize both filters
	struct filter filter_fast, filter_old;
	res = FILTER_INIT(&filter_fast, sign_net4_fast, rules, num_rules, &memory_context);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize fast filter");

	res = FILTER_INIT(&filter_old, sign_net4_old, rules, num_rules, &memory_context);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize old filter");

	// sip=187.182.248.169, dip=212.111.20.41
	uint8_t sip[4] = {187, 182, 248, 169};
	uint8_t dip[4] = {212, 111, 20, 41};

	struct packet p = {0};
	res = fill_packet_net4(&p, sip, dip, 0, 0, IPPROTO_UDP, 0);
	if (res != 0) {
		free_packet(&p);
	}

	struct packet *packet_ptr = &p;
	struct value_range *actions_fast, *actions_old;
	
	FILTER_QUERY(&filter_fast, sign_net4_fast, &packet_ptr, &actions_fast, 1);
	FILTER_QUERY(&filter_old, sign_net4_old, &packet_ptr, &actions_old, 1);

	// Compare results
	if (actions_fast->count != actions_old->count) {
		LOG(ERROR, "Mismatch at query %zu: fast=%zu actions, old=%zu actions (sip=%u.%u.%u.%u, dip=%u.%u.%u.%u)",
			(size_t)0, actions_fast->count, actions_old->count,
			sip[0], sip[1], sip[2], sip[3],
			dip[0], dip[1], dip[2], dip[3]);
		uint32_t action = ADDR_OF(&actions_fast->values)[0];
		LOG(ERROR, "matched with (sip=%u.%u.%u.%u/%u.%u.%u.%u, dip=%u.%u.%u.%u/%u.%u.%u.%u)", rules[action].net4.srcs[0].addr[0], rules[action].net4.srcs[0].addr[1], rules[action].net4.srcs[0].addr[2], rules[action].net4.srcs[0].addr[3], rules[action].net4.dsts->mask[0], rules[action].net4.dsts->mask[1], rules[action].net4.dsts->mask[2], rules[action].net4.dsts->mask[3], rules[action].net4.dsts[0].addr[0], rules[action].net4.dsts[0].addr[1], rules[action].net4.dsts[0].addr[2], rules[action].net4.dsts[0].addr[3], rules[action].net4.dsts->mask[0], rules[action].net4.dsts->mask[1], rules[action].net4.dsts->mask[2], rules[action].net4.dsts->mask[3]);
	} else if (actions_fast->count > 0) {
		// Compare first action (simplified check)
		uint32_t action_fast = ADDR_OF(&actions_fast->values)[0];
		uint32_t action_old = ADDR_OF(&actions_old->values)[0];
		if (action_fast != action_old) {
			LOG(ERROR, "Mismatch at query %zu: fast=%u, old=%u",
				(size_t)0, action_fast, action_old);
		}
	}

	free_packet(&p);

	FILTER_FREE(&filter_fast, sign_net4_fast);
	FILTER_FREE(&filter_old, sign_net4_old);
	
	free(rules);
	free(builders);

	return TEST_SUCCESS;
}

// Stress test: Performance comparison
static int
test_stress_performance(void *memory, size_t num_rules, size_t num_queries) {
	LOG(INFO, "=== Stress Test: Performance comparison (rules=%zu, queries=%zu) ===", 
	    num_rules, num_queries);
	
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, MEMORY_SIZE);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	// Generate rules
	struct filter_rule *rules = malloc(sizeof(struct filter_rule) * num_rules);
	struct filter_rule_builder *builders = malloc(sizeof(struct filter_rule_builder) * num_rules);
	TEST_ASSERT_NOT_NULL(rules, "failed to allocate rules");
	TEST_ASSERT_NOT_NULL(builders, "failed to allocate builders");

	srand(12345);
	
	for (size_t i = 0; i < num_rules; i++) {
		builder_init(&builders[i]);
		uint8_t a = rand() % 256;
		uint8_t b = rand() % 256;
		builder_add_net4_src(&builders[i], ip(a, b, 0, 0), ip(255, 255, 0, 0));
		builder_add_net4_dst(&builders[i], ip(a, b, 0, 0), ip(255, 255, 0, 0));
		rules[i] = build_rule(&builders[i], i + 1);
	}

	// Initialize both filters
	struct filter filter_fast, filter_old;
	res = FILTER_INIT(&filter_fast, sign_net4_fast, rules, num_rules, &memory_context);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize fast filter");

	res = FILTER_INIT(&filter_old, sign_net4_old, rules, num_rules, &memory_context);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize old filter");

	// Pre-generate test packets
	struct packet *packets = malloc(sizeof(struct packet) * num_queries);
	TEST_ASSERT_NOT_NULL(packets, "failed to allocate packets");
	
	for (size_t i = 0; i < num_queries; i++) {
		uint8_t sip[4] = {rand() % 256, rand() % 256, rand() % 256, rand() % 256};
		uint8_t dip[4] = {rand() % 256, rand() % 256, rand() % 256, rand() % 256};
		packets[i] = (struct packet){0};
		fill_packet_net4(&packets[i], sip, dip, 0, 0, IPPROTO_UDP, 0);
	}

	// Benchmark fast implementation
	struct timespec start_fast, end_fast;
	clock_gettime(CLOCK_MONOTONIC, &start_fast);
	
	for (size_t i = 0; i < num_queries; i++) {
		struct packet *packet_ptr = &packets[i];
		struct value_range *actions;
		FILTER_QUERY(&filter_fast, sign_net4_fast, &packet_ptr, &actions, 1);
	}
	
	clock_gettime(CLOCK_MONOTONIC, &end_fast);

	// Benchmark old implementation
	struct timespec start_old, end_old;
	clock_gettime(CLOCK_MONOTONIC, &start_old);
	
	for (size_t i = 0; i < num_queries; i++) {
		struct packet *packet_ptr = &packets[i];
		struct value_range *actions;
		FILTER_QUERY(&filter_old, sign_net4_old, &packet_ptr, &actions, 1);
	}
	
	clock_gettime(CLOCK_MONOTONIC, &end_old);

	// Calculate times
	double time_fast = (end_fast.tv_sec - start_fast.tv_sec) +
	                   (end_fast.tv_nsec - start_fast.tv_nsec) / 1e9;
	double time_old = (end_old.tv_sec - start_old.tv_sec) +
	                  (end_old.tv_nsec - start_old.tv_nsec) / 1e9;

	LOG(INFO, "Fast implementation: %.6f seconds (%.2f queries/sec)",
	    time_fast, num_queries / time_fast);
	LOG(INFO, "Old implementation:  %.6f seconds (%.2f queries/sec)",
	    time_old, num_queries / time_old);
	LOG(INFO, "Speedup: %.2fx", time_old / time_fast);

	// Cleanup
	for (size_t i = 0; i < num_queries; i++) {
		free_packet(&packets[i]);
	}
	free(packets);

	FILTER_FREE(&filter_fast, sign_net4_fast);
	FILTER_FREE(&filter_old, sign_net4_old);
	
	free(rules);
	free(builders);

	return TEST_SUCCESS;
}

static int
test_stress_correctness_single_byte(void *memory, size_t num_rules, size_t num_queries, uint64_t rng) {
	LOG(INFO, "=== Stress Test: Correctness comparison, single byte (rules=%zu, queries=%zu, seed=%lu) ===", 
	    num_rules, num_queries, rng);
	
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, MEMORY_SIZE);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	// Generate random rules
	struct filter_rule *rules = malloc(sizeof(struct filter_rule) * num_rules);
	struct filter_rule_builder *builders = malloc(sizeof(struct filter_rule_builder) * num_rules);
	TEST_ASSERT_NOT_NULL(rules, "failed to allocate rules");
	TEST_ASSERT_NOT_NULL(builders, "failed to allocate builders");

	for (size_t i = 0; i < num_rules; i++) {
		builder_init(&builders[i]);
		
		// Generate random network with /16 or /24 prefix
		uint8_t prefix_len = 32 - rng_next(&rng) % 9;
		uint8_t a = rng_next(&rng) % 256;

		uint8_t addr[4];
		uint8_t mask[4];
		memset(addr, 0, 4);
		memset(mask, 255, 4);
		addr[3] = a;
		mask[3] = (1ull<<prefix_len)-1;
		builder_add_net4_src(&builders[i], addr, mask);
		builder_add_net4_dst(&builders[i], addr, mask);
	
		rules[i] = build_rule(&builders[i], i + 1);

		// LOG(INFO, "Rule %zu: %u.%u.%u.%u/%u.%u.%u.%u [%u, %u]", i, rules[i].net4.srcs[0].addr[0], rules[i].net4.srcs[0].addr[1], rules[i].net4.srcs[0].addr[2], rules[i].net4.srcs[0].addr[3], rules[i].net4.dsts[0].mask[0], rules[i].net4.dsts[0].mask[1], rules[i].net4.dsts[0].mask[2], rules[i].net4.dsts[0].mask[3], rte_cpu_to_be_32(*(uint32_t *)rules[i].net4.srcs[0].addr), rte_cpu_to_be_32(*(uint32_t *)rules[i].net4.srcs[0].addr + (prefix_len == 16 ? 0xFFFF : 0xFF)));
	}

	// Initialize both filters
	struct filter filter_fast, filter_old;
	res = FILTER_INIT(&filter_fast, sign_net4_fast, rules, num_rules, &memory_context);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize fast filter");

	res = FILTER_INIT(&filter_old, sign_net4_old, rules, num_rules, &memory_context);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize old filter");

	for (size_t i = 0; i < num_queries; ++i) {
		uint8_t sip[4];
		uint8_t dip[4];
		memset(sip, 0, 4);
		memset(dip, 0, 4);
		dip[3] = sip[3] = rng_next(&rng) % 255;

		struct packet p = {0};
		res = fill_packet_net4(&p, sip, dip, 0, 0, IPPROTO_UDP, 0);
		if (res != 0) {
			free_packet(&p);
		}

		struct packet *packet_ptr = &p;
		struct value_range *actions_fast, *actions_old;
		
		FILTER_QUERY(&filter_fast, sign_net4_fast, &packet_ptr, &actions_fast, 1);
		FILTER_QUERY(&filter_old, sign_net4_old, &packet_ptr, &actions_old, 1);

		// Compare results
		if (actions_fast->count != actions_old->count) {
			LOG(ERROR, "Mismatch at query %zu: fast=%zu actions, old=%zu actions (sip=%u.%u.%u.%u, dip=%u.%u.%u.%u)",
				(size_t)0, actions_fast->count, actions_old->count,
				sip[0], sip[1], sip[2], sip[3],
				dip[0], dip[1], dip[2], dip[3]);
			// uint32_t action = ADDR_OF(&actions_fast->values)[0];
			// LOG(ERROR, "matched with (sip=%u.%u.%u.%u/%u.%u.%u.%u, dip=%u.%u.%u.%u/%u.%u.%u.%u)", rules[action].net4.srcs[0].addr[0], rules[action].net4.srcs[0].addr[1], rules[action].net4.srcs[0].addr[2], rules[action].net4.srcs[0].addr[3], rules[action].net4.dsts->mask[0], rules[action].net4.dsts->mask[1], rules[action].net4.dsts->mask[2], rules[action].net4.dsts->mask[3], rules[action].net4.dsts[0].addr[0], rules[action].net4.dsts[0].addr[1], rules[action].net4.dsts[0].addr[2], rules[action].net4.dsts[0].addr[3], rules[action].net4.dsts->mask[0], rules[action].net4.dsts->mask[1], rules[action].net4.dsts->mask[2], rules[action].net4.dsts->mask[3]);
		} else if (actions_fast->count > 0) {
			// Compare first action (simplified check)
			uint32_t action_fast = ADDR_OF(&actions_fast->values)[0];
			uint32_t action_old = ADDR_OF(&actions_old->values)[0];
			if (action_fast != action_old) {
				LOG(ERROR, "Mismatch at query %zu: fast=%u, old=%u",
					(size_t)0, action_fast, action_old);
			}
		}

		free_packet(&p);
	}

	FILTER_FREE(&filter_fast, sign_net4_fast);
	FILTER_FREE(&filter_old, sign_net4_old);
	
	free(rules);
	free(builders);

	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////

static int
test_stress_correctness_two_bytes(void *memory, size_t num_rules, size_t num_queries, uint64_t rng) {
	LOG(INFO, "=== Stress Test: Correctness comparison, two bytes (rules=%zu, queries=%zu, seed=%lu) ===", 
	    num_rules, num_queries, rng);
	
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, MEMORY_SIZE);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	// Generate random rules
	struct filter_rule *rules = malloc(sizeof(struct filter_rule) * num_rules);
	struct filter_rule_builder *builders = malloc(sizeof(struct filter_rule_builder) * num_rules);
	TEST_ASSERT_NOT_NULL(rules, "failed to allocate rules");
	TEST_ASSERT_NOT_NULL(builders, "failed to allocate builders");

	for (size_t i = 0; i < num_rules; i++) {
		builder_init(&builders[i]);
		
		uint8_t prefix_len = 32 - rng_next(&rng) % 17;
		uint8_t a = rng_next(&rng) % 256;
		uint8_t b = rng_next(&rng) % 256;

		uint8_t addr[4];
		uint8_t mask[4];
		memset(addr, 0, 4);
		memset(mask, 255, 4);
		addr[2] = a;
		addr[3] = b;
		mask[2] = ((1ull<<prefix_len)-1)/256;
		mask[3] = ((1ull<<prefix_len)-1)%256;
		builder_add_net4_src(&builders[i], addr, mask);
		builder_add_net4_dst(&builders[i], addr, mask);
	
		rules[i] = build_rule(&builders[i], i + 1);

		// LOG(INFO, "Rule %zu: %u.%u.%u.%u/%u.%u.%u.%u [%u, %u]", i, rules[i].net4.srcs[0].addr[0], rules[i].net4.srcs[0].addr[1], rules[i].net4.srcs[0].addr[2], rules[i].net4.srcs[0].addr[3], rules[i].net4.dsts[0].mask[0], rules[i].net4.dsts[0].mask[1], rules[i].net4.dsts[0].mask[2], rules[i].net4.dsts[0].mask[3], rte_cpu_to_be_32(*(uint32_t *)rules[i].net4.srcs[0].addr), rte_cpu_to_be_32(*(uint32_t *)rules[i].net4.srcs[0].addr + (prefix_len == 16 ? 0xFFFF : 0xFF)));
	}

	// Initialize both filters
	struct filter filter_fast, filter_old;
	res = FILTER_INIT(&filter_fast, sign_net4_fast, rules, num_rules, &memory_context);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize fast filter");

	res = FILTER_INIT(&filter_old, sign_net4_old, rules, num_rules, &memory_context);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize old filter");

	for (size_t i = 0; i < num_queries; ++i) {
		uint8_t sip[4];
		uint8_t dip[4];
		memset(sip, 0, 4);
		memset(dip, 0, 4);
		dip[3] = sip[3] = rng_next(&rng) % 255;
		dip[2] = sip[2] = rng_next(&rng) % 255;

		struct packet p = {0};
		res = fill_packet_net4(&p, sip, dip, 0, 0, IPPROTO_UDP, 0);
		if (res != 0) {
			free_packet(&p);
		}

		struct packet *packet_ptr = &p;
		struct value_range *actions_fast, *actions_old;
		
		FILTER_QUERY(&filter_fast, sign_net4_fast, &packet_ptr, &actions_fast, 1);
		FILTER_QUERY(&filter_old, sign_net4_old, &packet_ptr, &actions_old, 1);

		// Compare results
		if (actions_fast->count != actions_old->count) {
			LOG(ERROR, "Mismatch at query %zu: fast=%zu actions, old=%zu actions (sip=%u.%u.%u.%u, dip=%u.%u.%u.%u)",
				(size_t)0, actions_fast->count, actions_old->count,
				sip[0], sip[1], sip[2], sip[3],
				dip[0], dip[1], dip[2], dip[3]);
			// uint32_t action = ADDR_OF(&actions_fast->values)[0];
			// LOG(ERROR, "matched with (sip=%u.%u.%u.%u/%u.%u.%u.%u, dip=%u.%u.%u.%u/%u.%u.%u.%u)", rules[action].net4.srcs[0].addr[0], rules[action].net4.srcs[0].addr[1], rules[action].net4.srcs[0].addr[2], rules[action].net4.srcs[0].addr[3], rules[action].net4.dsts->mask[0], rules[action].net4.dsts->mask[1], rules[action].net4.dsts->mask[2], rules[action].net4.dsts->mask[3], rules[action].net4.dsts[0].addr[0], rules[action].net4.dsts[0].addr[1], rules[action].net4.dsts[0].addr[2], rules[action].net4.dsts[0].addr[3], rules[action].net4.dsts->mask[0], rules[action].net4.dsts->mask[1], rules[action].net4.dsts->mask[2], rules[action].net4.dsts->mask[3]);
		} else if (actions_fast->count > 0) {
			// Compare first action (simplified check)
			uint32_t action_fast = ADDR_OF(&actions_fast->values)[0];
			uint32_t action_old = ADDR_OF(&actions_old->values)[0];
			if (action_fast != action_old) {
				LOG(ERROR, "Mismatch at query %zu: fast=%u, old=%u",
					(size_t)0, action_fast, action_old);
			}
		}

		free_packet(&p);
	}

	FILTER_FREE(&filter_fast, sign_net4_fast);
	FILTER_FREE(&filter_old, sign_net4_old);
	
	free(rules);
	free(builders);

	return TEST_SUCCESS;
}

int
main() {
	log_enable_name("debug");

	void *memory = malloc(MEMORY_SIZE);
	if (!memory) {
		LOG(ERROR, "Failed to allocate memory");
		return 1;
	}

	size_t failed_count = 0;
	size_t total_tests = 0;

	(void)test_combined_basic;

	// Basic tests
	// total_tests++;
	// if (test_combined_basic(memory) != TEST_SUCCESS) {
	// 	LOG(ERROR, "test_combined_basic failed");
	// 	failed_count++;
	// }

	// total_tests++;
	// if (test_src_only_multiple_networks(memory) != TEST_SUCCESS) {
	// 	LOG(ERROR, "test_src_only_multiple_networks failed");
	// 	failed_count++;
	// }

	// total_tests++;
	// if (test_dst_only_multiple_networks(memory) != TEST_SUCCESS) {
	// 	LOG(ERROR, "test_dst_only_multiple_networks failed");
	// 	failed_count++;
	// }

	// total_tests++;
	// if (test_edge_cases(memory) != TEST_SUCCESS) {
	// 	LOG(ERROR, "test_edge_cases failed");
	// 	failed_count++;
	// }

	// total_tests++;
	// if (test_overlapping_ranges(memory) != TEST_SUCCESS) {
	// 	LOG(ERROR, "test_overlapping_ranges failed");
	// 	failed_count++;
	// }

	// // Debug tests
	// total_tests++;
	// if (test_debug_simple(memory) != TEST_SUCCESS) {
	// 	LOG(ERROR, "test_debug_simple failed");
	// 	failed_count++;
	// }

	// total_tests++;
	// if (test_debug_multiple_rules(memory) != TEST_SUCCESS) {
	// 	LOG(ERROR, "test_debug_multiple_rules failed");
	// 	failed_count++;
	// }

	(void)test_debug_stress_small;
	(void)test_src_only_multiple_networks;
	(void)test_overlapping_ranges;
	// total_tests++;
	// if (test_debug_stress_small(memory) != TEST_SUCCESS) {
	// 	LOG(ERROR, "test_debug_stress_small failed");
	// 	failed_count++;
	// }

	// total_tests++;
	// if (test_debug_overlapping(memory) != TEST_SUCCESS) {
	// 	LOG(ERROR, "test_debug_overlapping failed");
	// 	failed_count++;
	// }

	// // Stress tests - correctness
	// total_tests++;
	// if (test_stress_correctness(memory, 10, 100) != TEST_SUCCESS) {
	// 	LOG(ERROR, "test_stress_correctness (10 rules, 100 queries) failed");
	// 	failed_count++;
	// }

	(void)test_debug_overlapping;

	// total_tests++;
	// if (test_stress_correctness(memory, 49, 100) != TEST_SUCCESS) {
	// 	LOG(ERROR, "test_stress_correctness (100 rules, 1000 queries) failed");
	// 	failed_count++;
	// }
	(void)test_stress_correctness;

	for (size_t rng = 0; rng < 100; ++rng) {
		++total_tests;
		if (test_stress_correctness_single_byte(memory, 100, 10000, 123 * rng) != TEST_SUCCESS) {
			LOG(ERROR, "test_stress_correctness_single_byte (100 rules, 10000 queries, 123 seed) failed");
			failed_count++;
		}
	}

	for (size_t rng = 0; rng < 100; ++rng) {
		++total_tests;
		if (test_stress_correctness_two_bytes(memory, 100, 10000, 321 * rng) != TEST_SUCCESS) {
			LOG(ERROR, "test_stress_correctness_two_bytes (100 rules, 10000 queries, 321 seed) failed");
			failed_count++;
		}
	}

	

	// total_tests++;
	// if (test_stress_correctness(memory, 500, 5000) != TEST_SUCCESS) {
	// 	LOG(ERROR, "test_stress_correctness (500 rules, 5000 queries) failed");
	// 	failed_count++;
	// }

	// // Stress tests - performance
	// total_tests++;
	// if (test_stress_performance(memory, 100, 10000) != TEST_SUCCESS) {
	// 	LOG(ERROR, "test_stress_performance (100 rules, 10000 queries) failed");
	// 	failed_count++;
	// }

	// total_tests++;
	// if (test_stress_performance(memory, 500, 10000) != TEST_SUCCESS) {
	// 	LOG(ERROR, "test_stress_performance (500 rules, 10000 queries) failed");
	// 	failed_count++;
	// }

	// total_tests++;
	// if (test_stress_performance(memory, 1000, 10000) != TEST_SUCCESS) {
	// 	LOG(ERROR, "test_stress_performance (1000 rules, 10000 queries) failed");
	// 	failed_count++;
	// }

	(void)test_stress_performance;

	free(memory);

	if (failed_count == 0) {
		LOG(INFO, "=== ALL %zu TESTS PASSED ===", total_tests);
	} else {
		LOG(ERROR, "=== %zu/%zu TESTS FAILED ===", failed_count, total_tests);
	}

	return (failed_count > 0 ? 1 : 0);
}
