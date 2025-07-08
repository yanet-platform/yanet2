#include "util.h"

#include "attribute.h"
#include "common/memory_block.h"
#include "filter.h"

#include <rte_ip.h>

#include <assert.h>
#include <stdio.h>

int
main() {
	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	void *memory = malloc(1 << 24); // 16MB
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	// filter attributes
	struct filter_attribute attributes[2] = {
		port_src_attribute, attribute_port_dst
	};

	// action 1:
	//	src_port: [5..7]
	//	dst_port: [1..5]
	struct filter_action_builder builder1;
	builder_init(&builder1);
	builder_add_src_port_range(&builder1, 5, 7);
	builder_add_dst_port_range(&builder1, 1, 5);
	struct filter_action action1 = build_action(&builder1, 1);

	// action 2:
	//	src_port: [6..8]
	//	dst_port: [3..4]
	struct filter_action_builder builder2;
	builder_init(&builder2);
	builder_add_src_port_range(&builder2, 6, 8);
	builder_add_dst_port_range(&builder2, 3, 4);
	struct filter_action action2 = build_action(&builder2, 2);

	struct filter_action actions[2] = {action1, action2};

	// init filter
	struct filter filter;
	res = filter_init(&filter, attributes, 2, actions, 2, &memory_context);
	assert(res == 0);

	{
		// src_port: 6
		// dst_port: 3
		struct packet packet = make_packet(0, 0, 6, 3);
		query_filter_and_expect_action(&filter, &packet, 1);
		free_packet(&packet);
	}

	{
		// src_port: 8
		// dst_port: 3
		struct packet packet = make_packet(0, 0, 8, 3);
		query_filter_and_expect_action(&filter, &packet, 2);
		free_packet(&packet);
	}

	free(memory);

	puts("OK!");
	return 0;
}