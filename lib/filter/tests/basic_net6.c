#include "lib/filter/compiler.h"
#include "lib/filter/filter.h"
#include "lib/filter/query.h"

#include "lib/filter/tests/helpers.h"
#include "lib/utils/packet.h"

#include "logging/log.h"
#include <assert.h>
#include <netinet/in.h>
#include <string.h>

FILTER_COMPILER_DECLARE(sign_net6_dst_compile, net6_dst);
FILTER_QUERY_DECLARE(sign_net6_dst, net6_dst);

FILTER_COMPILER_DECLARE(sign_net6_compile, net6_src, net6_dst);
FILTER_QUERY_DECLARE(sign_net6, net6_src, net6_dst);

static void
query_packet_and_expect_action(
	struct filter *filter,
	uint8_t src_ip[NET6_LEN],
	uint8_t dst_ip[NET6_LEN],
	uint32_t action,
	const char *sign
) {
	struct packet packet = {0};
	int res = fill_packet_net6(
		&packet, src_ip, dst_ip, 100, 200, IPPROTO_UDP, 0
	);
	assert(res == 0);

	struct packet *packet_ptr = &packet;
	uint32_t actions;

	if (strcmp(sign, "dst") == 0) {
		filter_query(filter, sign_net6_dst, &packet_ptr, &actions, 1);
	} else if (strcmp(sign, "both") == 0) {
		filter_query(filter, sign_net6, &packet_ptr, &actions, 1);
	} else {
		assert(0 && "Invalid sign");
	}

	assert(actions == action);
	free_packet(&packet);
}

static void
query_packet_and_expect_no_actions(
	struct filter *filter,
	uint8_t src_ip[NET6_LEN],
	uint8_t dst_ip[NET6_LEN],
	const char *sign
) {
	struct packet packet = {0};
	int res = fill_packet_net6(
		&packet, src_ip, dst_ip, 100, 200, IPPROTO_UDP, 0
	);
	assert(res == 0);

	struct packet *packet_ptr = &packet;
	uint32_t actions;

	if (strcmp(sign, "dst") == 0) {
		filter_query(filter, sign_net6_dst, &packet_ptr, &actions, 1);
	} else if (strcmp(sign, "both") == 0) {
		filter_query(filter, sign_net6, &packet_ptr, &actions, 1);
	} else {
		assert(0 && "Invalid sign");
	}

	assert(actions == FILTER_RULE_INVALID);
	free_packet(&packet);
}

// Here big and low is in [0, 15], c1 and c2 is in [0, 16]
// This function makes IPv6 address like
// 0xBB 0xBB .. 0xB0 00 .. 00 0xAA .. 0xA0 00 .. 00,
// here c1 Bs and c2 Ls, B means big and L means low.
static void
make_addr(
	uint8_t ip[NET6_LEN], uint8_t big, uint8_t c1, uint8_t low, uint8_t c2
) {
	memset(ip, 0, NET6_LEN);
	for (uint8_t i = 0; i < c1; ++i) {
		if (i % 2 == 0) {
			ip[i / 2] = big << 4;
		} else {
			ip[i / 2] |= big;
		}
	}
	for (uint8_t i = 0; i < c2; ++i) {
		if (i % 2 == 0) {
			ip[8 + i / 2] = low << 4;
		} else {
			ip[8 + i / 2] |= low;
		}
	}
}

