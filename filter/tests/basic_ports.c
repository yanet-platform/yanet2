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
	// init memory
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
}

////////////////////////////////////////////////////////////////////////////////

void
src_dst_ports(void *memory) {
	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	const struct filter_attribute *attributes[2] = {
		&attribute_port_src, &attribute_port_dst
	};

	// rule 1
	//	src: 1024-5016
	//	dst: 500-50000
	struct filter_rule_builder builder1;
	builder_init(&builder1);
	builder_add_port_src_range(&builder1, 1024, 5016);
	builder_add_port_dst_range(&builder1, 500, 50000);
	struct filter_rule action1 = build_rule(&builder1, 1);

	// rule 2
	//	src: 30-500
	//	dst: 400-12040
	struct filter_rule_builder builder2;
	builder_init(&builder2);
	builder_add_port_src_range(&builder2, 30, 500);
	builder_add_port_dst_range(&builder2, 400, 12040);
	struct filter_rule action2 = build_rule(&builder2, 2);

	// rule 3
	//	src: 100-2014
	//	dst: 5000-15000
	struct filter_rule_builder builder3;
	builder_init(&builder3);
	builder_add_port_src_range(&builder3, 100, 2014);
	builder_add_port_dst_range(&builder3, 5000, 15000);
	struct filter_rule action3 = build_rule(&builder3, 3);

	// filter actions
	struct filter_rule actions[3] = {action1, action2, action3};

	struct filter filter;
	res = filter_init(&filter, attributes, 2, actions, 3, &memory_context);
	assert(res == 0);

	query_and_expect_action(&filter, 30, 400, 2);
	query_and_expect_action(&filter, 35, 445, 2);
	query_and_expect_action(&filter, 120, 6000, 2);
	query_and_expect_action(&filter, 300, 12040, 2);

	query_and_expect_action(&filter, 300, 12041, 3);
	query_and_expect_action(&filter, 300, 14900, 3);
	query_and_expect_action(&filter, 300, 15000, 3);

	query_and_expect_action(&filter, 600, 14000, 3);
	query_and_expect_action(&filter, 1024, 14000, 1);
	query_and_expect_action(&filter, 2000, 13000, 1);
	query_and_expect_action(&filter, 5000, 500, 1);
	query_and_expect_action(&filter, 5000, 50000, 1);
	query_and_expect_action(&filter, 5016, 500, 1);

	query_and_expect_no_action(&filter, 5017, 3000);
	query_and_expect_no_action(&filter, 20, 3000);

	filter_free(&filter);
}

////////////////////////////////////////////////////////////////////////////////

void
test_any_port(void *memory) {
	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	const struct filter_attribute *attributes[2] = {
		&attribute_port_src, &attribute_port_dst
	};

	// rule 1
	//	src: 1024-5016
	//	dst: any
	struct filter_rule_builder builder1;
	builder_init(&builder1);
	builder_add_port_src_range(&builder1, 1024, 5016);
	builder_add_port_dst_range(&builder1, 0, 65535);
	struct filter_rule action1 = build_rule(&builder1, 1);

	// rule 2
	//	src: any
	//	dst: 400-12040
	struct filter_rule_builder builder2;
	builder_init(&builder2);
	builder_add_port_src_range(&builder2, 0, 65535);
	builder_add_port_dst_range(&builder2, 400, 12040);
	struct filter_rule action2 = build_rule(&builder2, 2);

	// rule 3
	//	src: 100-2014
	//	dst: 5000-15000
	struct filter_rule_builder builder3;
	builder_init(&builder3);
	builder_add_port_src_range(&builder3, 100, 2014);
	builder_add_port_dst_range(&builder3, 5000, 15000);
	struct filter_rule action3 = build_rule(&builder3, 3);

	struct filter_rule actions[3] = {action1, action2, action3};

	struct filter filter;
	res = filter_init(&filter, attributes, 2, actions, 3, &memory_context);
	assert(res == 0);

	query_and_expect_action(&filter, 1025, 11111, 1);
	query_and_expect_action(&filter, 11111, 404, 2);
	query_and_expect_action(&filter, 500, 15000, 3);

	query_and_expect_no_action(&filter, 1000, 200);

	filter_free(&filter);
}

////////////////////////////////////////////////////////////////////////////////

int
main() {
	void *memory = malloc(1 << 24);

	test_src_dst_ports(memory);
	src_dst_ports(memory);
	test_any_port(memory);

	puts("OK!");

	free(memory);
	return 0;
}
