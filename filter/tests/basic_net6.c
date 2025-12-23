#include "filter/compiler.h"
#include "filter/filter.h"
#include "filter/query.h"

#include "filter/tests/helpers.h"
#include "lib/utils/packet.h"

#include "logging/log.h"
#include <assert.h>
#include <netinet/in.h>
#include <string.h>

FILTER_COMPILER_DECLARE(sign_net6_dst, net6_dst);
FILTER_QUERY_DECLARE(sign_net6_dst, net6_dst);

FILTER_COMPILER_DECLARE(sign_net6, net6_src, net6_dst);
FILTER_QUERY_DECLARE(sign_net6, net6_src, net6_dst);

////////////////////////////////////////////////////////////////////////////////

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

	uint32_t *actions = NULL;
	uint32_t actions_count = 0;

	if (strcmp(sign, "dst") == 0) {
		FILTER_QUERY(
			filter, sign_net6_dst, &packet, &actions, &actions_count
		);
	} else if (strcmp(sign, "both") == 0) {
		FILTER_QUERY(
			filter, sign_net6, &packet, &actions, &actions_count
		);
	} else {
		assert(0 && "Invalid sign");
	}

	assert(actions_count >= 1);
	assert(actions[0] == action);
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

	uint32_t *actions = NULL;
	uint32_t actions_count = 0;

	if (strcmp(sign, "dst") == 0) {
		FILTER_QUERY(
			filter, sign_net6_dst, &packet, &actions, &actions_count
		);
	} else if (strcmp(sign, "both") == 0) {
		FILTER_QUERY(
			filter, sign_net6, &packet, &actions, &actions_count
		);
	} else {
		assert(0 && "Invalid sign");
	}

	assert(actions_count == 0);
	free_packet(&packet);
}

////////////////////////////////////////////////////////////////////////////////

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

////////////////////////////////////////////////////////////////////////////////

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
	struct filter_rule rule = build_rule(&builder, 1);
	const struct filter_rule rules[1] = {rule};

	// init filter
	struct filter filter;
	res = FILTER_INIT(&filter, sign_net6_dst, rules, 1, &mctx);
	assert(res == 0);

	// query packet 1
	{
		uint8_t src[NET6_LEN] = {};
		uint8_t dst[NET6_LEN];
		make_addr(dst, 0xB, 16, 0xA, 16);
		query_packet_and_expect_action(&filter, src, dst, 1, "dst");
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
		query_packet_and_expect_action(&filter, src, dst, 1, "dst");
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
		query_packet_and_expect_action(&filter, src, dst, 1, "dst");
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
		query_packet_and_expect_action(&filter, src, dst, 1, "dst");
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
		query_packet_and_expect_action(&filter, src, dst, 1, "dst");
	}

	FILTER_FREE(&filter, sign_net6_dst);
}

////////////////////////////////////////////////////////////////////////////////

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
	struct filter_rule rule = build_rule(&builder, 1);
	const struct filter_rule rules[1] = {rule};

	// init filter
	struct filter filter;
	res = FILTER_INIT(&filter, sign_net6_dst, rules, 1, &mctx);
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
		query_packet_and_expect_action(&filter, src, dst, 1, "dst");
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

	FILTER_FREE(&filter, sign_net6_dst);
}

////////////////////////////////////////////////////////////////////////////////

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

		rule1 = build_rule(&builder1, 1);
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

		rule2 = build_rule(&builder2, 2);
	}

	const struct filter_rule rules[2] = {rule1, rule2};

	// init filter
	struct filter filter;
	res = FILTER_INIT(&filter, sign_net6, rules, 2, &mctx);
	assert(res == 0);

	// query packet 1
	{
		uint8_t src[16];
		make_addr(src, 0xB, 10, 0xA, 6);

		uint8_t dst[16];
		make_addr(dst, 0xB, 10, 0xA, 6);

		query_packet_and_expect_action(&filter, src, dst, 1, "both");
	}

	// query packet 2
	{
		uint8_t src[16];
		make_addr(src, 0xB, 10, 0xA, 6);

		uint8_t dst[16];
		make_addr(dst, 0xB, 9, 0xA, 5);

		query_packet_and_expect_action(&filter, src, dst, 2, "both");
	}

	// query packet 3
	{
		uint8_t src[16];
		make_addr(src, 0xB, 9, 0xA, 6);

		uint8_t dst[16];
		make_addr(dst, 0xB, 10, 0xA, 6);

		query_packet_and_expect_action(&filter, src, dst, 1, "both");
	}

	// query packet 4
	{
		uint8_t src[16];
		make_addr(src, 0xB, 9, 0xA, 5);

		uint8_t dst[16];
		make_addr(dst, 0xB, 9, 0xA, 5);

		query_packet_and_expect_no_actions(&filter, src, dst, "both");
	}

	FILTER_FREE(&filter, sign_net6);
}

////////////////////////////////////////////////////////////////////////////////

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

	LOG(INFO, "All tests passed");

	free(memory);

	return 0;
}