static void
test1(void *memory) {
	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context mctx;
	int res = memory_context_init(&mctx, "test", &allocator);
	assert(res == 0);

	// build rules
	struct filter_rule_builder builder;
	builder_init(&builder);
	struct net6 net = {
		.addr = {},
		.mask =
			{
				0xff,
				0xff,
				0xff,
				0xff,
				0xff,
				0x00,
				0x00,
				0x00,
				0xff,
				0xff,
				0xff,
				0x00,
				0x00,
				0x00,
				0x00,
				0x00,
			},
	};
	make_addr(net.addr, 0xB, 16, 0xA, 16);
	builder_add_net6_dst(&builder, net);
	struct filter_rule rule = build_rule(&builder);
	const struct filter_rule *rule_ptrs[1] = {&rule};

	// init filter
	struct filter filter;
	res = filter_init(
		&filter, sign_net6_dst_compile, rule_ptrs, 1, &mctx, NULL
	);
	assert(res == 0);

	// query packet 1
	{
		uint8_t src[NET6_LEN] = {};
		uint8_t dst[NET6_LEN];
		make_addr(dst, 0xB, 16, 0xA, 16);
		query_packet_and_expect_action(&filter, src, dst, 0, "dst");
	}

	// query packet 2
	{
		uint8_t src[NET6_LEN] = {};
		uint8_t dst[NET6_LEN];
		memset(dst, 0xBB, NET6_LEN);
		query_packet_and_expect_no_actions(&filter, src, dst, "dst");

		memset(dst, 0xAA, NET6_LEN);
		query_packet_and_expect_no_actions(&filter, src, dst, "dst");
	}

	// query packet 3
	{
		uint8_t src[NET6_LEN] = {};
		uint8_t dst[NET6_LEN];
		memset(dst, 0, NET6_LEN);
		dst[0] = dst[1] = dst[2] = dst[3] = dst[4] = 0xBB;
		dst[8] = dst[9] = dst[10] = 0xAA;
		query_packet_and_expect_action(&filter, src, dst, 0, "dst");
	}

	// query packet 4
	{
		uint8_t src[NET6_LEN] = {};
		uint8_t dst[NET6_LEN];
		make_addr(dst, 0xB, 16, 0xA, 16);
		dst[4] = 0xB0;
		query_packet_and_expect_no_actions(&filter, src, dst, "dst");
	}

	// query packet 5
	{
		uint8_t src[NET6_LEN] = {};
		uint8_t dst[NET6_LEN];
		make_addr(dst, 0xB, 16, 0xA, 16);
		dst[5] = 0xB0;
		query_packet_and_expect_action(&filter, src, dst, 0, "dst");
	}

	// query packet 6
	{
		uint8_t src[NET6_LEN] = {};
		uint8_t dst[NET6_LEN];
		make_addr(dst, 0xB, 16, 0xA, 16);
		dst[10] = 0xA0;
		query_packet_and_expect_no_actions(&filter, src, dst, "dst");
	}

	// query packet 7
	{
		uint8_t src[NET6_LEN] = {};
		uint8_t dst[NET6_LEN];
		make_addr(dst, 0xB, 16, 0xA, 16);
		dst[9] = 0xA0;
		query_packet_and_expect_no_actions(&filter, src, dst, "dst");
	}

	// query packet 8
	{
		uint8_t src[NET6_LEN] = {};
		uint8_t dst[NET6_LEN];
		make_addr(dst, 0xB, 16, 0xA, 16);
		dst[11] = 0xA0;
		query_packet_and_expect_action(&filter, src, dst, 0, "dst");
	}

	// query packet 9
	{
		uint8_t src[16] = {};
		uint8_t dst[16] = {
			0xbb,
			0xbb,
			0xbb,
			0xbb,
			0xbb,
			0x00,
			0x00,
			0x00,
			0xaa,
			0xaa,
			0xaa,
			0x00,
			0x00,
			0x00,
			0x00,
			0x00,
		};
		query_packet_and_expect_action(&filter, src, dst, 0, "dst");
	}

	filter_free(&filter, sign_net6_dst_compile);
	memory_context_fini(&mctx);
}

