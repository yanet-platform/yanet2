#include "attribute.h"
#include "common/memory_block.h"
#include "filter/filter.h"
#include "utils.h"
#include <assert.h>

////////////////////////////////////////////////////////////////////////////////

#define MEMORY (size_t)(1ull << 30)

////////////////////////////////////////////////////////////////////////////////

void
test1(void *memory) {
	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, MEMORY);

	struct memory_context memory_context;
	int ret = memory_context_init(&memory_context, "test", &allocator);
	assert(ret == 0);

	// build rules
	struct filter_rule_builder b;
	builder_init(&b);
	builder_add_net4_src(
		&b, ip(0xff, 0xff, 32, 0), ip(0xff, 0xff, 0xff, 0)
	);
	builder_add_net4_dst(
		&b, ip(0xff, 0xff, 33, 0), ip(0xff, 0xff, 0xff, 0)
	);
	builder_add_port_src_range(&b, 288, 788);
	builder_add_port_dst_range(&b, 388, 888);
	struct filter_rule rule = build_rule(&b, 289);

	// filter attributes
	const struct filter_attribute *attrs[] = {
		&attribute_proto,
		&attribute_net4_src,
		&attribute_net4_dst,
		&attribute_port_src,
		&attribute_port_dst
	};
	struct filter filter;
	ret = filter_init(&filter, attrs, 5, &rule, 1, &memory_context);
	assert(ret == 0);

	struct packet packet = make_packet(
		ip(0xff, 0xff, 32, 1),
		ip(0xff, 0xff, 33, 1),
		288,
		800,
		IPPROTO_UDP,
		0,
		0
	);
	uint32_t *actions;
	uint32_t count;
	ret = filter_query(&filter, &packet, &actions, &count);
	assert(ret == 0);
	assert(count == 1);
	assert(actions[0] == 289);

	free_packet(&packet);
	filter_free(&filter);
}

////////////////////////////////////////////////////////////////////////////////

#define RULES 708

////////////////////////////////////////////////////////////////////////////////

void
test2(void *memory) {
	// build rules
	struct filter_rule rules[RULES];
	struct filter_rule_builder builders[RULES];

	for (uint32_t i = 0; i < RULES; ++i) {
		struct filter_rule_builder *b = &builders[i];

		builder_init(b);
		builder_add_port_src_range(b, i & 127, 500 + (i & 255));
		builder_add_port_dst_range(b, 100 + (i & 127), 600 + (i & 255));
		builder_add_net4_src(
			b, ip(0xff, 0xff, i & 0xff, 0), ip(0xff, 0xff, 0xff, 0)
		);
		builder_add_net4_dst(
			b,
			ip(0xff, 0xff, (i + 1) & 0xff, 0),
			ip(0xff, 0xff, 0xff, 0)
		);
		builder_set_proto(b, IPPROTO_UDP, 0, 0);

		rules[i] = build_rule(b, i + 1);
	}

	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, MEMORY);

	struct memory_context mctx;
	int ret = memory_context_init(&mctx, "test", &allocator);
	assert(ret == 0);

	// build filter
	const struct filter_attribute *attrs[] = {
		&attribute_proto,
		&attribute_net4_src,
		&attribute_net4_dst,
		&attribute_port_src,
		&attribute_port_dst
	};
	struct filter filter;
	ret = filter_init(&filter, attrs, 5, rules, RULES, &mctx);
	assert(ret == 0);

	// query packet
	struct packet packet = make_packet(
		ip(0xff, 0xff, 32, 1),
		ip(0xff, 0xff, 33, 1),
		32,
		132,
		IPPROTO_UDP,
		0,
		0
	);
	uint32_t *actions;
	uint32_t count;
	ret = filter_query(&filter, &packet, &actions, &count);
	assert(ret == 0);
	assert(count == 1);
	assert(actions[0] == 33);

	free_packet(&packet);
	filter_free(&filter);
}

////////////////////////////////////////////////////////////////////////////////

int
main() {
	void *memory = malloc(MEMORY);

	puts("test1...");
	test1(memory);

	puts("test2...");
	test2(memory);

	free(memory);

	puts("OK");

	return 0;
}
