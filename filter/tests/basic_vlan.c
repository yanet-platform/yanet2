#include "rule.h"
#include "utils.h"

#include "attribute.h"
#include "common/memory_block.h"
#include "filter.h"

#include <netinet/in.h>
#include <rte_ip.h>

#include <assert.h>
#include <stdio.h>

////////////////////////////////////////////////////////////////////////////////

void
query_packet(struct filter *filter, uint16_t vlan, uint32_t expected) {
	struct packet packet = make_packet(0, 0, 0, 0, IPPROTO_UDP, 0, vlan);
	uint32_t *actions;
	uint32_t actions_count;
	filter_query(filter, &packet, &actions, &actions_count);
	assert(actions_count == 1);
	assert(actions[0] == expected);
}

////////////////////////////////////////////////////////////////////////////////

void
test_proto_1(void *memory) {
	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	struct filter_rule_builder b1;
	builder_set_vlan(&b1, 10);
	struct filter_rule r1 = build_rule(&b1, 1);

	struct filter_rule_builder b2;
	builder_set_vlan(&b2, 20);
	struct filter_rule r2 = build_rule(&b2, 2);

	struct filter_rule_builder b3;
	builder_set_vlan(&b3, 30);
	struct filter_rule r3 = build_rule(&b3, 3);

	struct filter_rule rules[3] = {r1, r2, r3};

	const struct filter_attribute *attributes[] = {&attribute_vlan};

	struct filter filter;
	res = filter_init(&filter, attributes, 1, rules, 3, &memory_context);
	assert(res == 0);

	query_packet(&filter, 10, 1);
	query_packet(&filter, 20, 2);
	query_packet(&filter, 30, 3);

	filter_free(&filter);
}

int
main() {
	void *memory = malloc(1 << 24);

	test_proto_1(memory);

	free(memory);

	puts("OK");

	return 0;
}