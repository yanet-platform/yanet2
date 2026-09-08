// Regression tests for the filter2 compiler: rules with wide network
// lists and rules with non-prefix masks.
//
// Every scenario compiles host side through the public compile entries
// and classifies real packets. Two regressions are pinned: the network
// dedup stage must be sized from the real per rule network count (a
// single wide rule used to overflow it), and a non-contiguous mask must
// be rejected by the compile entry instead of walking out of bounds.

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "common/memory.h"
#include "common/memory_address.h"
#include "common/memory_block.h"

#include "lib/filter2/compiler.h"
#include "lib/filter2/filter.h"
#include "lib/filter2/query.h"

#include "lib/utils/packet.h"

FILTER_COMPILER_DECLARE(c_net4, net4_src, net4_dst);
FILTER_QUERY_DECLARE(q_net4, net4_src, net4_dst);
FILTER_COMPILER_DECLARE(c_net6, net6_src, net6_dst);
FILTER_QUERY_DECLARE(q_net6, net6_src, net6_dst);

// 13 distinct networks in one rule: one past the 12 entries the dedup
// view was sized to when it assumed at most four networks per rule.
#define WIDE_NET_COUNT 13

// Wide rule fanout of the production shape that triggered the overflow.
#define FANOUT_NET4_COUNT 20000

// Wide rule fanout of the production shape that triggered the overflow
// on the v6 side as well.
#define FANOUT_NET6_COUNT 20000

