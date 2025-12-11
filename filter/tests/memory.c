#include "common/memory.h"
#include "utils.h"

#include "attribute.h"
#include "common/memory_block.h"
#include "filter.h"

#include <rte_ip.h>

#include <assert.h>
#include <stdio.h>

////////////////////////////////////////////////////////////////////////////////

void
query_and_expect_action(
	struct filter *filter,
	uint16_t src_port,
	uint16_t dst_port,
	uint32_t expected
) {
	struct packet packet = make_packet4(
		ip(0, 0, 0, 0),
		ip(0, 0, 0, 0),
		src_port,
		dst_port,
		IPPROTO_UDP,
		0,
		0
	);
	query_filter_and_expect_action(filter, &packet, expected);
	free_packet(&packet);
}

void
query_and_expect_no_action(
	struct filter *filter, uint16_t src_port, uint16_t dst_port
) {
	struct packet packet = make_packet4(
		ip(0, 0, 0, 0),
		ip(0, 0, 0, 0),
		src_port,
		dst_port,
		IPPROTO_UDP,
		0,
		0
	);
	query_filter_and_expect_no_actions(filter, &packet);
	free_packet(&packet);
}

////////////////////////////////////////////////////////////////////////////////

void
test_src_dst_ports(void *memory) {
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	// filter attributes
	const struct filter_attribute *attributes[2] = {
		&attribute_port_src, &attribute_port_dst
	};

	// action 1:
	//	src_port: [5..7]
	//	dst_port: [1..5]
	struct filter_rule_builder builder1;
	builder_init(&builder1);
	builder_add_port_src_range(&builder1, 5, 7);
	builder_add_port_dst_range(&builder1, 1, 5);
	struct filter_rule action1 = build_rule(&builder1, 1);

	// action 2:
	//	src_port: [6..8]
	//	dst_port: [3..4]
	struct filter_rule_builder builder2;
	builder_init(&builder2);
	builder_add_port_src_range(&builder2, 6, 8);
	builder_add_port_dst_range(&builder2, 3, 4);
	struct filter_rule action2 = build_rule(&builder2, 2);

	struct filter_rule actions[2] = {action1, action2};

	// init filter
	struct filter filter;
	res = filter_init(&filter, attributes, 2, actions, 2, &memory_context);
	assert(res == 0);

	query_and_expect_action(&filter, 6, 3, 1);
	query_and_expect_action(&filter, 8, 3, 2);

	filter_free(&filter);

	memory_bfree(&memory_context, memory, 1 << 24);
	void *mem = memory_balloc(&memory_context, 1 << 24);
	assert(mem == memory);
}

void
test_src_port_only(void *memory) {
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	// action 1:
	//	src_port: [500..700]
	struct filter_rule_builder builder1;
	builder_init(&builder1);
	builder_add_port_src_range(&builder1, 500, 700);
	struct filter_rule action1 = build_rule(&builder1, 1);

	// action 2:
	//	src_port: [600..800]
	struct filter_rule_builder builder2;
	builder_init(&builder2);
	builder_add_port_src_range(&builder2, 600, 800);
	struct filter_rule action2 = build_rule(&builder2, 2);

	struct filter_rule actions[2] = {action1, action2};

	const struct filter_attribute *attrs[1] = {&attribute_port_src};

	// init filter
	struct filter filter;
	res = filter_init(&filter, attrs, 1, actions, 2, &memory_context);
	assert(res == 0);

	query_and_expect_action(&filter, 500, 0, 1);
	query_and_expect_action(&filter, 600, 0, 1);
	query_and_expect_action(&filter, 700, 0, 1);
	query_and_expect_action(&filter, 701, 0, 2);
	query_and_expect_action(&filter, 800, 0, 2);

	query_and_expect_no_action(&filter, 499, 0);
	query_and_expect_no_action(&filter, 801, 0);

	filter_free(&filter);

	memory_bfree(&memory_context, memory, 1 << 24);
	void *mem = memory_balloc(&memory_context, 1 << 24);
	assert(mem == memory);
}

int
main() {
	void *memory = malloc(1 << 24);

	for (size_t i = 0; i < 10; ++i) {
		test_src_port_only(memory);
		if (i >= 5) {
			memset(memory, (int)i, 1 << 24);
		}
	}

	for (size_t i = 0; i < 10; ++i) {
		test_src_dst_ports(memory);
		if (i >= 5) {
			memset(memory, (int)i, 1 << 24);
		}
	}

	free(memory);

	puts("OK!");

	return 0;
}
