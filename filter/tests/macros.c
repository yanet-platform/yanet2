#include "../filter.h"
#include "util.h"
#include <assert.h>
#include <netinet/in.h>
#include <stdio.h>

////////////////////////////////////////////////////////////////////////////////

void
src_port(void *memory) {
	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	// rule 1
	//	src: 1024-5016
	struct filter_rule_builder builder1;
	builder_init(&builder1);
	builder_add_port_src_range(&builder1, 1024, 5016);
	struct filter_rule rule1 = build_rule(&builder1, 1);

	FILTER_DECLARE(filter, &attribute_port_src);

	FILTER_INIT(filter, &rule1, 1, &memory_context, &res);
	assert(res == 0);

	struct packet packet = make_packet(0, 0, 4000, 0, IPPROTO_UDP, 0, 0);
	uint32_t *actions;
	uint32_t actions_count;
	FILTER_QUERY(filter, &packet, &actions, &actions_count);
	assert(actions_count == 1);
	assert(actions[0] == 1);

	free_packet(&packet);
	FILTER_FREE(filter);
}

////////////////////////////////////////////////////////////////////////////////

int
main() {
	void *memory = malloc(1 << 24);
	src_port(memory);
	free(memory);
	puts("OK");
	return 0;
}