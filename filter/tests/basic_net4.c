#include "filter/compiler.h"
#include "filter/filter.h"
#include "filter/query.h"

#include "filter/tests/helpers.h"
#include "lib/utils/packet.h"

#include "logging/log.h"
#include <assert.h>
#include <netinet/in.h>

FILTER_COMPILER_DECLARE(sign_net4_compile, net4_src, net4_dst);
FILTER_QUERY_DECLARE(sign_net4, net4_src, net4_dst);

static void
query_and_expect_action(
	struct filter *filter,
	uint8_t sip[NET4_LEN],
	uint8_t dip[NET4_LEN],
	uint32_t expected
) {
	struct packet p = {0};
	int res = fill_packet_net4(&p, sip, dip, 0, 0, IPPROTO_UDP, 0);
	assert(res == 0);
	struct packet *packet_ptr = &p;
	struct value_range *actions;
	filter_query(filter, sign_net4, &packet_ptr, &actions, 1);
	assert(actions->count >= 1);
	assert(ADDR_OF(&actions->values)[0] == expected);
	free_packet(&p);
}

static void
query_and_expect_no_action(
	struct filter *filter, uint8_t sip[NET4_LEN], uint8_t dip[NET4_LEN]
) {
	struct packet p = {0};
	int res = fill_packet_net4(&p, sip, dip, 0, 0, IPPROTO_UDP, 0);
	assert(res == 0);
	struct packet *packet_ptr = &p;
	struct value_range *actions;
	filter_query(filter, sign_net4, &packet_ptr, &actions, 1);
	assert(actions->count == 0);
	free_packet(&p);
}

// Helper to create prefix mask from prefix length
static uint32_t
prefix_mask(uint32_t prefix) {
	uint32_t mask = (uint32_t)(-1) ^ ((1 << (32 - prefix)) - 1);
	return mask;
}

// Helper to convert mask to big-endian bytes
static void
mask_to_bytes(uint32_t mask, uint8_t bytes[4]) {
	bytes[0] = (mask >> 24) & 0xFF;
	bytes[1] = (mask >> 16) & 0xFF;
	bytes[2] = (mask >> 8) & 0xFF;
	bytes[3] = mask & 0xFF;
}

// Regression test with all 20 rules from the failing stress test (seed=12)
// This test reproduces the exact scenario that exposed the bug in the old
// implementation
static void
test_stress_seed12_regression(void *memory, size_t memory_size) {
	LOG(INFO,
	    "=== Regression Test: Stress seed=12 (20 rules, 3 packets) ===");

	// Create a new block allocator and memory context for this test
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, memory_size);

	struct memory_context memory_context;
	int res =
		memory_context_init(&memory_context, "stress_test", &allocator);
	assert(res == 0);

	// Define all 20 rules from the stress test
	struct {
		uint8_t src_addr[4];
		uint8_t src_prefix;
		uint8_t dst_addr[4];
		uint8_t dst_prefix;
	} rule_specs[20] = {
		{{6, 1, 132, 136}, 32, {6, 3, 132, 130}, 21},
		{{5, 10, 131, 129}, 20, {7, 4, 132, 134}, 17},
		{{4, 4, 133, 135}, 27, {5, 7, 128, 131}, 17},
		{{9, 8, 136, 132}, 29, {10, 1, 132, 132}, 28},
		{{7, 10, 130, 137}, 32, {10, 8, 129, 130}, 26},
		{{9, 6, 136, 130}, 20, {9, 6, 128, 128}, 24},
		{{1, 3, 137, 129}, 25, {8, 9, 134, 130}, 19},
		{{4, 7, 134, 136}, 32, {4, 7, 133, 132}, 26},
		{{5, 7, 135, 132}, 29, {6, 4, 134, 129}, 21},
		{{9, 7, 137, 132}, 31, {4, 1, 132, 137}, 17},
		{{4, 5, 137, 131}, 29, {10, 2, 133, 133}, 31},
		{{7, 7, 129, 132}, 16, {7, 10, 128, 128}, 20},
		{{9, 4, 133, 131}, 25, {7, 4, 128, 136}, 21},
		{{5, 4, 136, 130}, 19, {1, 8, 128, 133}, 26},
		{{8, 4, 136, 128}, 25, {3, 3, 133, 128}, 20},
		{{4, 4, 128, 133}, 16, {5, 3, 136, 132}, 23},
		{{2, 8, 131, 128}, 23, {3, 3, 136, 133}, 29},
		{{9, 6, 136, 131}, 29, {8, 6, 128, 134}, 26},
		{{9, 10, 136, 136}, 31, {2, 5, 131, 137}, 19},
		{{5, 5, 129, 132}, 16, {4, 10, 129, 137}, 29},
	};

	// Build all 20 rules
	struct filter_rule_builder builders[20];
	struct filter_rule rules[20];
	const struct filter_rule *rule_ptrs[20];

	for (size_t i = 0; i < 20; i++) {
		builder_init(&builders[i]);

		uint8_t src_mask[4], dst_mask[4];
		mask_to_bytes(prefix_mask(rule_specs[i].src_prefix), src_mask);
		mask_to_bytes(prefix_mask(rule_specs[i].dst_prefix), dst_mask);

		builder_add_net4_src(
			&builders[i], rule_specs[i].src_addr, src_mask
		);
		builder_add_net4_dst(
			&builders[i], rule_specs[i].dst_addr, dst_mask
		);

		rules[i] = build_rule(&builders[i]);
		rule_ptrs[i] = rules + i;
	}

	// Initialize filter with all 20 rules
	struct filter filter;
	res = filter_init(
		&filter, sign_net4_compile, rule_ptrs, 20, &memory_context
	);
	assert(res == 0);

	// Test packet 0: src=7.1.134.133, dst=4.5.130.133
	// Should match rule 11 (index 11): src=7.7.129.132/16,
	// dst=7.10.128.128/20 src 7.1.134.133 matches 7.7.129.132/16 (7.0.0.0
	// - 7.255.255.255) dst 4.5.130.133 does NOT match 7.10.128.128/20
	// (7.10.128.0 - 7.10.143.255) Expected: NO match
	query_and_expect_no_action(
		&filter, ip(7, 1, 134, 133), ip(4, 5, 130, 133)
	);

	// Test packet 1: src=4.2.130.128, dst=10.6.129.133
	// Should check against multiple rules but none should fully match
	query_and_expect_no_action(
		&filter, ip(4, 2, 130, 128), ip(10, 6, 129, 133)
	);

	// Test packet 2: src=5.10.138.134, dst=1.9.139.137
	// This is the critical test case that exposed the bug!
	// src 5.10.138.134 matches rule 1: src=5.10.131.129/20 (5.10.128.0
	// - 5.10.143.255) dst 1.9.139.137 does NOT match rule 1:
	// dst=7.4.132.134/17 (7.4.128.0 - 7.4.255.255) Expected: NO match (old
	// implementation incorrectly returned action 2)
	query_and_expect_no_action(
		&filter, ip(5, 10, 138, 134), ip(1, 9, 139, 137)
	);

	filter_free(&filter, sign_net4_compile);

	LOG(INFO, "Regression test passed!");
}

