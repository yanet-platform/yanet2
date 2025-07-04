#include "attribute.h"
#include "common/memory_block.h"
#include "filter.h"

#include <assert.h>
#include <stdio.h>

int
main() {
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	void *memory = malloc(1 << 24); // 16MB
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	struct filter_attribute attributes[2] = {
		src_port_attribute, dst_port_attribute
	};

	struct filter_net6 dummy_net6 = {0, 0, NULL, NULL};
	struct filter_net4 dummy_net4 = {0, 0, NULL, NULL};

	struct filter_port_range src_port_range_1 = {5, 7};
	struct filter_port_range dst_port_range_1 = {1, 5};
	struct filter_transport f1 = {
		0, 1, 1, &src_port_range_1, &dst_port_range_1
	};

	struct filter_port_range src_port_range_2 = {6, 8};
	struct filter_port_range dst_port_range_2 = {3, 4};
	struct filter_transport f2 = {
		0, 1, 1, &src_port_range_2, &dst_port_range_2
	};

	struct filter_action actions[2] = {
		{dummy_net6, dummy_net4, f1, 1},
		{dummy_net6, dummy_net4, f2, 2},
	};

	struct filter filter;
	res = filter_init(&filter, attributes, 2, actions, 2, &memory_context);
	assert(res == 0);

	struct packet_info packet = {.src_port = 6, .dst_port = 2};

	uint32_t *result_actions;
	uint32_t result_count;
	res = filter_query(&filter, packet, &result_actions, &result_count);
	assert(res == 0);

	assert(result_count == 1);
	assert(result_actions[0] == 1);

	puts("OK!");

	return 0;
}