static void
test2(void *memory) {
	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context mctx;
	int res = memory_context_init(&mctx, "test", &allocator);
	assert(res == 0);

	// build rules
	struct filter_rule_builder builder;
	builder_init(&builder);
	struct net6 net = {
		.addr = {},
		.mask =
			{
				0xff,
				0xff,
				0xff,
				0xff,
				0xf0,
				0x00,
				0x00,
				0x00,
				0xff,
				0xff,
				0xf0,
				0x00,
				0x00,
				0x00,
				0x00,
				0x00,
			},
	};
	memset(net.addr, 0xBb, 8);
	memset(net.addr + 8, 0xAa, 8);
	builder_add_net6_dst(&builder, net);
	struct filter_rule rule = build_rule(&builder);
	const struct filter_rule *rule_ptrs[1] = {&rule};

	// init filter
	struct filter filter;
	res = filter_init(
		&filter, sign_net6_dst_compile, rule_ptrs, 1, &mctx, NULL
	);
	assert(res == 0);

	// query packet 1
	{
		uint8_t src[16] = {};
		uint8_t dst[16] = {
			0xbb,
			0xbb,
			0xbb,
			0xbb,
			0xb0,
			0x00,
			0x00,
			0x00,
			0xaa,
			0xaa,
			0xa0,
			0x00,
			0x00,
			0x00,
			0x00,
			0x00,
		};
		query_packet_and_expect_action(&filter, src, dst, 0, "dst");
	}

	// query packet 2
	{
		uint8_t src[16] = {};
		uint8_t dst[16] = {
			0xbb,
			0xbb,
			0xbb,
			0xbb,
			0xb0,
			0x00,
			0x00,
			0x00,
			0xaa,
			0xaa,
			0x90,
			0x00,
			0x00,
			0x00,
			0x00,
			0x00,
		};
		query_packet_and_expect_no_actions(&filter, src, dst, "dst");
	}

	// query packet 3
	{
		uint8_t src[16] = {};
		uint8_t dst[16] = {
			0xbb,
			0xbb,
			0xbb,
			0xbb,
			0xf0,
			0x00,
			0x00,
			0x00,
			0xaa,
			0xaa,
			0xa0,
			0x00,
			0x00,
			0x00,
			0x00,
			0x00,
		};
		query_packet_and_expect_no_actions(&filter, src, dst, "dst");
	}

	filter_free(&filter, sign_net6_dst_compile);
	memory_context_fini(&mctx);
}

static void
test3(void *memory) {
	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context mctx;
	int res = memory_context_init(&mctx, "test", &allocator);
	assert(res == 0);

	// build rules

	// rule1
	struct filter_rule rule1;
	struct filter_rule_builder builder1;
	{
		builder_init(&builder1);

		// add IPv6 source address rule
		struct net6 src_net = {
			.addr = {},
			.mask =
				{
					0xff,
					0xff,
					0xff,
					0xff,
					0xf0,
					0x00,
					0x00,
					0x00,
					0xff,
					0xff,
					0xf0,
					0x00,
					0x00,
					0x00,
					0x00,
					0x00,
				},
		};
		make_addr(src_net.addr, 0xB, 16, 0xA, 16);
		builder_add_net6_src(&builder1, src_net);

		// add IPv6 destination address rule
		struct net6 dst_net = {
			.addr = {},
			.mask =
				{
					0xff,
					0xff,
					0xff,
					0xff,
					0xff,
					0x00,
					0x00,
					0x00,
					0xff,
					0xff,
					0xff,
					0x00,
					0x00,
					0x00,
					0x00,
					0x00,
				},
		};
		make_addr(dst_net.addr, 0xB, 16, 0xA, 16);
		builder_add_net6_dst(&builder1, dst_net);

		rule1 = build_rule(&builder1);
	}

	struct filter_rule rule2;
	struct filter_rule_builder builder2;
	{
		builder_init(&builder2);

		// add IPv6 source address rule
		struct net6 src_net = {
			.addr = {},
			.mask =
				{
					0xff,
					0xff,
					0xff,
					0xff,
					0xff,
					0x00,
					0x00,
					0x00,
					0xff,
					0xff,
					0xff,
					0x00,
					0x00,
					0x00,
					0x00,
					0x00,
				},
		};
		make_addr(src_net.addr, 0xB, 16, 0xA, 16);
		builder_add_net6_src(&builder2, src_net);

		// add IPv6 destination address rule
		struct net6 dst_net = {
			.addr = {},
			.mask =
				{
					0xff,
					0xff,
					0xff,
					0xff,
					0xf0,
					0x00,
					0x00,
					0x00,
					0xff,
					0xff,
					0xf0,
					0x00,
					0x00,
					0x00,
					0x00,
					0x00,
				},
		};
		make_addr(dst_net.addr, 0xB, 16, 0xA, 16);
		builder_add_net6_dst(&builder2, dst_net);

		rule2 = build_rule(&builder2);
	}

	const struct filter_rule *rule_ptrs[2] = {&rule1, &rule2};

	// init filter
	struct filter filter;
	res = filter_init(
		&filter, sign_net6_compile, rule_ptrs, 2, &mctx, NULL
	);
	assert(res == 0);

	// query packet 1
	{
		uint8_t src[16];
		make_addr(src, 0xB, 10, 0xA, 6);

		uint8_t dst[16];
		make_addr(dst, 0xB, 10, 0xA, 6);

		query_packet_and_expect_action(&filter, src, dst, 0, "both");
	}

	// query packet 2
	{
		uint8_t src[16];
		make_addr(src, 0xB, 10, 0xA, 6);

		uint8_t dst[16];
		make_addr(dst, 0xB, 9, 0xA, 5);

		query_packet_and_expect_action(&filter, src, dst, 1, "both");
	}

	// query packet 3
	{
		uint8_t src[16];
		make_addr(src, 0xB, 9, 0xA, 6);

		uint8_t dst[16];
		make_addr(dst, 0xB, 10, 0xA, 6);

		query_packet_and_expect_action(&filter, src, dst, 0, "both");
	}

	// query packet 4
	{
		uint8_t src[16];
		make_addr(src, 0xB, 9, 0xA, 5);

		uint8_t dst[16];
		make_addr(dst, 0xB, 9, 0xA, 5);

		query_packet_and_expect_no_actions(&filter, src, dst, "both");
	}

	filter_free(&filter, sign_net6_compile);
	memory_context_fini(&mctx);
}

