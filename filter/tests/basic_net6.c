#include "attribute.h"
#include "filter.h"
#include "utils.h"
#include <assert.h>

////////////////////////////////////////////////////////////////////////////////

void
query_packet_and_expect_action(
	struct filter *filter,
	uint8_t src_ip[16],
	uint8_t dst_ip[16],
	uint32_t action
) {
	struct packet packet = make_packet_net6(src_ip, dst_ip, 100, 200);
	query_filter_and_expect_action(filter, &packet, action);
	free_packet(&packet);
}

void
query_packet_and_expect_no_actions(
	struct filter *filter, uint8_t src_ip[16], uint8_t dst_ip[16]
) {
	struct packet packet = make_packet_net6(src_ip, dst_ip, 100, 200);
	query_filter_and_expect_no_actions(filter, &packet);
	free_packet(&packet);
}

////////////////////////////////////////////////////////////////////////////////

void
make_addr(uint8_t *ip, uint8_t big, uint8_t c1, uint8_t low, uint8_t c2) {
	memset(ip, 0, 16);
	for (uint8_t i = 0; i < c2; ++i) {
		if (i % 2 == 0) {
			ip[8 - i / 2 - 1] = low << 4;
		} else {
			ip[8 - i / 2 - 1] |= low;
		}
	}
	for (uint8_t i = 0; i < c1; ++i) {
		if (i % 2 == 0) {
			ip[16 - i / 2 - 1] = big << 4;
		} else {
			ip[16 - i / 2 - 1] |= big;
		}
	}
}

////////////////////////////////////////////////////////////////////////////////

void
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
		.ip = {},
		.pref_hi = 40,
		.pref_lo = 24,
	};
	memset(net.ip, 0xaa, 8);
	memset(net.ip + 8, 0xbb, 8);
	builder_add_net6_dst(&builder, net);
	struct filter_rule rule = build_rule(&builder, 1);
	const struct filter_rule rules[1] = {rule};

	// init filter
	const struct filter_attribute *attrs[1] = {&attribute_net6_dst};
	struct filter filter;
	res = filter_init(&filter, attrs, 1, rules, 1, &mctx);
	assert(res == 0);

	// query packet 1
	{
		uint8_t src[16] = {};
		uint8_t dst[16];
		memset(dst, 0xaa, 8);
		memset(dst + 8, 0xbb, 8);
		query_packet_and_expect_action(&filter, src, dst, 1);
	}

	// query packet 2
	{
		uint8_t src[16] = {};
		uint8_t dst[16];
		memset(dst, 0xbb, 16);
		query_packet_and_expect_no_actions(&filter, src, dst);

		memset(dst, 0xaa, 16);
		query_packet_and_expect_no_actions(&filter, src, dst);
	}

	// query packet 3
	{
		uint8_t src[16] = {};
		uint8_t dst[16];
		memset(dst, 0, 16);
		dst[15] = dst[14] = dst[13] = dst[12] = dst[11] = 0xbb;
		dst[5] = 0xaa;
		dst[6] = dst[7] = 0xaa;
		query_packet_and_expect_action(&filter, src, dst, 1);
	}

	// query packet 4
	{
		uint8_t src[16] = {};
		uint8_t dst[16];
		memset(dst, 0xaa, 8);
		memset(dst + 8, 0xbb, 8);
		dst[11] = 0xb0;
		query_packet_and_expect_no_actions(&filter, src, dst);
	}

	// query packet 5
	{
		uint8_t src[16] = {};
		uint8_t dst[16];
		memset(dst, 0xaa, 8);
		memset(dst + 8, 0xbb, 8);
		dst[10] = 0xb0;
		query_packet_and_expect_action(&filter, src, dst, 1);
	}

	// query packet 6
	{
		uint8_t src[16] = {};
		uint8_t dst[16];
		memset(dst, 0xaa, 8);
		memset(dst + 8, 0xbb, 8);
		dst[5] = 0xa0;
		query_packet_and_expect_no_actions(&filter, src, dst);
	}

	// query packet 7
	{
		uint8_t src[16] = {};
		uint8_t dst[16];
		memset(dst, 0xaa, 8);
		memset(dst + 8, 0xbb, 8);
		dst[6] = 0xa0;
		query_packet_and_expect_no_actions(&filter, src, dst);
	}

	// query packet 8
	{
		uint8_t src[16] = {};
		uint8_t dst[16];
		memset(dst, 0xaa, 8);
		memset(dst + 8, 0xbb, 8);
		dst[4] = 0xa0;
		query_packet_and_expect_action(&filter, src, dst, 1);
	}

	// query packet 9
	{
		uint8_t src[16] = {};
		uint8_t dst[16];
		memset(dst, 0xaa, 8);
		memset(dst + 8, 0xbb, 8);
		memset(dst, 0, 5);
		memset(dst + 8, 0, 3);
		query_packet_and_expect_action(&filter, src, dst, 1);
	}

	filter_free(&filter);
}

////////////////////////////////////////////////////////////////////////////////

