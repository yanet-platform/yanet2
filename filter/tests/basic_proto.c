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
query_tcp_packet(struct filter *filter, uint16_t flags, uint32_t expected) {
	struct packet packet = make_packet(
		ip(0, 0, 0, 0), ip(0, 0, 0, 0), 0, 0, IPPROTO_TCP, flags, 0
	);
	query_filter_and_expect_action(filter, &packet, expected);
	free_packet(&packet);
}

void
query_udp_packet(struct filter *filter, uint32_t expected) {
	struct packet packet = make_packet(
		ip(0, 0, 0, 0), ip(0, 0, 0, 0), 0, 0, IPPROTO_UDP, 0, 0
	);
	query_filter_and_expect_action(filter, &packet, expected);
	free_packet(&packet);
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
	builer_set_proto(&b1, IPPROTO_TCP, 0b101, 0b010);
	struct filter_rule r1 = build_rule(&b1, 1);

	struct filter_rule_builder b2;
	builer_set_proto(&b2, IPPROTO_UDP, 0, 0);
	struct filter_rule r2 = build_rule(&b2, 2);

	struct filter_rule_builder b3;
	builer_set_proto(&b3, PROTO_UNSPEC, 0, 0);
	struct filter_rule r3 = build_rule(&b3, 3);

	struct filter_rule rules[3] = {r1, r2, r3};

	const struct filter_attribute *attrs[1] = {&attribute_proto};

	struct filter filter;
	res = filter_init(&filter, attrs, 1, rules, 3, &memory_context);
	assert(res == 0);

	query_tcp_packet(&filter, 0b101, 1);
	query_tcp_packet(&filter, 0b10101, 1);
	query_tcp_packet(&filter, 0b1101, 1);
	query_tcp_packet(&filter, (1 << 9) - 1 - 2, 1);
	query_tcp_packet(&filter, 0b010, 3);
	query_tcp_packet(&filter, 0b011, 3);
	query_tcp_packet(&filter, 0b1110, 3);

	query_udp_packet(&filter, 2);

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