// Shared per-direction half-classification (filter_net6_share_init).

// Recomputes the leaf slot a plain (non-shared) net6 classifier assigns to
// addr, the same way FILTER_ATTR_QUERY_FUNC(net6_src/net6_dst) does.
static uint32_t
local_net6_slot(struct net6_classifier *c, const uint8_t addr[NET6_LEN]) {
	uint32_t hi = lpm8_lookup(&c->hi, addr);
	uint32_t lo = lpm8_lookup(&c->lo, addr + 8);
	return value_table_get(&c->comb, hi, lo);
}

// Recomputes the leaf slot the shared union classification assigns to addr
// for one of its two local classifiers, the way acl_handle_packets does:
// one union lookup translated through that classifier's remap arrays.
static uint32_t
shared_net6_slot(
	struct net6_share_dir *dir,
	uint32_t *remap_hi,
	uint32_t *remap_lo,
	struct net6_classifier *local,
	const uint8_t addr[NET6_LEN]
) {
	uint32_t hi = lpm8_lookup(&dir->hi, addr);
	uint32_t lo = lpm8_lookup(&dir->lo, addr + 8);
	return value_table_get(&local->comb, remap_hi[hi], remap_lo[lo]);
}

// Verifies that the shared leaf slot for both local classifiers matches
// their own plain leaf lookup for addr, on both the src and dst share
// directories built by test_share.
static void
assert_share_matches_local(
	struct net6_share_dir *share_src,
	struct net6_share_dir *share_dst,
	struct net6_classifier *local_a_src,
	struct net6_classifier *local_a_dst,
	struct net6_classifier *local_b_src,
	struct net6_classifier *local_b_dst,
	const uint8_t addr[NET6_LEN]
) {
	uint32_t *remap_hi_a = ADDR_OF(&share_src->remap_hi_a);
	uint32_t *remap_lo_a = ADDR_OF(&share_src->remap_lo_a);
	uint32_t *remap_hi_b = ADDR_OF(&share_src->remap_hi_b);
	uint32_t *remap_lo_b = ADDR_OF(&share_src->remap_lo_b);

	assert(local_net6_slot(local_a_src, addr) ==
	       shared_net6_slot(
		       share_src, remap_hi_a, remap_lo_a, local_a_src, addr
	       ));
	assert(local_net6_slot(local_b_src, addr) ==
	       shared_net6_slot(
		       share_src, remap_hi_b, remap_lo_b, local_b_src, addr
	       ));

	remap_hi_a = ADDR_OF(&share_dst->remap_hi_a);
	remap_lo_a = ADDR_OF(&share_dst->remap_lo_a);
	remap_hi_b = ADDR_OF(&share_dst->remap_hi_b);
	remap_lo_b = ADDR_OF(&share_dst->remap_lo_b);

	assert(local_net6_slot(local_a_dst, addr) ==
	       shared_net6_slot(
		       share_dst, remap_hi_a, remap_lo_a, local_a_dst, addr
	       ));
	assert(local_net6_slot(local_b_dst, addr) ==
	       shared_net6_slot(
		       share_dst, remap_hi_b, remap_lo_b, local_b_dst, addr
	       ));
}