void
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
		.ip = {},
		.pref_hi = 36,
		.pref_lo = 20,
	};
	memset(net.ip, 0xaa, 8);
	memset(net.ip + 8, 0xbb, 8);
	builder_add_net6_dst(&builder, net);
	struct filter_rule rule = build_rule(&builder, 1);
	const struct filter_rule rules[1] = {rule};

	// init filter
	const struct filter_attribute *attrs[1] = {&attribute_net6_dst};
	struct filter filter;
	res = filter_init(&filter, attrs, 1, rules, 1, &mctx);
	assert(res == 0);

	// query packet 1
	{
		uint8_t src[16] = {};
		uint8_t dst[16];
		memset(dst, 0, 5);
		dst[5] = 0xa0;
		memset(dst + 6, 0xaa, 2);
		memset(dst + 8, 0, 3);
		dst[11] = 0xb0;
		memset(dst + 12, 0xbb, 4);
		query_packet_and_expect_action(&filter, src, dst, 1);
	}

	// query packet 2
	{
		uint8_t src[16] = {};
		uint8_t dst[16];
		memset(dst, 0, 5);
		dst[5] = 0x90;
		memset(dst + 6, 0xaa, 2);
		memset(dst + 8, 0, 3);
		dst[11] = 0xb0;
		memset(dst + 12, 0xbb, 4);
		query_packet_and_expect_no_actions(&filter, src, dst);
	}

	// query packet 3
	{
		uint8_t src[16] = {};
		uint8_t dst[16];
		memset(dst, 0, 5);
		dst[5] = 0xa0;
		memset(dst + 6, 0xaa, 2);
		memset(dst + 8, 0, 3);
		dst[11] = 0xf0;
		memset(dst + 12, 0xbb, 4);
		query_packet_and_expect_no_actions(&filter, src, dst);
	}

	filter_free(&filter);
}

////////////////////////////////////////////////////////////////////////////////

void
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
			.ip = {},
			.pref_hi = 36,
			.pref_lo = 20,
		};
		memset(src_net.ip, 0xaa, 8);
		memset(src_net.ip + 8, 0xbb, 8);
		builder_add_net6_src(&builder1, src_net);

		// add IPv6 destination address rule
		struct net6 dst_net = {
			.ip = {},
			.pref_hi = 40,
			.pref_lo = 24,
		};
		memset(dst_net.ip, 0xaa, 8);
		memset(dst_net.ip + 8, 0xbb, 8);
		builder_add_net6_dst(&builder1, dst_net);

		rule1 = build_rule(&builder1, 1);
	}

	struct filter_rule rule2;
	struct filter_rule_builder builder2;
	{
		builder_init(&builder2);

		// add IPv6 source address rule
		struct net6 src_net = {
			.ip = {},
			.pref_hi = 40,
			.pref_lo = 24,
		};
		memset(src_net.ip, 0xaa, 8);
		memset(src_net.ip + 8, 0xbb, 8);
		builder_add_net6_src(&builder2, src_net);

		// add IPv6 destination address rule
		struct net6 dst_net = {
			.ip = {},
			.pref_hi = 36,
			.pref_lo = 20,
		};
		memset(dst_net.ip, 0xaa, 8);
		memset(dst_net.ip + 8, 0xbb, 8);
		builder_add_net6_dst(&builder2, dst_net);

		rule2 = build_rule(&builder2, 2);
	}

	const struct filter_rule rules[2] = {rule1, rule2};

	// init filter
	const struct filter_attribute *attrs[2] = {
		&attribute_net6_src, &attribute_net6_dst
	};
	struct filter filter;
	res = filter_init(&filter, attrs, 2, rules, 2, &mctx);
	assert(res == 0);

	// query packet 1
	{
		uint8_t src[16];
		make_addr(src, 0xb, 10, 0xa, 6);

		uint8_t dst[16];
		make_addr(dst, 0xb, 10, 0xa, 6);

		query_packet_and_expect_action(&filter, src, dst, 1);
	}

	// query packet 2
	{
		uint8_t src[16];
		make_addr(src, 0xb, 10, 0xa, 6);

		uint8_t dst[16];
		make_addr(dst, 0xb, 9, 0xa, 5);

		query_packet_and_expect_action(&filter, src, dst, 2);
	}

	// query packet 3
	{
		uint8_t src[16];
		make_addr(src, 0xb, 9, 0xa, 6);

		uint8_t dst[16];
		make_addr(dst, 0xb, 10, 0xa, 6);

		query_packet_and_expect_action(&filter, src, dst, 1);
	}

	// query packet 4
	{
		uint8_t src[16];
		make_addr(src, 0xb, 9, 0xa, 5);

		uint8_t dst[16];
		make_addr(dst, 0xb, 9, 0xa, 5);

		query_packet_and_expect_no_actions(&filter, src, dst);
	}

	filter_free(&filter);
}

////////////////////////////////////////////////////////////////////////////////

int
main() {
	void *memory = malloc(1 << 24); // 16MB

	puts("test1...");
	test1(memory);

	puts("test2...");
	test2(memory);

	puts("test3...");
	test3(memory);

	puts("OK");

	return 0;
}