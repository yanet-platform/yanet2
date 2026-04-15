#include "filter/compiler.h"
#include "filter/filter.h"
#include "filter/query.h"

#include "filter/tests/helpers.h"
#include "lib/utils/packet.h"

#include "logging/log.h"
#include <assert.h>
#include <netinet/in.h>
#include <stdlib.h>

FILTER_COMPILER_DECLARE(sign_ports_compile, port_src, port_dst);
FILTER_QUERY_DECLARE(sign_ports, port_src, port_dst);

static void
query_and_expect_action(
	struct filter *filter,
	uint16_t src_port,
	uint16_t dst_port,
	uint32_t expected
) {
	struct packet p = {0};
	uint8_t sip[NET4_LEN] = {0, 0, 0, 0};
	uint8_t dip[NET4_LEN] = {0, 0, 0, 0};
	int res = fill_packet_net4(
		&p, sip, dip, src_port, dst_port, IPPROTO_UDP, 0
	);
	assert(res == 0);
	struct packet *packet_ptr = &p;
	struct value_range *actions;
	filter_query(filter, sign_ports, &packet_ptr, &actions, 1);
	assert(actions->count >= 1);
	assert(ADDR_OF(&actions->values)[0] == expected);
	free_packet(&p);
}

static void
query_and_expect_no_action(
	struct filter *filter, uint16_t src_port, uint16_t dst_port
) {
	struct packet p = {0};
	uint8_t sip[NET4_LEN] = {0, 0, 0, 0};
	uint8_t dip[NET4_LEN] = {0, 0, 0, 0};
	int res = fill_packet_net4(
		&p, sip, dip, src_port, dst_port, IPPROTO_UDP, 0
	);
	assert(res == 0);
	struct packet *packet_ptr = &p;
	struct value_range *actions;
	filter_query(filter, sign_ports, &packet_ptr, &actions, 1);
	assert(actions->count == 0);
	free_packet(&p);
}

static void
test_src_dst_ports(void *memory) {
	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	// action 1:
	//   src_port: [5..7]
	//   dst_port: [1..5]
	struct filter_rule_builder builder1;
	builder_init(&builder1);
	builder_add_port_src_range(&builder1, 5, 7);
	builder_add_port_dst_range(&builder1, 1, 5);
	struct filter_rule action1 = build_rule(&builder1);

	// action 2:
	//   src_port: [6..8]
	//   dst_port: [3..4]
	struct filter_rule_builder builder2;
	builder_init(&builder2);
	builder_add_port_src_range(&builder2, 6, 8);
	builder_add_port_dst_range(&builder2, 3, 4);
	struct filter_rule action2 = build_rule(&builder2);

	const struct filter_rule *action_ptrs[2] = {&action1, &action2};

	// init filter
	struct filter f;
	res = filter_init(
		&f, sign_ports_compile, action_ptrs, 2, &memory_context
	);
	assert(res == 0);

	query_and_expect_action(&f, 6, 3, 0);
	query_and_expect_action(&f, 8, 3, 1);

	filter_free(&f, sign_ports_compile);
}

static void
src_dst_ports(void *memory) {
	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	// rules
	struct filter_rule_builder builder1;
	builder_init(&builder1);
	builder_add_port_src_range(&builder1, 1024, 5016);
	builder_add_port_dst_range(&builder1, 500, 50000);
	struct filter_rule action1 = build_rule(&builder1);

	struct filter_rule_builder builder2;
	builder_init(&builder2);
	builder_add_port_src_range(&builder2, 30, 500);
	builder_add_port_dst_range(&builder2, 400, 12040);
	struct filter_rule action2 = build_rule(&builder2);

	struct filter_rule_builder builder3;
	builder_init(&builder3);
	builder_add_port_src_range(&builder3, 100, 2014);
	builder_add_port_dst_range(&builder3, 5000, 15000);
	struct filter_rule action3 = build_rule(&builder3);

	const struct filter_rule *action_ptrs[3] = {
		&action1, &action2, &action3
	};

	struct filter f;
	res = filter_init(
		&f, sign_ports_compile, action_ptrs, 3, &memory_context
	);
	assert(res == 0);

	query_and_expect_action(&f, 30, 400, 1);
	query_and_expect_action(&f, 35, 445, 1);
	query_and_expect_action(&f, 120, 6000, 1);
	query_and_expect_action(&f, 300, 12040, 1);

	query_and_expect_action(&f, 300, 12041, 2);
	query_and_expect_action(&f, 300, 14900, 2);
	query_and_expect_action(&f, 300, 15000, 2);

	query_and_expect_action(&f, 600, 14000, 2);
	query_and_expect_action(&f, 1024, 14000, 0);
	query_and_expect_action(&f, 2000, 13000, 0);
	query_and_expect_action(&f, 5000, 500, 0);
	query_and_expect_action(&f, 5000, 50000, 0);
	query_and_expect_action(&f, 5016, 500, 0);

	query_and_expect_no_action(&f, 5017, 3000);
	query_and_expect_no_action(&f, 20, 3000);

	filter_free(&f, sign_ports_compile);
}

static void
test_any_port(void *memory) {
	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	// rule 1: src [1024..5016], dst any
	struct filter_rule_builder builder1;
	builder_init(&builder1);
	builder_add_port_src_range(&builder1, 1024, 5016);
	builder_add_port_dst_range(&builder1, 0, 65535);
	struct filter_rule action1 = build_rule(&builder1);

	// rule 2: src any, dst [400..12040]
	struct filter_rule_builder builder2;
	builder_init(&builder2);
	builder_add_port_src_range(&builder2, 0, 65535);
	builder_add_port_dst_range(&builder2, 400, 12040);
	struct filter_rule action2 = build_rule(&builder2);

	// rule 3: src [100..2014], dst [5000..15000]
	struct filter_rule_builder builder3;
	builder_init(&builder3);
	builder_add_port_src_range(&builder3, 100, 2014);
	builder_add_port_dst_range(&builder3, 5000, 15000);
	struct filter_rule action3 = build_rule(&builder3);

	const struct filter_rule *action_ptrs[3] = {
		&action1, &action2, &action3
	};

	struct filter f;
	res = filter_init(
		&f, sign_ports_compile, action_ptrs, 3, &memory_context
	);
	assert(res == 0);

	query_and_expect_action(&f, 1025, 11111, 0);
	query_and_expect_action(&f, 11111, 404, 1);
	query_and_expect_action(&f, 500, 15000, 2);

	query_and_expect_no_action(&f, 1000, 200);

	filter_free(&f, sign_ports_compile);
}

int
main() {
	void *memory = malloc(1 << 24);
	log_enable_name("debug");

	LOG(INFO, "Running test_src_dst_ports...");
	test_src_dst_ports(memory);
	LOG(INFO, "test_src_dst_ports passed");

	LOG(INFO, "Running src_dst_ports...");
	src_dst_ports(memory);
	LOG(INFO, "src_dst_ports passed");

	LOG(INFO, "Running test_any_port...");
	test_any_port(memory);
	LOG(INFO, "test_any_port passed");

	free(memory);
	LOG(INFO, "All tests passed");
	return 0;
}