// Verifies filter_net6_share_init against two local classifiers (mimicking
// filter_ip6 and filter_ip6_port) whose partitions genuinely differ: an
// address landing in one classifier's partition but not the other's is the
// case a broken remap would smooth over, and netB1 below uses the same
// non-contiguous-across-128-bit-but-per-64-bit-half-contiguous mask shape
// as test1's rule (5 hi bytes + 3 lo bytes, with a gap in between).
static void
test_share(void *memory) {
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context mctx;
	int res = memory_context_init(&mctx, "test", &allocator);
	assert(res == 0);

	// netA1: broad /8 supernet 0xBB::/8, any lo half.
	struct net6 net_a1 = {.addr = {}, .mask = {}};
	net_a1.mask[0] = 0xff;
	make_addr(net_a1.addr, 0xB, 2, 0, 0);

	// netA2: broad /8 supernet 0xEE::/8, disjoint from B's networks.
	struct net6 net_a2 = {.addr = {}, .mask = {}};
	net_a2.mask[0] = 0xff;
	make_addr(net_a2.addr, 0xE, 2, 0, 0);

	// netB1: nested inside netA1's supernet, narrower than it, with a
	// bi-contiguous but not whole-address-contiguous mask (5 hi bytes,
	// then a gap, then 3 lo bytes).
	struct net6 net_b1 = {
		.addr = {},
		.mask =
			{0xff,
			 0xff,
			 0xff,
			 0xff,
			 0xff,
			 0x00,
			 0x00,
			 0x00,
			 0xff,
			 0xff,
			 0xff,
			 0x00,
			 0x00,
			 0x00,
			 0x00,
			 0x00},
	};
	make_addr(net_b1.addr, 0xB, 16, 0xA, 16);

	// netB2: broad /8 supernet 0xCC::/8, disjoint from A's networks.
	struct net6 net_b2 = {.addr = {}, .mask = {}};
	net_b2.mask[0] = 0xff;
	make_addr(net_b2.addr, 0xC, 2, 0, 0);

	struct filter_rule_builder builder_a;
	builder_init(&builder_a);
	builder_add_net6_src(&builder_a, net_a1);
	builder_add_net6_dst(&builder_a, net_a1);
	struct filter_rule rule_a1 = build_rule(&builder_a);

	struct filter_rule_builder builder_a2;
	builder_init(&builder_a2);
	builder_add_net6_src(&builder_a2, net_a2);
	builder_add_net6_dst(&builder_a2, net_a2);
	struct filter_rule rule_a2 = build_rule(&builder_a2);

	struct filter_rule_builder builder_b1;
	builder_init(&builder_b1);
	builder_add_net6_src(&builder_b1, net_b1);
	builder_add_net6_dst(&builder_b1, net_b1);
	struct filter_rule rule_b1 = build_rule(&builder_b1);

	struct filter_rule_builder builder_b2;
	builder_init(&builder_b2);
	builder_add_net6_src(&builder_b2, net_b2);
	builder_add_net6_dst(&builder_b2, net_b2);
	struct filter_rule rule_b2 = build_rule(&builder_b2);

	const struct filter_rule *rule_ptrs_a[2] = {&rule_a1, &rule_a2};
	const struct filter_rule *rule_ptrs_b[2] = {&rule_b1, &rule_b2};

	struct filter filter_a;
	res = filter_init(
		&filter_a, sign_net6_compile, rule_ptrs_a, 2, &mctx, NULL
	);
	assert(res == 0);

	struct filter filter_b;
	res = filter_init(
		&filter_b, sign_net6_compile, rule_ptrs_b, 2, &mctx, NULL
	);
	assert(res == 0);

	struct net6_classifier *local_a_src =
		(struct net6_classifier *)ADDR_OF(&filter_a.v[2].data);
	struct net6_classifier *local_a_dst =
		(struct net6_classifier *)ADDR_OF(&filter_a.v[3].data);
	struct net6_classifier *local_b_src =
		(struct net6_classifier *)ADDR_OF(&filter_b.v[2].data);
	struct net6_classifier *local_b_dst =
		(struct net6_classifier *)ADDR_OF(&filter_b.v[3].data);

	// The union projection covers every network of both local
	// classifiers, mirroring how acl_module_init_net6_share collects
	// every ip6 rule regardless of which of filter_ip6/filter_ip6_port
	// it ends up in.
	const struct filter_rule *rule_ptrs_union[4] = {
		&rule_a1, &rule_a2, &rule_b1, &rule_b2
	};

	struct net6_share_dir share_src;
	res = filter_net6_share_init(
		&mctx,
		rule_ptrs_union,
		4,
		1,
		local_a_src,
		local_b_src,
		&share_src,
		NULL
	);
	assert(res == 0);

	struct net6_share_dir share_dst;
	res = filter_net6_share_init(
		&mctx,
		rule_ptrs_union,
		4,
		0,
		local_a_dst,
		local_b_dst,
		&share_dst,
		NULL
	);
	assert(res == 0);

	// Matches netA1 (0xBB::/8) and, since it is netB1's own address,
	// netB1 too: a partition both local classifiers agree on.
	uint8_t addr_both[NET6_LEN];
	make_addr(addr_both, 0xB, 16, 0xA, 16);

	// Matches netA1's /8 but not netB1's narrower prefix: a partition of
	// local_a that local_b does not share.
	uint8_t addr_only_a[NET6_LEN] = {0};
	addr_only_a[0] = 0xBB;

	// Matches netB2's /8, disjoint from both of local_a's networks: a
	// partition of local_b that local_a does not share.
	uint8_t addr_only_b[NET6_LEN] = {0};
	addr_only_b[0] = 0xCC;

	// Matches neither classifier's networks.
	uint8_t addr_neither[NET6_LEN] = {0};
	addr_neither[0] = 0x11;

	assert_share_matches_local(
		&share_src,
		&share_dst,
		local_a_src,
		local_a_dst,
		local_b_src,
		local_b_dst,
		addr_both
	);
	assert_share_matches_local(
		&share_src,
		&share_dst,
		local_a_src,
		local_a_dst,
		local_b_src,
		local_b_dst,
		addr_only_a
	);
	assert_share_matches_local(
		&share_src,
		&share_dst,
		local_a_src,
		local_a_dst,
		local_b_src,
		local_b_dst,
		addr_only_b
	);
	assert_share_matches_local(
		&share_src,
		&share_dst,
		local_a_src,
		local_a_dst,
		local_b_src,
		local_b_dst,
		addr_neither
	);

	filter_net6_share_dir_free(&mctx, &share_dst);
	filter_net6_share_dir_free(&mctx, &share_src);
	filter_free(&filter_b, sign_net6_compile);
	filter_free(&filter_a, sign_net6_compile);
	memory_context_fini(&mctx);
}

int
main() {
	log_enable_name("debug");
	void *memory = malloc(1 << 24); // 16MB

	LOG(INFO, "Running test1...");
	test1(memory);
	LOG(INFO, "test1 passed");

	LOG(INFO, "Running test2...");
	test2(memory);
	LOG(INFO, "test2 passed");

	LOG(INFO, "Running test3...");
	test3(memory);
	LOG(INFO, "test3 passed");

	LOG(INFO, "Running test_share...");
	test_share(memory);
	LOG(INFO, "test_share passed");

	LOG(INFO, "All tests passed");

	free(memory);

	return 0;
}
