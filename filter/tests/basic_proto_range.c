#include "rule.h"
#include "utils.h"

#include "attribute.h"
#include "common/memory_block.h"
#include "filter.h"

#include <netinet/in.h>
#include <rte_ip.h>

#include <assert.h>

#include <lib/logging/log.h>

void
query_tcp_packet(struct filter *filter, uint16_t flags, uint32_t expected) {
	struct packet packet = make_packet4(
		ip(0, 0, 0, 0), ip(0, 0, 0, 0), 0, 0, IPPROTO_TCP, flags, 0
	);
	query_filter_and_expect_action(filter, &packet, expected);
	free_packet(&packet);
}

void
query_udp_packet(struct filter *filter, uint32_t expected) {
	struct packet packet = make_packet4(
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
	builder_init(&b1);
	builder_add_proto_range(
		&b1, 256 * IPPROTO_TCP, 256 * IPPROTO_TCP + 255
	);
	struct filter_rule r1 = build_rule(&b1, 1);

	struct filter_rule_builder b2;
	builder_init(&b2);
	builder_add_proto_range(
		&b2, 256 * IPPROTO_UDP, 256 * IPPROTO_UDP + 255
	);
	struct filter_rule r2 = build_rule(&b2, 2);

	struct filter_rule rules[2] = {r1, r2};

	const struct filter_attribute *attrs[1] = {&attribute_proto_range};

	struct filter filter;

	LOG(INFO, "filter init...");
	res = filter_init(&filter, attrs, 1, rules, 2, &memory_context);
	assert(res == 0);

	LOG(INFO, "query tcp packet...");
	query_tcp_packet(&filter, 0, 1);

	LOG(INFO, "query udp packet...");
	query_udp_packet(&filter, 2);

	filter_free(&filter);
}

int
main() {
	log_enable_name("debug");

	void *memory = malloc(1 << 24);

	test_proto_1(memory);

	free(memory);

	LOG(INFO, "passed");

	return 0;
}
