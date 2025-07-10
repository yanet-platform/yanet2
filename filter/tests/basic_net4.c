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
		attribute_net4_src, attribute_net4_dst
	};

	// action 1:
	struct filter_action_builder builder1;
	builder_init(&builder1);
	builder_add_net4_src(
		&builder1, ip(192, 255, 168, 0), ip(255, 255, 255, 0)
	);
	builder_add_net4_dst(
		&builder1, ip(192, 255, 168, 0), ip(255, 255, 255, 0)
	);
	struct filter_action action1 = build_action(&builder1, 1);

	// init filter
	struct filter filter;
	res = filter_init(&filter, attributes, 2, &action1, 1, &memory_context);
	assert(res == 0);

	{
		struct packet packet = make_packet(
			ip(192, 255, 168, 1), ip(192, 255, 168, 10), 0, 0
		);
		query_filter_and_expect_action(&filter, &packet, 1);
		free_packet(&packet);
	}

	{
		// no action because src ip mismatch
		struct packet packet = make_packet(
			ip(195, 255, 168, 1), ip(192, 255, 168, 10), 0, 0
		);
		query_filter_and_expect_no_actions(&filter, &packet);
		free_packet(&packet);
	}

	{
		// no action because dst ip mismatch
		struct packet packet = make_packet(
			ip(192, 255, 168, 10), ip(195, 255, 168, 1), 0, 0
		);
		query_filter_and_expect_no_actions(&filter, &packet);
		free_packet(&packet);
	}

	free(memory);

	puts("OK!");
	return 0;
}