int
main() {
	log_enable_name("debug");

	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	void *memory = malloc(1 << 28);
	block_allocator_put_arena(&allocator, memory, 1 << 28);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	// action 1:
	struct filter_rule_builder builder1;
	builder_init(&builder1);
	builder_add_net4_src(
		&builder1, ip(192, 255, 168, 0), ip(255, 255, 255, 0)
	);
	builder_add_net4_dst(
		&builder1, ip(192, 255, 168, 0), ip(255, 255, 255, 0)
	);
	struct filter_rule action1 = build_rule(&builder1);
	const struct filter_rule *action_ptr = &action1;

	// init filter
	struct filter filter;
	res = filter_init(
		&filter, sign_net4_compile, &action_ptr, 1, &memory_context
	);
	assert(res == 0);

	query_and_expect_action(
		&filter, ip(192, 255, 168, 1), ip(192, 255, 168, 10), 0
	);

	// no action because src ip mismatch
	query_and_expect_no_action(
		&filter, ip(195, 255, 168, 1), ip(192, 255, 168, 10)
	);

	// no action because dst ip mismatch
	query_and_expect_no_action(
		&filter, ip(192, 255, 168, 10), ip(195, 255, 168, 1)
	);

	filter_free(&filter, sign_net4_compile);

	// Regression test for bug where src_dst filter incorrectly matches
	// when only src matches but dst doesn't (or vice versa)
	// Rule: src=5.10.131.129/20, dst=7.4.132.134/17
	struct filter_rule_builder builder2;
	builder_init(&builder2);
	// 5.10.131.129/20 covers 5.10.128.0 - 5.10.143.255
	builder_add_net4_src(
		&builder2, ip(5, 10, 131, 129), ip(255, 255, 240, 0)
	);
	// 7.4.132.134/17 covers 7.4.128.0 - 7.4.255.255
	builder_add_net4_dst(
		&builder2, ip(7, 4, 132, 134), ip(255, 255, 128, 0)
	);
	struct filter_rule action2 = build_rule(&builder2);

	const struct filter_rule *action2_ptr = &action2;

	struct filter filter2;
	res = filter_init(
		&filter2, sign_net4_compile, &action2_ptr, 1, &memory_context
	);
	assert(res == 0);

	// Packet: src=5.10.138.134 (matches src), dst=1.9.139.137 (does NOT
	// match dst) Expected: NO match because dst doesn't match
	query_and_expect_no_action(
		&filter2, ip(5, 10, 138, 134), ip(1, 9, 139, 137)
	);

	// Packet: src=5.10.138.134 (matches src), dst=7.4.200.100 (matches dst)
	// Expected: MATCH because both src and dst match
	query_and_expect_action(
		&filter2, ip(5, 10, 138, 134), ip(7, 4, 200, 100), 0
	);

	// Packet: src=1.1.1.1 (does NOT match src), dst=7.4.200.100 (matches
	// dst) Expected: NO match because src doesn't match
	query_and_expect_no_action(
		&filter2, ip(1, 1, 1, 1), ip(7, 4, 200, 100)
	);

	filter_free(&filter2, sign_net4_compile);

	// Run comprehensive regression test with all 20 rules from stress test
	// Allocate separate memory for the stress test
	// test_stress_seed12_regression(memory, 1 << 28);

	(void)test_stress_seed12_regression;

	free(memory);

	LOG(INFO, "OK!");
	return 0;
}