#define CHECK(cond)                                                            \
	do {                                                                   \
		if (!(cond)) {                                                 \
			fprintf(stderr, "%s: failed: %s\n", __func__, #cond);  \
			return -1;                                             \
		}                                                              \
	} while (0)

// One query against a compiled filter: classifies a single v4 or v6
// packet built by the fill helpers and returns the rule index.
static uint32_t
query_net4_one(struct filter *filter, const uint8_t src[NET4_LEN]) {
	uint8_t dst[NET4_LEN] = {192, 0, 2, 1};
	struct packet *packet = calloc(1, sizeof(*packet));
	if (packet == NULL) {
		return FILTER_RULE_INVALID;
	}
	if (fill_packet_net4(packet, src, dst, 1234, 80, 6, 0x12) != 0) {
		free(packet);
		return FILTER_RULE_INVALID;
	}
	const struct packet *packets[1] = {packet};
	uint32_t results[1] = {FILTER_RULE_INVALID};
	filter_query(filter, q_net4, packets, results, 1);
	free_packet(packet);
	free(packet);
	return results[0];
}

static uint32_t
query_net6_one(struct filter *filter, const uint8_t src[NET6_LEN]) {
	uint8_t dst[NET6_LEN] = {
		0x20, 0x01, 0x0d, 0xb8, 0xff, 0xff, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1
	};
	struct packet *packet = calloc(1, sizeof(*packet));
	if (packet == NULL) {
		return FILTER_RULE_INVALID;
	}
	if (fill_packet_net6(packet, src, dst, 1234, 80, 6, 0x12) != 0) {
		free(packet);
		return FILTER_RULE_INVALID;
	}
	const struct packet *packets[1] = {packet};
	uint32_t results[1] = {FILTER_RULE_INVALID};
	filter_query(filter, q_net6, packets, results, 1);
	free_packet(packet);
	free(packet);
	return results[0];
}

/*
 * A single rule carries 13 distinct /24 source networks; the compile
 * must succeed and addresses inside and outside the list must classify
 * accordingly.
 */
static int
run_net4_dedup_wide_rule_test(void) {
	struct net4 srcs[WIDE_NET_COUNT];
	struct net4 dsts[1];
	memset(srcs, 0, sizeof(srcs));
	memset(dsts, 0, sizeof(dsts));
	for (uint32_t k = 0; k < WIDE_NET_COUNT; ++k) {
		srcs[k].addr[0] = 10;
		srcs[k].addr[2] = (uint8_t)k;
		srcs[k].mask[0] = 255;
		srcs[k].mask[1] = 255;
		srcs[k].mask[2] = 255;
	}

	struct filter_rule rule;
	memset(&rule, 0, sizeof(rule));
	rule.net4.srcs = srcs;
	rule.net4.src_count = WIDE_NET_COUNT;
	rule.net4.dsts = dsts;
	rule.net4.dst_count = 1;
	const struct filter_rule *ptrs[1] = {&rule};

	void *arena = malloc(1 << 20);
	struct block_allocator ba;
	block_allocator_init(&ba);
	block_allocator_put_arena(&ba, arena, 1 << 20);
	struct memory_context mctx;
	memory_context_init(&mctx, "filter2_regression", &ba);

	struct filter filter;
	memset(&filter, 0, sizeof(filter));
	CHECK(filter_init(&filter, c_net4, ptrs, 1, &mctx) == 0);

	uint8_t inside[NET4_LEN] = {10, 0, 5, 77};
	uint8_t outside[NET4_LEN] = {10, 0, 99, 77};
	CHECK(query_net4_one(&filter, inside) == 0);
	CHECK(query_net4_one(&filter, outside) == FILTER_RULE_INVALID);

	filter_free(&filter, c_net4);
	free(arena);
	return 0;
}

/*
 * A single rule carries 13 distinct /48 source networks; same shape as
 * the v4 scenario and the production rule that held tens of thousands
 * of v6 networks.
 */
static int
run_net6_dedup_wide_rule_test(void) {
	struct net6 srcs[WIDE_NET_COUNT];
	struct net6 dsts[1];
	memset(srcs, 0, sizeof(srcs));
	memset(dsts, 0, sizeof(dsts));
	for (uint32_t k = 0; k < WIDE_NET_COUNT; ++k) {
		srcs[k].addr[0] = 0x20;
		srcs[k].addr[1] = 0x01;
		srcs[k].addr[2] = 0x0d;
		srcs[k].addr[3] = 0xb8;
		srcs[k].addr[5] = (uint8_t)k;
		for (uint32_t b = 0; b < 6; ++b) {
			srcs[k].mask[b] = 255;
		}
	}

	struct filter_rule rule;
	memset(&rule, 0, sizeof(rule));
	rule.net6.srcs = srcs;
	rule.net6.src_count = WIDE_NET_COUNT;
	rule.net6.dsts = dsts;
	rule.net6.dst_count = 1;
	const struct filter_rule *ptrs[1] = {&rule};

	void *arena = malloc(1 << 20);
	struct block_allocator ba;
	block_allocator_init(&ba);
	block_allocator_put_arena(&ba, arena, 1 << 20);
	struct memory_context mctx;
	memory_context_init(&mctx, "filter2_regression", &ba);

	struct filter filter;
	memset(&filter, 0, sizeof(filter));
	CHECK(filter_init(&filter, c_net6, ptrs, 1, &mctx) == 0);

	uint8_t inside[NET6_LEN] = {0x20, 0x01, 0x0d, 0xb8, 0, 5};
	uint8_t outside[NET6_LEN] = {0x20, 0x01, 0x0d, 0xb8, 0, 0x63};
	CHECK(query_net6_one(&filter, inside) == 0);
	CHECK(query_net6_one(&filter, outside) == FILTER_RULE_INVALID);

	filter_free(&filter, c_net6);
	free(arena);
	return 0;
}

// A rule with the production fanout shape: twenty thousand distinct
// /24 source networks in one rule, compiled and queried.
static int
run_net4_dedup_large_fanout_test(void) {
	static struct net4 srcs[FANOUT_NET4_COUNT];
	struct net4 dsts[1];
	memset(dsts, 0, sizeof(dsts));
	memset(srcs, 0, sizeof(srcs));
	for (uint32_t k = 0; k < FANOUT_NET4_COUNT; ++k) {
		srcs[k].addr[0] = 10;
		srcs[k].addr[1] = (uint8_t)(k >> 8);
		srcs[k].addr[2] = (uint8_t)(k & 0xff);
		srcs[k].mask[0] = 255;
		srcs[k].mask[1] = 255;
		srcs[k].mask[2] = 255;
	}

	struct filter_rule rule;
	memset(&rule, 0, sizeof(rule));
	rule.net4.srcs = srcs;
	rule.net4.src_count = FANOUT_NET4_COUNT;
	rule.net4.dsts = dsts;
	rule.net4.dst_count = 1;
	const struct filter_rule *ptrs[1] = {&rule};

	void *arena = malloc(1 << 26);
	struct block_allocator ba;
	block_allocator_init(&ba);
	block_allocator_put_arena(&ba, arena, 1 << 26);
	struct memory_context mctx;
	memory_context_init(&mctx, "filter2_regression", &ba);

	struct filter filter;
	memset(&filter, 0, sizeof(filter));
	CHECK(filter_init(&filter, c_net4, ptrs, 1, &mctx) == 0);

	uint8_t inside[NET4_LEN] = {10, 5, 100, 7};
	uint8_t outside[NET4_LEN] = {10, 200, 0, 7};
	CHECK(query_net4_one(&filter, inside) == 0);
	CHECK(query_net4_one(&filter, outside) == FILTER_RULE_INVALID);

	filter_free(&filter, c_net4);
	free(arena);
	return 0;
}

/*
 * A v6 rule with the production fanout shape that overflowed the
 * dedup view: twenty thousand distinct /48 source networks in one
 * rule, compiled and queried.
 */
static int
run_net6_dedup_large_fanout_test(void) {
	static struct net6 srcs[FANOUT_NET6_COUNT];
	struct net6 dsts[1];
	memset(dsts, 0, sizeof(dsts));
	memset(srcs, 0, sizeof(srcs));
	for (uint32_t k = 0; k < FANOUT_NET6_COUNT; ++k) {
		srcs[k].addr[0] = 0x20;
		srcs[k].addr[1] = 0x01;
		srcs[k].addr[2] = 0x0d;
		srcs[k].addr[3] = 0xb8;
		srcs[k].addr[4] = (uint8_t)(k >> 8);
		srcs[k].addr[5] = (uint8_t)(k & 0xff);
		for (uint32_t b = 0; b < 6; ++b) {
			srcs[k].mask[b] = 255;
		}
	}

	struct filter_rule rule;
	memset(&rule, 0, sizeof(rule));
	rule.net6.srcs = srcs;
	rule.net6.src_count = FANOUT_NET6_COUNT;
	rule.net6.dsts = dsts;
	rule.net6.dst_count = 1;
	const struct filter_rule *ptrs[1] = {&rule};

	void *arena = malloc(1 << 28);
	struct block_allocator ba;
	block_allocator_init(&ba);
	block_allocator_put_arena(&ba, arena, 1 << 28);
	struct memory_context mctx;
	memory_context_init(&mctx, "filter2_regression", &ba);

	struct filter filter;
	memset(&filter, 0, sizeof(filter));
	CHECK(filter_init(&filter, c_net6, ptrs, 1, &mctx) == 0);

	uint8_t inside[NET6_LEN] = {0x20, 0x01, 0x0d, 0xb8, 0x10, 0x63};
	uint8_t outside[NET6_LEN] = {0x20, 0x01, 0x0d, 0xb8, 0x50, 0x00};
	CHECK(query_net6_one(&filter, inside) == 0);
	CHECK(query_net6_one(&filter, outside) == FILTER_RULE_INVALID);

	filter_free(&filter, c_net6);
	free(arena);
	return 0;
}

/*
 * A rule with the v4 mask 255.0.255.0 is rejected at the compile entry
 * while the same rule with a contiguous mask compiles.
 */
static int
run_net4_mask_non_contiguous_rejected_test(void) {
	struct net4 srcs[1];
	struct net4 dsts[1];
	memset(srcs, 0, sizeof(srcs));
	memset(dsts, 0, sizeof(dsts));
	srcs[0].addr[0] = 10;
	srcs[0].mask[0] = 255;
	struct filter_rule rule;
	memset(&rule, 0, sizeof(rule));
	rule.net4.srcs = srcs;
	rule.net4.src_count = 1;
	rule.net4.dsts = dsts;
	rule.net4.dst_count = 1;
	const struct filter_rule *ptrs[1] = {&rule};

	void *arena = malloc(1 << 20);
	struct block_allocator ba;
	block_allocator_init(&ba);
	block_allocator_put_arena(&ba, arena, 1 << 20);
	struct memory_context mctx;
	memory_context_init(&mctx, "filter2_regression", &ba);

	struct filter filter;
	memset(&filter, 0, sizeof(filter));
	CHECK(filter_init(&filter, c_net4, ptrs, 1, &mctx) == 0);
	filter_free(&filter, c_net4);

	srcs[0].mask[2] = 255;
	memset(&filter, 0, sizeof(filter));
	CHECK(filter_init(&filter, c_net4, ptrs, 1, &mctx) == -1);

	free(arena);
	return 0;
}

/*
 * A rule with the v6 mask ff00:ff00:: is rejected at the compile entry
 * while the same rule with a contiguous mask compiles.
 */
static int
run_net6_mask_non_contiguous_rejected_test(void) {
	struct net6 srcs[1];
	struct net6 dsts[1];
	memset(&srcs, 0, sizeof(srcs));
	memset(&dsts, 0, sizeof(dsts));
	srcs[0].addr[0] = 0x20;
	srcs[0].mask[0] = 0xff;
	struct filter_rule rule;
	memset(&rule, 0, sizeof(rule));
	rule.net6.srcs = srcs;
	rule.net6.src_count = 1;
	rule.net6.dsts = dsts;
	rule.net6.dst_count = 1;
	const struct filter_rule *ptrs[1] = {&rule};

	void *arena = malloc(1 << 20);
	struct block_allocator ba;
	block_allocator_init(&ba);
	block_allocator_put_arena(&ba, arena, 1 << 20);
	struct memory_context mctx;
	memory_context_init(&mctx, "filter2_regression", &ba);

	struct filter filter;
	memset(&filter, 0, sizeof(filter));
	CHECK(filter_init(&filter, c_net6, ptrs, 1, &mctx) == 0);
	filter_free(&filter, c_net6);

	srcs[0].mask[2] = 0xff;
	memset(&filter, 0, sizeof(filter));
	CHECK(filter_init(&filter, c_net6, ptrs, 1, &mctx) == -1);

	free(arena);
	return 0;
}

int
main(void) {
	int failed = 0;
	failed += run_net4_dedup_wide_rule_test() != 0;
	failed += run_net6_dedup_wide_rule_test() != 0;
	failed += run_net4_dedup_large_fanout_test() != 0;
	failed += run_net6_dedup_large_fanout_test() != 0;
	failed += run_net4_mask_non_contiguous_rejected_test() != 0;
	failed += run_net6_mask_non_contiguous_rejected_test() != 0;
	return failed;
}
